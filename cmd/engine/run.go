package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"bourse/internal/anomaly"
	"bourse/internal/bus"
	"bourse/internal/calendar"
	"bourse/internal/flow"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// output is one message computed from one snapshot, not yet published.
type output struct {
	subject string
	v       any
	id      string // explicit message ID (re-emitted outputs keep their original one); "" = prefix:index
}

// processor owns the engine state. Not safe for concurrent use (see flow.Engine).
type processor struct {
	eng   *flow.Engine
	radar *anomaly.Radar
	pub   bus.Publisher
	retry []time.Duration // publish backoff; len+1 attempts per output

	// NATS path only.
	js      jsBus
	epoch   int64  // MD stream creation time: makes output IDs unique per stream lifetime
	lastSeq uint64 // highest MD sequence applied to state AND fully published (or reported undecodable)
	cpDay   string // trading day recorded in the checkpoint (for this MD epoch)

	// loss is set by recovery when snapshots were discarded from MD before this engine applied
	// them (or before it could replay them); logged only: the affected days are marked partial by
	// the flow engine's late-start rule (their first retained snapshot is a late baseline).
	loss        *lossInfo
	cal         *calendar.Calendar
	unmapped    map[string]bool // unmapped instruments seen on unmappedDay
	unmappedDay string

	guard          func() error // e.g. lease validity; checked before every publish (nil = none)
	allowSynthetic bool         // accept SYN* snapshots (ALLOW_SYNTHETIC_ON_BUS=1, local demos only)
	retryPoisonSeq uint64       // operator override: process this seq despite POISON_SUSPECT
}

func newProcessor(cfg flow.Config, pub bus.Publisher) *processor {
	rc := anomaly.DefaultConfig()
	rc.Sessions = cfg.Sessions // one calendar for every time rule
	if cfg.Sessions == nil {
		cfg.Sessions = calendar.Default()
	}
	return &processor{eng: flow.New(cfg), radar: anomaly.New(rc), pub: pub, cal: cfg.Sessions,
		retry:    []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second},
		unmapped: map[string]bool{}}
}

// dsmID is the message ID of an instrument's DAY_START_MISSED for one day: stable across
// restarts and the same whether it was published live or re-emitted after a recovery replay.
func (p *processor) dsmID(ins, day string) string {
	return fmt.Sprintf("eng:%d:dsm:%s:%s", p.epoch, ins, day)
}

// noteUnmapped logs, per trading day, when the 1st, 10th, 100th, 1000th instrument without a
// class in the session calendar is seen (class "unknown": union session, no per-class aggregates).
func (p *processor) noteUnmapped(s *model.Snapshot) {
	if p.cal.Mapped(s.InsCode) {
		return
	}
	if day := tehran.TradingDay(s.SourceTime); day != p.unmappedDay {
		p.unmappedDay, p.unmapped = day, map[string]bool{}
	}
	if p.unmapped[s.InsCode] {
		return
	}
	p.unmapped[s.InsCode] = true
	switch n := len(p.unmapped); n {
	case 1, 10, 100, 1000:
		log.Printf("engine: WARNING: %d instrument(s) without a class in the session calendar on %s (latest %s; class %q, see docs/sessions.md)",
			n, p.unmappedDay, s.InsCode, calendar.Unknown)
	}
}

// lossInfo describes snapshots discarded from MD that the current state never saw.
type lossInfo struct {
	unprocessedBefore time.Time // never-applied snapshots stored before this were discarded
	replayDay         string    // applied snapshots of this trading day are missing from the replay
	replayBefore      time.Time // as replayDay when the day is unknown: those stored before this
}

