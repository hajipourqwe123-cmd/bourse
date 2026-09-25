package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"bourse/internal/bus"
	"bourse/internal/flow"
	"bourse/internal/model"
	"bourse/internal/tehran"
)

// fakeBus is an in-memory MD stream with controlled store times.
type fakeBus struct {
	msgs      map[uint64]fakeMsg
	floor     uint64
	stopAfter uint64 // Replay stops (without error) after this sequence; 0 = never
	start     time.Time
	replayed  []uint64
}

type fakeMsg struct {
	subj   string
	data   []byte
	stored time.Time
}

func (f *fakeBus) add(seq uint64, stored time.Time, s model.Snapshot) {
	b, _ := json.Marshal(s)
	f.addRaw(seq, stored, bus.SubjSnapshot(s.InsCode), string(b))
}

func (f *fakeBus) addRaw(seq uint64, stored time.Time, subj, data string) {
	if f.msgs == nil {
		f.msgs = map[uint64]fakeMsg{}
	}
	f.msgs[seq] = fakeMsg{subj, []byte(data), stored}
}

func (f *fakeBus) seqs() (first, last uint64) {
	for s := range f.msgs {
		if first == 0 || s < first {
			first = s
		}
		if s > last {
			last = s
		}
	}
	return
}

func (f *fakeBus) AckFloor(context.Context, string, string) (uint64, error) { return f.floor, nil }
func (f *fakeBus) GetMsg(_ context.Context, _ string, seq uint64) (string, []byte, time.Time, error) {
	m, ok := f.msgs[seq]
	if !ok {
		return "", nil, time.Time{}, jetstream.ErrMsgNotFound
	}
	return m.subj, m.data, m.stored, nil
}
func (f *fakeBus) StreamState(context.Context, string) (uint64, time.Time, error) {
	first, _ := f.seqs()
	return first, f.msgs[first].stored, nil
}
func (f *fakeBus) StreamCreated(context.Context, string) (time.Time, error) {
	return time.Unix(1, 0), nil
}
func (f *fakeBus) Consume(context.Context, bus.ConsumerSpec, func(bus.Msg) error) error {
	return errors.New("not used")
}
func (f *fakeBus) Replay(_ context.Context, _, _ string, start time.Time, upTo uint64, fn func(bus.Msg) error) (uint64, error) {
	f.start = start
	first, lastSeq := f.seqs()
	var last uint64
	for seq := first; seq <= lastSeq && seq <= upTo; seq++ {
		m, ok := f.msgs[seq]
		if !ok || (!start.IsZero() && m.stored.Before(start)) {
			continue
		}
		if err := fn(bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: seq, Stored: m.stored}); err != nil {
			return last, err
		}
		f.replayed = append(f.replayed, seq)
		last = seq
		if f.stopAfter != 0 && seq >= f.stopAfter {
			break
		}
	}
	return last, nil
}

// recPub records publishes; fail makes every publish fail.
type recPub struct {
	ids  []string
	subj []string
	fail bool
}

func (r *recPub) Publish(subject string, v any) error { return r.PublishID(subject, "", v) }
func (r *recPub) PublishID(subject, id string, v any) error {
	if r.fail {
		return errors.New("nats down")
	}
	r.ids, r.subj = append(r.ids, id), append(r.subj, subject)
	return nil
}

func newTestProcessor(f *fakeBus, pub bus.Publisher) *processor {
	p := newProcessor(flow.DefaultConfig(), pub)
	p.retry = []time.Duration{time.Millisecond}
	p.js, p.epoch = f, 1
	return p
}

