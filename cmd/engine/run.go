package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"bourse/internal/anomaly"
	"bourse/internal/bus"
	"bourse/internal/flow"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// output is one message computed from one snapshot, not yet published.
type output struct {
	subject string
	v       any
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
	// partialBefore is set when recovery found part of its replay window already discarded from
	// MD: snapshots stored before it are lost, so days and 10-minute windows starting before it
	// are published with Partial=true, and each instrument gets one RECOVERY_TRUNCATED issue.
	partialBefore time.Time
	flagged       map[string]bool // instruments already given a RECOVERY_TRUNCATED issue
}

func newProcessor(cfg flow.Config, pub bus.Publisher) *processor {
	return &processor{eng: flow.New(cfg), radar: anomaly.New(anomaly.DefaultConfig()), pub: pub,
		retry:   []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second},
		flagged: map[string]bool{}}
}

// compute runs one snapshot through the engine and radar. It mutates state exactly once per
// snapshot, whether or not the outputs are published afterwards (recovery discards them).
func (p *processor) compute(s model.Snapshot) []output {
	var outs []output
	r := p.eng.Process(s)
	for _, i := range r.Issues {
		outs = append(outs, output{bus.SubjQuality(s.InsCode), i})
	}
	for _, e := range r.Events {
		outs = append(outs, output{bus.SubjFlow(s.InsCode), e})
	}
	if r.Game != nil {
		outs = append(outs, output{bus.SubjGame(s.InsCode), r.Game})
	}
	if r.Window != nil {
		outs = append(outs, output{bus.SubjWindow(s.InsCode), r.Window})
		if ev := p.radar.Divergence(r.Window); ev != nil {
			outs = append(outs, output{bus.SubjAI(s.InsCode), ev})
		}
	}
	for _, ev := range p.radar.Observe(r.Interval) {
		outs = append(outs, output{bus.SubjAI(s.InsCode), ev})
	}
	return outs
}

type idPublisher interface {
	PublishID(subject, id string, v any) error
}