// compute runs one snapshot through the engine and radar. It mutates state exactly once per
// snapshot, whether or not the outputs are published afterwards (recovery discards them).
func (p *processor) compute(s model.Snapshot) []output {
	var outs []output
	p.noteUnmapped(&s)
	r := p.eng.Process(s)
	for _, i := range r.Issues {
		outs = append(outs, output{subject: bus.SubjQuality(s.InsCode), v: i})
	}
	for _, e := range r.Events {
		outs = append(outs, output{subject: bus.SubjFlow(s.InsCode), v: e})
	}
	if r.Game != nil {
		outs = append(outs, output{subject: bus.SubjGame(s.InsCode), v: r.Game})
	}
	if r.Window != nil {
		outs = append(outs, output{subject: bus.SubjWindow(s.InsCode), v: r.Window})
		if !r.Window.Partial { // AI-02 must not run on a partial window
			if ev := p.radar.Divergence(r.Window); ev != nil {
				outs = append(outs, output{subject: bus.SubjAI(s.InsCode), v: ev})
			}
		}
	}
	for _, ev := range p.radar.Observe(r.Interval) {
		outs = append(outs, output{subject: bus.SubjAI(s.InsCode), v: ev})
	}
	return outs
}

type idPublisher interface {
	PublishID(subject, id string, v any) error
}

// batchPublisher sends several messages in one attempt (bus.JetStream).
type batchPublisher interface {
	PublishBatch(items []bus.BatchItem) []error
}

func outputID(o output, idPrefix string, i int) string {
	if o.id == "" && idPrefix != "" {
		return fmt.Sprintf("%s:%d", idPrefix, i)
	}
	return o.id
}

// publish sends outs in order. A failed output is retried (the SAME value, same message ID)
// with bounded backoff, then the error is returned; outputs already sent are not resent.
// idPrefix != "" gives output i the message ID idPrefix:i (JetStream de-duplication).
// keepAlive (may be nil) is called at least every second while publishing, so a slow but
// progressing publish never outlives the consumer's AckWait.
func (p *processor) publish(ctx context.Context, outs []output, idPrefix string, keepAlive func()) error {
	idp, useID := p.pub.(idPublisher)
	last := time.Now()
	alive := func() {
		if keepAlive != nil && time.Since(last) >= time.Second {
			keepAlive()
			last = time.Now()
		}
	}
	var guardErr error
	done := make([]bool, len(outs))
	// First attempt: every output at once (one round trip instead of one per output), after the
	// lease guard; anything not stored is retried one by one below, same value and message ID.
	if bp, ok := p.pub.(batchPublisher); ok && len(outs) > 1 {
		if p.guard != nil {
			if err := p.guard(); err != nil {
				return fmt.Errorf("before output 1/%d: %w", len(outs), err)
			}
		}
		items := make([]bus.BatchItem, len(outs))
		for i, o := range outs {
			items[i] = bus.BatchItem{Subject: o.subject, ID: outputID(o, idPrefix, i), V: o.v}
		}
		for i, err := range bp.PublishBatch(items) {
			done[i] = err == nil
		}
	}
	for i, o := range outs {
		if done[i] {
			continue
		}
		alive()
		id := outputID(o, idPrefix, i)
		err := bus.Retry(ctx, p.retry, alive, func() error {
			if p.guard != nil { // before EVERY attempt, retries included
				if guardErr = p.guard(); guardErr != nil {
					return bus.Permanent(guardErr) // no point retrying without the lease
				}
			}
			if useID && id != "" {
				return idp.PublishID(o.subject, id, o.v)
			}
			return p.pub.Publish(o.subject, o.v)
		})
		if guardErr != nil {
			return fmt.Errorf("before output %d/%d: %w", i+1, len(outs), guardErr)
		}
		if err != nil {
			return fmt.Errorf("output %d/%d on %s: %w", i+1, len(outs), o.subject, err)
		}
	}
	return nil
}

// decode parses a snapshot delivered on subject and checks that it belongs to that subject.
func decode(subject string, data []byte) (model.Snapshot, error) {
	s, err := model.DecodeSnapshot(data)
	if err != nil {
		return s, err
	}
	if want := strings.TrimPrefix(subject, "md.snap."); s.InsCode != want {
		return s, fmt.Errorf("ins_code %q does not match subject %s", s.InsCode, subject)
	}
	return s, nil
}

