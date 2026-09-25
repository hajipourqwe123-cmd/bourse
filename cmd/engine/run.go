package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

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
}

func newProcessor(cfg flow.Config, pub bus.Publisher) *processor {
	return &processor{eng: flow.New(cfg), radar: anomaly.New(anomaly.DefaultConfig()), pub: pub,
		retry: []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}}
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
func (p *processor) publish(outs []output, idPrefix string, beforeRetry func()) error {
	idp, useID := p.pub.(idPublisher)
	useID = useID && idPrefix != ""
	for i, o := range outs {
		err := bus.Retry(p.retry, beforeRetry, func() error {
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
		var s model.Snapshot
		if err := json.Unmarshal(env.Data, &s); err != nil {
			bad++
			continue
		}
		n++
		if err := p.publish(p.compute(s), "", nil); err != nil {
			return n, bad, fmt.Errorf("publish: %w", err)
		}
	}
	return n, bad, sc.Err()
}

const (
	engineDurable = "engine"
	snapFilter    = "md.snap.>"
)

// jsBus is what the NATS path needs from *bus.JetStream (narrowed for tests).
type jsBus interface {
	AckFloor(ctx context.Context, stream, durable string) (uint64, error)
	GetMsg(ctx context.Context, stream string, seq uint64) (string, []byte, time.Time, error)
	Replay(ctx context.Context, stream, filter string, start time.Time, upTo uint64, fn func(bus.Msg) error) error
	StreamCreated(ctx context.Context, stream string) (time.Time, error)
	Consume(ctx context.Context, spec bus.ConsumerSpec, fn func(bus.Msg) error) error
}

// recoverState rebuilds engine and radar state after a restart: it replays, WITHOUT publishing,
// md.snap.> from the start of the Tehran trading day of the last acknowledged snapshot up to the
// durable's ack floor. Everything after the floor is still owned by the durable consumer.
// The radar only re-warms on that day (its longer history is not restored).
func (p *processor) recoverState(ctx context.Context, js jsBus) (replayed int, err error) {
	floor, err := js.AckFloor(ctx, bus.StreamMD, engineDurable)
	if err != nil || floor == 0 {
		return 0, err
	}
	var start time.Time // zero = whole retained stream
	if _, data, _, err := js.GetMsg(ctx, bus.StreamMD, floor); err != nil {
		log.Printf("engine: recovery: message at ack floor %d unavailable (%v); replaying the whole stream", floor, err)
	} else if s, err := decodeSnapshot(data); err != nil {
		log.Printf("engine: recovery: message at ack floor %d undecodable; replaying the whole stream", floor)
	} else {
		// Stream store time is compared with a source-time day boundary: a 1h margin keeps
		// snapshots stored slightly before their source time. Earlier-day snapshots that slip in
		// are harmless: the engine resets each instrument on its first snapshot of a new day.
		start = tehran.DayStart(s.SourceTime).Add(-time.Hour)
	}
	err = js.Replay(ctx, bus.StreamMD, snapFilter, start, floor, func(m bus.Msg) error {
		s, err := decodeSnapshot(m.Data)
		if err != nil {
			return nil // was terminated (and reported) when first consumed
		}
		p.compute(s)
		replayed++
		return nil
	})
	return replayed, err
}

func decodeSnapshot(data []byte) (model.Snapshot, error) {
	var s model.Snapshot
	err := json.Unmarshal(data, &s)
	return s, err
}

// runNATS consumes md.snap.> through the durable "engine" consumer (BUS=nats). It returns on
// ctx cancellation (nil) or when outputs cannot be published after retries (non-nil: exit 1).
func (p *processor) runNATS(ctx context.Context, js jsBus, spec bus.ConsumerSpec) error {
	n, err := p.recoverState(ctx, js)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}
	log.Printf("engine: recovered state from %d snapshots", n)
	created, err := js.StreamCreated(ctx, bus.StreamMD)
	if err != nil {
		return err
	}
	epoch := created.UnixNano()
	err = js.Consume(ctx, spec, func(m bus.Msg) error {
		s, derr := decodeSnapshot(m.Data)
		if derr != nil {
			iss := model.QualityIssue{InsCode: strings.TrimPrefix(m.Subject, "md.snap."), Code: quality.Undecodable,
				Detail: fmt.Sprintf("stream seq %d: payload is not a snapshot: %v", m.StreamSeq, derr), At: m.Stored}
			if err := p.publish([]output{{bus.SubjQuality(iss.InsCode), iss}}, fmt.Sprintf("eng:%d:%d", epoch, m.StreamSeq), m.InProgress); err != nil {
				return bus.Abort(err)
			}
			return bus.Permanent(derr)
		}
		// From here on Process() has run: never Nak. Retry the same outputs, else abort.
		if err := p.publish(p.compute(s), fmt.Sprintf("eng:%d:%d", epoch, m.StreamSeq), m.InProgress); err != nil {
			return bus.Abort(err)
		}
		return nil
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}