// publish sends outs in order. A failed output is retried (the SAME value, same message ID)
// with bounded backoff, then the error is returned; outputs already sent are not resent.
// idPrefix != "" gives output i the message ID idPrefix:i (JetStream de-duplication).
// keepAlive (may be nil) is called at least every second while publishing, so a slow but
// progressing publish never outlives the consumer's AckWait.
func (p *processor) publish(ctx context.Context, outs []output, idPrefix string, keepAlive func()) error {
	idp, useID := p.pub.(idPublisher)
	useID = useID && idPrefix != ""
	last := time.Now()
	alive := func() {
		if keepAlive != nil && time.Since(last) >= time.Second {
			keepAlive()
			last = time.Now()
		}
	}
	for i, o := range outs {
		alive()
		err := bus.Retry(ctx, p.retry, alive, func() error {
			if useID {
				return idp.PublishID(o.subject, fmt.Sprintf("%s:%d", idPrefix, i), o.v)
			}
			return p.pub.Publish(o.subject, o.v)
		})
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
	engineDurable = "engine"
	snapFilter    = "md.snap.>"
	poisonAfter   = 5 // deliveries of one MD message before the engine stops with POISON_SUSPECT
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
func (p *processor) recoverState(ctx context.Context) (replayed int, err error) {
	floor, err := p.js.AckFloor(ctx, bus.StreamMD, engineDurable)
	if err != nil || floor == 0 {
		return 0, err
	}
	st, err := p.js.StreamState(ctx, bus.StreamMD)
	if err != nil {
		return 0, err
	}
	var start time.Time // zero = whole retained stream
	subj, data, stored, gerr := p.js.GetMsg(ctx, bus.StreamMD, floor)
	switch {
	case gerr != nil:
		log.Printf("engine: recovery: message at ack floor %d unavailable (%v); replaying the whole stream", floor, gerr)
	default:
		if s, err := decode(subj, data); err == nil {
			start = recoveryStart(s.SourceTime, stored)
		} else { // it was terminated as UNDECODABLE: fall back to its store day
			start = recoveryStart(stored, stored)
		}
	}
	// Part of the window already gone (limits/age): state for today would be understated.
	if st.FirstSeq > 1 && (start.IsZero() || st.FirstStored.After(start)) {
		p.partialBefore = st.FirstStored
		log.Printf("engine: recovery: MD starts at seq %d stored %s, after the replay start; totals from before then are incomplete (partial=true, RECOVERY_TRUNCATED)",
			st.FirstSeq, st.FirstStored.UTC().Format(time.RFC3339))
	}
	last, err := p.js.Replay(ctx, bus.StreamMD, snapFilter, start, floor, func(m bus.Msg) error {
		s, err := decode(m.Subject, m.Data)
		if err != nil {
			return nil // was terminated (and reported) when first consumed
		}
		p.compute(s)
		replayed++
		return nil
	})
	if err != nil {
		return replayed, err
	}
	if gerr == nil && last != floor {
		return replayed, fmt.Errorf("recovery replay stopped at seq %d before the ack floor %d", last, floor)
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
	if m.StreamSeq > p.lastSeq+1 {
		if err := p.fillGap(ctx, m.StreamSeq); err != nil {
			return err
		}
	}
	if m.NumDelivered >= poisonAfter {
		return p.poison(ctx, m)
	}
	return p.apply(ctx, m, m.InProgress)
}

// poison reports a message that was delivered poisonAfter times without being acknowledged
// (every previous attempt crashed or aborted the engine) and stops the engine WITHOUT
// processing, acking or terminating it: it stays first in line until someone investigates.
func (p *processor) poison(ctx context.Context, m bus.Msg) error {
	ins := strings.TrimPrefix(m.Subject, "md.snap.")
	iss := model.QualityIssue{InsCode: ins, Code: quality.PoisonSuspect, At: m.Stored,
		Detail: fmt.Sprintf("stream seq %d delivered %d times without being processed; engine stopped, message kept for investigation", m.StreamSeq, m.NumDelivered)}
	err := fmt.Errorf("POISON_SUSPECT: MD seq %d (%s) delivered %d times without success; not processing it. "+
		"Inspect it (stream MD, that sequence), fix the cause, then restart; the engine will not skip it on its own", m.StreamSeq, m.Subject, m.NumDelivered)
	if perr := p.publish(ctx, []output{{bus.SubjQuality(ins), iss}}, fmt.Sprintf("eng:%d:%d:poison", p.epoch, m.StreamSeq), m.InProgress); perr != nil {
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
		if err := p.publish(ctx, []output{{bus.SubjQuality(iss.InsCode), iss}}, id, keepAlive); err != nil {
			return bus.Abort(err)
		}
		p.lastSeq = m.StreamSeq
		return bus.Permanent(derr)
	}
	// From here on Process() has run: never Nak. Retry the same outputs, else abort.
	outs := p.compute(s)
	if !p.partialBefore.IsZero() {
		outs = p.markPartial(s, outs)
	}
	if err := p.publish(ctx, outs, id, keepAlive); err != nil {
		return bus.Abort(err)
	}
	p.lastSeq = m.StreamSeq
	return nil
}

// markPartial flags outputs whose day or window started before partialBefore (their earlier
// snapshots were lost) and adds one RECOVERY_TRUNCATED issue per instrument for such a day.
func (p *processor) markPartial(s model.Snapshot, outs []output) []output {
	for _, o := range outs {
		switch v := o.v.(type) {
		case *model.GameTotals:
			if d, err := time.ParseInLocation("2006-01-02", v.Day, tehran.Loc); err == nil && d.Before(p.partialBefore) {
				v.Partial = true
			}
		case *model.TenMinute:
			v.Partial = v.WindowStart.Before(p.partialBefore)
		}
	}
	if !p.flagged[s.InsCode] && tehran.DayStart(s.SourceTime).Before(p.partialBefore) {
		p.flagged[s.InsCode] = true
		outs = append(outs, output{bus.SubjQuality(s.InsCode), model.QualityIssue{InsCode: s.InsCode, Code: quality.RecoveryTruncated,
			Detail: fmt.Sprintf("engine restarted after snapshots stored before %s were discarded from the bus; day totals and windows before then are partial",
				p.partialBefore.UTC().Format(time.RFC3339)), At: s.IngestTime}})
	}
	return outs
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

// leaser is what runLeased needs from *bus.JetStream.
type leaser interface {
	AcquireLease(ctx context.Context, bucket, key, holder string, ttl time.Duration) (*bus.Lease, error)
}

// runLeased runs the NATS engine only while it holds the single-engine lease: it refuses to
// start (bus.ErrLeaseHeld) when another engine holds it, and stops with bus.ErrLeaseLost when
// the lease is lost while running (it may have been taken over). Two engines on one durable
// would each see only part of the snapshots.
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
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-lease.Lost():
			cancel()
		case <-lctx.Done():
		}
	}()
	err = p.runNATS(lctx, js, spec, exitWhenIdle)
	select {
	case <-lease.Lost():
		return fmt.Errorf("stopping: %w (another engine may have taken over)", bus.ErrLeaseLost)
	default:
	}
	return err
}