func tehranTime(day, hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hm, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRecoveryStart(t *testing.T) {
	// Source 2026-09-23 10:00 Tehran, stored a second later: day start 2026-09-23 00:00 Tehran
	// = 2026-09-22 20:30 UTC, minus the 1h margin = 19:30 UTC.
	src := tehranTime("2026-09-23", "10:00")
	if got, want := recoveryStart(src, src.Add(time.Second)), time.Date(2026, 9, 22, 19, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("normal: %s, want %s", got.UTC(), want)
	}
	// Source clock 2.5h ahead across midnight: source 2026-09-24 01:00 Tehran (2026-09-23 21:30
	// UTC) stored 2026-09-23 19:00 UTC. Day start − 1h = 19:30 UTC is after the store time, so
	// replay must start at the floor's store time or it would skip the floor itself.
	stored := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC)
	if got := recoveryStart(tehranTime("2026-09-24", "01:00"), stored); !got.Equal(stored) {
		t.Errorf("source ahead: %s, want %s", got.UTC(), stored)
	}
}

// Replay window: previous-day messages stored before (day start − 1h) are excluded, those in
// the margin are included, and nothing after the ack floor is touched.
func TestRecoverStateReplaysTradingDayUpToFloor(t *testing.T) {
	d := day("2026-09-23", 1, 3, 5)
	prev := day("2026-09-22", 1, 2, 5)
	f := &fakeBus{floor: 4}
	f.add(1, tehranTime("2026-09-22", "12:00"), prev[0]) // outside the window
	f.add(2, tehranTime("2026-09-22", "23:30"), prev[1]) // inside the 1h margin
	f.add(3, d[0].SourceTime.Add(time.Second), d[0])     // today
	f.add(4, d[1].SourceTime.Add(time.Second), d[1])     // the ack floor
	f.add(5, d[2].SourceTime.Add(time.Second), d[2])     // after the floor: the durable's
	p := newTestProcessor(f, &recPub{})
	n, err := p.recoverState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.replayed) != "[2 3 4]" || n != 3 || p.lastSeq != 4 || p.truncated {
		t.Fatalf("replayed %v (n=%d) lastSeq=%d truncated=%v; want [2 3 4], 3, 4, false", f.replayed, n, p.lastSeq, p.truncated)
	}
	// State equals an engine that processed exactly today's snapshots 3 and 4.
	ref := flow.New(flow.DefaultConfig())
	ref.Process(d[0])
	ref.Process(d[1])
	if got, want := p.eng.Process(d[2]).Game, ref.Process(d[2]).Game; got == nil || *got != *want {
		t.Fatalf("recovered state differs: %+v vs %+v", got, want)
	}
}

func TestRecoverStateFailsWhenReplayStopsBeforeFloor(t *testing.T) {
	d := day("2026-09-23", 1, 3, 5)
	f := &fakeBus{floor: 3, stopAfter: 2}
	for i, s := range d {
		f.add(uint64(i+1), s.SourceTime, s)
	}
	_, err := newTestProcessor(f, &recPub{}).recoverState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "before the ack floor 3") {
		t.Fatalf("want replay-short error, got %v", err)
	}
}

func TestRecoverStateFloorMissingOrUndecodable(t *testing.T) {
	d := day("2026-09-23", 1, 4, 5)
	// Floor 2 was discarded; the stream now starts at 3: whole-stream replay, truncated.
	f := &fakeBus{floor: 2}
	f.add(3, d[2].SourceTime, d[2])
	f.add(4, d[3].SourceTime, d[3])
	p := newTestProcessor(f, &recPub{})
	if _, err := p.recoverState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.start.IsZero() || !p.truncated || p.lastSeq != 2 || len(f.replayed) != 0 {
		t.Fatalf("missing floor: start=%s truncated=%v lastSeq=%d replayed=%v", f.start, p.truncated, p.lastSeq, f.replayed)
	}
	// Floor is an undecodable message: window from its store day.
	f = &fakeBus{floor: 2}
	f.add(1, d[0].SourceTime, d[0])
	f.addRaw(2, d[1].SourceTime, "md.snap.SYNTEST0000", `{}`)
	p = newTestProcessor(f, &recPub{})
	if _, err := p.recoverState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := recoveryStart(d[1].SourceTime, d[1].SourceTime); !f.start.Equal(want) || fmt.Sprint(f.replayed) != "[1 2]" {
		t.Fatalf("undecodable floor: start=%s want %s replayed=%v", f.start, want, f.replayed)
	}
}