// runNDJSON reads snapshot envelopes from r until EOF (BUS=ndjson).
func (p *processor) runNDJSON(r io.Reader) (n, bad int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var env struct {
			Subject string          `json:"subject"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil || !strings.HasPrefix(env.Subject, "md.snap.") {
			bad++
			continue
		}
		s, err := decode(env.Subject, env.Data)
		if err != nil {
			bad++
			continue
		}
		n++
		if err := p.publish(context.Background(), p.compute(s), "", nil); err != nil {
			return n, bad, fmt.Errorf("publish: %w", err)
		}
	}
	return n, bad, sc.Err()
}

const (
	engineDurable    = "engine"
	snapFilter       = "md.snap.>"
	poisonReleaseKey = "engine_poison_released" // service_state key: last seq released by ENGINE_RETRY_POISON_SEQ
	poisonAfter      = 5                        // deliveries of one MD message before the engine stops with POISON_SUSPECT
)

// engineConsumer is the durable consumer spec. MaxDeliver is unlimited on purpose: every
// engine handler error is either Permanent (terminated at once) or Abort (process exits), so
// the only redeliveries are after crashes/aborts, and counting those would silently drop a
// snapshot whose outputs were never published. Instead the engine itself stops on a message
// delivered poisonAfter times (POISON_SUSPECT) and keeps it for investigation. Never drop.
func engineConsumer() bus.ConsumerSpec {
	return bus.ConsumerSpec{Stream: bus.StreamMD, Durable: engineDurable, Filter: snapFilter, MaxDeliver: -1}
}

// jsBus is what the NATS path needs from *bus.JetStream (narrowed so tests can fake it).
type jsBus interface {
	AckFloor(ctx context.Context, stream, durable string) (uint64, error)
	GetMsg(ctx context.Context, stream string, seq uint64) (string, []byte, time.Time, error)
	StreamState(ctx context.Context, stream string) (bus.StreamInfo, error)
	Replay(ctx context.Context, stream, filter string, start time.Time, upTo uint64, fn func(bus.Msg) error) (uint64, error)
	StreamCreated(ctx context.Context, stream string) (time.Time, error)
	Consume(ctx context.Context, spec bus.ConsumerSpec, fn func(bus.Msg) error) error
	LoadCheckpoint(ctx context.Context, name string) (bus.Checkpoint, bool, error)
	SaveCheckpoint(ctx context.Context, name string, cp bus.Checkpoint) error
	StateGet(ctx context.Context, key string) ([]byte, bool, error)
	StatePut(ctx context.Context, key string, val []byte) error
}

// recoveryStart picks where replay begins: 1h before the Tehran day start of the snapshot at
// the ack floor, compared against stream STORE time (the margin keeps snapshots stored a little
// before their source time), and never after the floor message's own store time (a source
// clock running ahead must not skip the floor itself).
func recoveryStart(floorSource, floorStored time.Time) time.Time {
	start := tehran.DayStart(floorSource).Add(-time.Hour)
	if floorStored.Before(start) {
		start = floorStored
	}
	return start
}

// recoverState rebuilds engine and radar state after a restart: it replays, WITHOUT publishing,
// md.snap.> from the start of the Tehran trading day of the last acknowledged snapshot up to the
// durable's ack floor. Everything after the floor is still owned by the durable consumer. The
// radar only re-warms on that window (its longer history is not restored). Snapshots of the
// previous day inside the 1h margin only rebuild that day's state; a later snapshot of today
// resets the instrument, and an earlier-day snapshot after today's is OUT_OF_ORDER (flow).
//
// The floor's trading day comes from the floor message, or from the checkpoint when that
// message has been discarded. Loss is classified so that only affected days become partial:
// never-applied snapshots discarded (first retained seq > floor+1), and applied snapshots of the
// floor's day missing from the replay.
func (p *processor) recoverState(ctx context.Context) (replayed int, err error) {
	floor, err := p.js.AckFloor(ctx, bus.StreamMD, engineDurable)
	if err != nil || floor == 0 {
		return 0, err
	}
	st, err := p.js.StreamState(ctx, bus.StreamMD)
	if err != nil {
		return 0, err
	}
	cp, cpOK, err := p.js.LoadCheckpoint(ctx, engineDurable)
	if err != nil {
		return 0, err
	}
	if cpOK && cp.Epoch != p.epoch {
		log.Printf("engine: recovery: checkpoint belongs to an earlier MD stream; ignored")
		cpOK = false
	}
	if cpOK {
		p.cpDay = cp.Day
	}
	var start time.Time // zero = whole retained stream
	floorDay := ""
	subj, data, stored, gerr := p.js.GetMsg(ctx, bus.StreamMD, floor)
	if gerr != nil && !errors.Is(gerr, jetstream.ErrMsgNotFound) {
		return 0, fmt.Errorf("recovery: read ack floor message %d: %w", floor, gerr) // not proof of loss: retry on restart
	}
	switch {
	case gerr == nil:
		if s, err := decode(subj, data); err == nil {
			start, floorDay = recoveryStart(s.SourceTime, stored), tehran.TradingDay(s.SourceTime)
		} else { // it was terminated as UNDECODABLE: fall back to its store day
			start, floorDay = recoveryStart(stored, stored), tehran.TradingDay(stored)
		}
	case cpOK && cp.Seq <= floor:
		if d, err := time.ParseInLocation("2006-01-02", cp.Day, tehran.Loc); err == nil {
			start, floorDay = d.Add(-time.Hour), cp.Day
		}
		log.Printf("engine: recovery: message at ack floor %d unavailable (%v); its trading day %s comes from the checkpoint", floor, gerr, cp.Day)
	default:
		log.Printf("engine: recovery: message at ack floor %d unavailable (%v) and no usable checkpoint; replaying the whole stream", floor, gerr)
	}
	var loss lossInfo
	if st.FirstSeq > floor+1 {
		loss.unprocessedBefore = st.FirstStored
	}
	if gerr != nil || (st.FirstSeq > 1 && !start.IsZero() && st.FirstStored.After(start)) {
		if floorDay != "" {
			loss.replayDay = floorDay
		} else {
			loss.replayBefore = st.FirstStored
		}
	}
	if loss != (lossInfo{}) {
		p.loss = &loss
		log.Printf("engine: recovery: MD starts at seq %d (stored %s), ack floor %d, floor day %q: snapshots were lost; affected days are marked partial by the late-start rule (DAY_START_MISSED)",
			st.FirstSeq, st.FirstStored.UTC().Format(time.RFC3339), floor, floorDay)
	}
	replayIssues := map[string]output{}
	last, err := p.js.Replay(ctx, bus.StreamMD, snapFilter, start, floor, func(m bus.Msg) error {
		s, err := decode(m.Subject, m.Data)
		if err != nil || (model.IsSynthetic(&s) && !p.allowSynthetic) {
			return nil // was terminated (and reported) when first consumed
		}
		for _, o := range p.compute(s) {
			if iss, ok := o.v.(model.QualityIssue); ok && iss.Code == quality.DayStartMissed {
				// Keyed by instrument and day: a later replayed baseline of the same day (after a
				// re-baseline) keeps the first issue; its stable ID de-duplicates across processes.
				key := s.InsCode + "|" + tehran.TradingDay(s.SourceTime)
				if _, seen := replayIssues[key]; !seen {
					replayIssues[key] = output{subject: bus.SubjQuality(s.InsCode), v: iss,
						id: p.dsmID(s.InsCode, tehran.TradingDay(s.SourceTime))}
				}
			}
		}
		replayed++
		return nil
	})
	if err != nil {
		return replayed, err
	}
	if gerr == nil && last != floor {
		return replayed, fmt.Errorf("recovery replay stopped at seq %d before the ack floor %d", last, floor)
	}
	// Replay outputs are discarded. When the replay was complete the previous process already
	// published every DAY_START_MISSED it contains; when snapshots were lost, a replayed baseline
	// may be new (the lost ones were the real baseline), so its issue is published now, before
	// consuming, under the stable per-(instrument, day) ID (a duplicate of an earlier publish is
	// de-duplicated within the window). Never dropped if the instrument does not trade again.
	if p.loss != nil && len(replayIssues) > 0 {
		keys := make([]string, 0, len(replayIssues))
		for k := range replayIssues {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		outs := make([]output, 0, len(keys))
		for _, k := range keys {
			outs = append(outs, replayIssues[k])
		}
		if err := p.publish(ctx, outs, "", nil); err != nil {
			return replayed, fmt.Errorf("recovery: publish DAY_START_MISSED: %w", err)
		}
		log.Printf("engine: recovery: published %d DAY_START_MISSED issue(s) from the replay", len(outs))
	}
	p.lastSeq = floor
	return replayed, nil
}

// handle is the durable consumer's handler. It applies each MD sequence to state at most once
// and in order: already-applied sequences (redelivery after a lost ack or a slow publish) are
// acked without recomputing; sequences the consumer skipped (terminated elsewhere, a late ack
// from a previous process) are fetched and applied first.
func (p *processor) handle(ctx context.Context, m bus.Msg) error {
	if m.StreamSeq <= p.lastSeq {
		return nil // applied and fully published before: just ack again
	}
	// Checked before the gap fill: a crash while applying a skipped sequence also counts as a
	// delivery of this message, so the report names the whole range.
	if m.NumDelivered >= poisonAfter {
		if m.StreamSeq != p.retryPoisonSeq {
			return p.poison(ctx, m)
		}
		// One-shot: recorded before processing, so a crash while processing it again cannot loop
		// (the next start finds the release used and reports POISON_SUSPECT again).
		if err := p.js.StatePut(ctx, poisonReleaseKey, []byte(strconv.FormatUint(m.StreamSeq, 10))); err != nil {
			return bus.Abort(fmt.Errorf("record poison release: %w", err))
		}
		p.retryPoisonSeq = 0
		log.Printf("engine: WARNING: processing MD seq %d once more despite %d deliveries (ENGINE_RETRY_POISON_SEQ)", m.StreamSeq, m.NumDelivered)
	}
	if m.StreamSeq > p.lastSeq+1 {
		if err := p.fillGap(ctx, m.StreamSeq); err != nil {
			return err
		}
	}
	return p.apply(ctx, m, m.InProgress)
}

// poison reports a message that was delivered poisonAfter times without being acknowledged
// (every previous attempt crashed or aborted the engine) and stops the engine WITHOUT
// processing, acking or terminating it: it stays first in line until someone investigates.
func (p *processor) poison(ctx context.Context, m bus.Msg) error {
	ins := strings.TrimPrefix(m.Subject, "md.snap.")
	seqs := fmt.Sprintf("seq %d", m.StreamSeq)
	from := p.lastSeq + 1
	if st, err := p.js.StreamState(ctx, bus.StreamMD); err == nil && st.FirstSeq > from {
		from = st.FirstSeq // earlier ones are gone, they cannot be the cause
	}
	if from < m.StreamSeq {
		seqs = fmt.Sprintf("seqs %d..%d (the crash may be in any of them; the earlier ones were fetched to fill a gap)", from, m.StreamSeq)
	}
	iss := model.QualityIssue{InsCode: ins, Code: quality.PoisonSuspect, At: m.Stored,
		Detail: fmt.Sprintf("MD %s: delivered %d times without being processed; engine stopped, message kept for investigation", seqs, m.NumDelivered)}
	err := fmt.Errorf("POISON_SUSPECT: MD %s (%s) delivered %d times without success; not processing it. "+
		"Inspect it (stream MD), fix the cause, then restart with ENGINE_RETRY_POISON_SEQ=%d to process it once more; "+
		"the engine never skips it on its own", seqs, m.Subject, m.NumDelivered, m.StreamSeq)
	if perr := p.publish(ctx, []output{{subject: bus.SubjQuality(ins), v: iss}}, fmt.Sprintf("eng:%d:%d:poison", p.epoch, m.StreamSeq), m.InProgress); perr != nil {
		err = fmt.Errorf("%w (reporting it also failed: %v)", err, perr)
	}
	return bus.Abort(err)
}

func (p *processor) fillGap(ctx context.Context, upTo uint64) error {
	from := p.lastSeq + 1
	if st, err := p.js.StreamState(ctx, bus.StreamMD); err == nil && st.FirstSeq > from {
		log.Printf("engine: MD sequences %d..%d were discarded before this engine applied them", from, st.FirstSeq-1)
		from = st.FirstSeq
	}
	for seq := from; seq < upTo; seq++ {
		subj, data, stored, err := p.js.GetMsg(ctx, bus.StreamMD, seq)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			log.Printf("engine: MD sequence %d is gone before this engine applied it", seq)
			continue
		}
		if err != nil {
			return bus.Abort(err)
		}
		log.Printf("engine: applying MD sequence %d that the consumer did not deliver", seq)
		if err := p.apply(ctx, bus.Msg{Subject: subj, Data: data, StreamSeq: seq, Stored: stored}, nil); err != nil && !bus.IsPermanent(err) {
			return err
		}
	}
	return nil
}

// apply decodes, computes and publishes one MD message and records it in lastSeq.
func (p *processor) apply(ctx context.Context, m bus.Msg, keepAlive func()) error {
	id := fmt.Sprintf("eng:%d:%d", p.epoch, m.StreamSeq)
	s, derr := decode(m.Subject, m.Data)
	if derr != nil {
		iss := model.QualityIssue{InsCode: strings.TrimPrefix(m.Subject, "md.snap."), Code: quality.Undecodable,
			Detail: fmt.Sprintf("stream seq %d: payload is not a valid snapshot: %v", m.StreamSeq, derr), At: m.Stored}
		if err := p.publish(ctx, []output{{subject: bus.SubjQuality(iss.InsCode), v: iss}}, id, keepAlive); err != nil {
			return bus.Abort(err)
		}
		p.lastSeq = m.StreamSeq
		return bus.Permanent(derr)
	}
	if model.IsSynthetic(&s) && !p.allowSynthetic {
		// Rule 5: never compute or publish metrics for SYN* on the bus. No quality issue either
		// (it would itself carry a SYN* instrument).
		p.lastSeq = m.StreamSeq
		return bus.Permanent(fmt.Errorf("synthetic instrument %s refused (ALLOW_SYNTHETIC_ON_BUS is not set)", s.InsCode))
	}
	// From here on Process() has run: never Nak. Retry the same outputs, else abort.
	outs := p.compute(s)
	for i := range outs {
		if iss, ok := outs[i].v.(model.QualityIssue); ok && iss.Code == quality.DayStartMissed {
			outs[i].id = p.dsmID(s.InsCode, tehran.TradingDay(s.SourceTime))
		}
	}
	if err := p.publish(ctx, outs, id, keepAlive); err != nil {
		return bus.Abort(err)
	}
	p.lastSeq = m.StreamSeq
	p.checkpoint(ctx, s, m.StreamSeq)
	return nil
}

// checkpoint records the first MD sequence of each new trading day, so a restart can place its
// ack floor on a day after the floor message itself is discarded. A snapshot whose source time
// is implausibly ahead of its ingest time does not move it.
func (p *processor) checkpoint(ctx context.Context, s model.Snapshot, seq uint64) {
	day := tehran.TradingDay(s.SourceTime)
	if day == p.cpDay || s.SourceTime.After(s.IngestTime.Add(time.Hour)) {
		return
	}
	if p.guard != nil && p.guard() != nil {
		return
	}
	if err := p.js.SaveCheckpoint(ctx, engineDurable, bus.Checkpoint{Seq: seq, Day: day, Epoch: p.epoch}); err != nil {
		log.Printf("engine: checkpoint: %v (recovery will be conservative)", err) // retried on the next snapshot
		return
	}
	p.cpDay = day
}

// runNATS consumes md.snap.> through the durable "engine" consumer (BUS=nats). It returns nil
// on ctx cancellation, or an error (exit non-zero) when outputs cannot be published.
// With exitWhenIdle it also returns nil once every message currently in MD is acknowledged
// (make demo-nats, batch backfills).
func (p *processor) runNATS(ctx context.Context, js jsBus, spec bus.ConsumerSpec, exitWhenIdle bool) error {
	p.js = js
	created, err := js.StreamCreated(ctx, bus.StreamMD)
	if err != nil {
		return err
	}
	p.epoch = created.UnixNano()
	if p.retryPoisonSeq != 0 {
		if b, ok, err := js.StateGet(ctx, poisonReleaseKey); err != nil {
			return err
		} else if ok && string(b) == strconv.FormatUint(p.retryPoisonSeq, 10) {
			log.Printf("engine: ENGINE_RETRY_POISON_SEQ=%d was already used once; ignoring it (remove it, or investigate again)", p.retryPoisonSeq)
			p.retryPoisonSeq = 0
		}
	}
	n, err := p.recoverState(ctx)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}
	log.Printf("engine: recovered state from %d snapshots (ack floor %d)", n, p.lastSeq)
	if exitWhenIdle {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		go func() {
			t := time.NewTicker(250 * time.Millisecond)
			defer t.Stop()
			for {
				st, err1 := js.StreamState(ctx, bus.StreamMD)
				floor, err2 := js.AckFloor(ctx, bus.StreamMD, engineDurable)
				if err1 == nil && err2 == nil && floor >= st.LastSeq {
					log.Printf("engine: caught up at MD seq %d; exiting (exit-when-idle)", floor)
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	err = js.Consume(ctx, spec, func(m bus.Msg) error { return p.handle(ctx, m) })
	if err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil // shutdown requested; an Abort error is still returned even during shutdown
	}
	return err
}

// leaseBucket holds the single-engine lease (P-02); the key is the durable's name.
const leaseBucket = "engine_lease"

// leaser is what runLeased needs from *bus.JetStream besides jsBus.
type leaser interface {
	AcquireLease(ctx context.Context, bucket, key, holder string, ttl time.Duration) (*bus.Lease, error)
	EnsureStreams(ctx context.Context) error
	WatchLimits(ctx context.Context, interval time.Duration)
}

// runLeased runs the NATS engine only while it holds the single-engine lease. It takes the
// lease before touching anything (streams, consumer, state): a second engine refuses to start
// with bus.ErrLeaseHeld. While running, the lease is checked before every publish and every ack,
// and the engine stops with bus.ErrLeaseLost once it is lost or not renewed in time. Two engines
// on one durable would each see only part of the snapshots.
func runLeased(ctx context.Context, js interface {
	jsBus
	leaser
}, p *processor, spec bus.ConsumerSpec, exitWhenIdle bool, holder string, ttl time.Duration) error {
	lease, err := js.AcquireLease(ctx, leaseBucket, spec.Durable, holder, ttl)
	if err != nil {
		return err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lease.Release(rctx); err != nil {
			log.Printf("engine: release lease: %v", err)
		}
	}()
	log.Printf("engine: holding lease %s/%s as %s (ttl %s)", leaseBucket, spec.Durable, holder, ttl)
	if err := js.EnsureStreams(ctx); err != nil {
		return err
	}
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go js.WatchLimits(lctx, 30*time.Second)
	go func() {
		select {
		case <-lease.Lost():
			cancel()
		case <-lctx.Done():
		}
	}()
	p.guard, spec.BeforeAck = lease.Valid, lease.Valid
	err = p.runNATS(lctx, js, spec, exitWhenIdle)
	if lerr := lease.Valid(); lerr != nil || errors.Is(err, bus.ErrLeaseLost) {
		lost := fmt.Errorf("stopping: %w (another engine may have taken over)", bus.ErrLeaseLost)
		if err != nil && !errors.Is(err, bus.ErrLeaseLost) && !errors.Is(err, context.Canceled) {
			return errors.Join(lost, err) // keep the real failure visible
		}
		return lost
	}
	return err
}