func TestTruncatedDayFlaggedOncePerInstrument(t *testing.T) {
	d := day("2026-09-23", 2, 3, 5) // A0 B0 A1 B1 A2 B2
	f := &fakeBus{floor: 3}
	for i := 2; i < len(d); i++ { // seqs 1-2 (today's start) were discarded
		f.add(uint64(i+1), d[i].SourceTime, d[i])
	}
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	if _, err := p.recoverState(context.Background()); err != nil || !p.truncated {
		t.Fatalf("err=%v truncated=%v", err, p.truncated)
	}
	for seq := uint64(4); seq <= 6; seq++ {
		m := f.msgs[seq]
		if err := p.handle(context.Background(), bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	count := map[string]int{}
	for _, s := range pub.subj {
		if strings.HasPrefix(s, "quality.") {
			count[s]++
		}
	}
	if count["quality.SYNTEST0000"] != 1 || count["quality.SYNTEST0001"] != 1 {
		t.Fatalf("RECOVERY_TRUNCATED issues per instrument: %v", count)
	}
}

func TestHandleSkipsAppliedAndFillsGaps(t *testing.T) {
	d := day("2026-09-23", 1, 5, 5)
	f := &fakeBus{}
	for i, s := range d {
		f.add(uint64(i+1), s.SourceTime, s)
	}
	f.addRaw(6, d[4].SourceTime, "md.snap.SYNTEST0000", `{}`)
	f.add(7, d[4].SourceTime.Add(5*time.Second), func() model.Snapshot { s := d[4]; s.SourceTime = s.SourceTime.Add(5 * time.Second); return s }())
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	msg := func(seq uint64) bus.Msg {
		m := f.msgs[seq]
		return bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: seq, Stored: m.stored}
	}
	ctx := context.Background()
	for _, seq := range []uint64{1, 2} {
		if err := p.handle(ctx, msg(seq)); err != nil {
			t.Fatal(err)
		}
	}
	before := len(pub.ids)
	if err := p.handle(ctx, msg(2)); err != nil || len(pub.ids) != before { // redelivery of an applied seq
		t.Fatalf("redelivered seq recomputed/published: err=%v published %d more", err, len(pub.ids)-before)
	}
	// Seq 7 arrives next: 3, 4, 5 (valid) and 6 (undecodable) are applied first, in order.
	if err := p.handle(ctx, msg(7)); err != nil {
		t.Fatal(err)
	}
	var seqOrder []string
	for _, id := range pub.ids[before:] {
		seq := strings.Split(id, ":")[2]
		if len(seqOrder) == 0 || seqOrder[len(seqOrder)-1] != seq {
			seqOrder = append(seqOrder, seq)
		}
	}
	if fmt.Sprint(seqOrder) != "[3 4 5 6 7]" || p.lastSeq != 7 {
		t.Fatalf("gap fill order %v lastSeq %d", seqOrder, p.lastSeq)
	}
	if i := indexOf(pub.ids, "eng:1:6:0"); i < 0 || pub.subj[i] != "quality.SYNTEST0000" {
		t.Fatalf("no UNDECODABLE issue for gap-filled seq 6: %v", pub.ids)
	}
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// Publishing the UNDECODABLE issue fails: abort, message not recorded as applied.
func TestUndecodableIssuePublishFailureAborts(t *testing.T) {
	f := &fakeBus{}
	f.addRaw(1, time.Unix(100, 0), "md.snap.SYNTEST0000", `null`)
	p := newTestProcessor(f, &recPub{fail: true})
	m := f.msgs[1]
	err := p.handle(context.Background(), bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: 1, Stored: m.stored})
	if err == nil || bus.IsPermanent(err) || !strings.Contains(err.Error(), "nats down") || p.lastSeq != 0 {
		t.Fatalf("err=%v permanent=%v lastSeq=%d", err, bus.IsPermanent(err), p.lastSeq)
	}
}
