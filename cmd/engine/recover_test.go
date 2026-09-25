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
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// fakeBus is an in-memory MD stream with controlled store times.
type fakeBus struct {
	cp        *bus.Checkpoint
	state     map[string][]byte
	getErr    error // returned by GetMsg for every sequence when set
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
	if f.getErr != nil {
		return "", nil, time.Time{}, f.getErr
	}
	m, ok := f.msgs[seq]
	if !ok {
		return "", nil, time.Time{}, jetstream.ErrMsgNotFound
	}
	return m.subj, m.data, m.stored, nil
}
func (f *fakeBus) StreamState(context.Context, string) (bus.StreamInfo, error) {
	first, last := f.seqs()
	return bus.StreamInfo{FirstSeq: first, FirstStored: f.msgs[first].stored, LastSeq: last}, nil
}
func (f *fakeBus) StreamCreated(context.Context, string) (time.Time, error) {
	return time.Unix(1, 0), nil
}
func (f *fakeBus) LoadCheckpoint(context.Context, string) (bus.Checkpoint, bool, error) {
	if f.cp == nil {
		return bus.Checkpoint{}, false, nil
	}
	return *f.cp, true, nil
}
func (f *fakeBus) SaveCheckpoint(_ context.Context, _ string, cp bus.Checkpoint) error {
	f.cp = &cp
	return nil
}
func (f *fakeBus) StateGet(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := f.state[key]
	return v, ok, nil
}
func (f *fakeBus) StatePut(_ context.Context, key string, val []byte) error {
	if f.state == nil {
		f.state = map[string][]byte{}
	}
	f.state[key] = val
	return nil
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
	vals []any
	fail bool
}

func (r *recPub) Publish(subject string, v any) error { return r.PublishID(subject, "", v) }
func (r *recPub) PublishID(subject, id string, v any) error {
	if r.fail {
		return errors.New("nats down")
	}
	r.ids, r.subj, r.vals = append(r.ids, id), append(r.subj, subject), append(r.vals, v)
	return nil
}

func newTestProcessor(f *fakeBus, pub bus.Publisher) *processor {
	p := newProcessor(flow.DefaultConfig(), pub)
	p.retry = []time.Duration{time.Millisecond}
	p.js, p.epoch = f, 1
	p.allowSynthetic = true // test instruments are SYN*
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
	if fmt.Sprint(f.replayed) != "[2 3 4]" || n != 3 || p.lastSeq != 4 || p.loss != nil {
		t.Fatalf("replayed %v (n=%d) lastSeq=%d loss=%+v; want [2 3 4], 3, 4, none", f.replayed, n, p.lastSeq, p.loss)
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
	// Floor 2 was discarded, no checkpoint; the stream now starts at 3: whole-stream replay,
	// conservative loss (day unknown).
	f := &fakeBus{floor: 2}
	f.add(3, d[2].SourceTime, d[2])
	f.add(4, d[3].SourceTime, d[3])
	p := newTestProcessor(f, &recPub{})
	if _, err := p.recoverState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.start.IsZero() || p.loss == nil || !p.loss.replayBefore.Equal(d[2].SourceTime) || p.lastSeq != 2 || len(f.replayed) != 0 {
		t.Fatalf("missing floor: start=%s loss=%+v lastSeq=%d replayed=%v", f.start, p.loss, p.lastSeq, f.replayed)
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

// partialFlags runs seqs through handle and collects partial flags and RECOVERY_TRUNCATED issues.
func partialFlags(t *testing.T, p *processor, f *fakeBus, pub *recPub, from, to uint64) (games, windows map[string]bool, issues map[string]int) {
	t.Helper()
	for seq := from; seq <= to; seq++ {
		m := f.msgs[seq]
		if err := p.handle(context.Background(), bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: seq, Stored: m.stored}); err != nil {
			t.Fatal(err)
		}
	}
	games, windows, issues = map[string]bool{}, map[string]bool{}, map[string]int{}
	for _, v := range pub.vals {
		switch v := v.(type) {
		case model.QualityIssue:
			if v.Code == quality.RecoveryTruncated {
				issues[v.InsCode]++
			}
		case *model.GameTotals:
			games[v.InsCode+" "+v.Day] = v.Partial
		case *model.TenMinute:
			windows[v.InsCode+" "+v.WindowStart.In(tehran.Loc).Format("2006-01-02 15:04")] = v.Partial
		}
	}
	return
}

// Applied snapshots of today are gone from the replay: today's GameTotals and the window of each
// instrument's first retained snapshot are partial, one RECOVERY_TRUNCATED per instrument, and
// the next trading day is complete again.
func TestTruncatedDayMarkedPartial(t *testing.T) {
	d := day("2026-09-23", 2, 3, 5) // seqs 1..6 = A0 B0 A1 B1 A2 B2, all in the 09:00 window
	f := &fakeBus{floor: 3}
	for i := 2; i < len(d); i++ { // seqs 1-2 (applied, today) were discarded
		f.add(uint64(i+1), d[i].SourceTime, d[i])
	}
	next := day("2026-09-24", 1, 2, 6) // SYNTEST0000 on the next day: seqs 7, 8
	f.add(7, next[0].SourceTime, next[0])
	f.add(8, next[1].SourceTime, next[1])
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	if _, err := p.recoverState(context.Background()); err != nil || p.loss == nil || p.loss.replayDay != "2026-09-23" {
		t.Fatalf("err=%v loss=%+v", err, p.loss)
	}
	games, windows, issues := partialFlags(t, p, f, pub, 4, 8)
	if issues["SYNTEST0000"] != 1 || issues["SYNTEST0001"] != 1 {
		t.Errorf("RECOVERY_TRUNCATED per instrument: %v", issues)
	}
	wantGames := map[string]bool{"SYNTEST0000 2026-09-23": true, "SYNTEST0001 2026-09-23": true, "SYNTEST0000 2026-09-24": false}
	wantWindows := map[string]bool{"SYNTEST0000 2026-09-23 09:00": true, "SYNTEST0001 2026-09-23 09:00": true, "SYNTEST0000 2026-09-24 09:00": false}
	if fmt.Sprint(games) != fmt.Sprint(wantGames) || fmt.Sprint(windows) != fmt.Sprint(wantWindows) {
		t.Errorf("partial flags\n games   %v want %v\n windows %v want %v", games, wantGames, windows, wantWindows)
	}
}

// The cut lands just before a 10-minute boundary: the window holding the first retained
// snapshot (09:10) lacks the interval that ended at it, so it is partial; the next window is
// complete. The floor message is gone; its day comes from the checkpoint.
func TestPartialWindowAtTenMinuteBoundary(t *testing.T) {
	d := day("2026-09-23", 1, 250, 8) // index i = 09:00 + 5s*i; index 120 = 09:10:00, 240 = 09:20:00
	f := &fakeBus{floor: 120, cp: &bus.Checkpoint{Seq: 1, Day: "2026-09-23", Epoch: 1}}
	for i := 120; i < len(d); i++ { // seqs 1..120 (up to 09:09:55) were applied and are gone
		f.add(uint64(i+1), d[i].SourceTime, d[i])
	}
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	if _, err := p.recoverState(context.Background()); err != nil || p.loss == nil || p.loss.replayDay != "2026-09-23" {
		t.Fatalf("err=%v loss=%+v", err, p.loss)
	}
	games, windows, _ := partialFlags(t, p, f, pub, 121, uint64(len(d)))
	if !games["SYNTEST0000 2026-09-23"] || !windows["SYNTEST0000 2026-09-23 09:10"] || windows["SYNTEST0000 2026-09-23 09:20"] {
		t.Fatalf("games %v windows %v; want day partial, 09:10 partial, 09:20 complete", games, windows)
	}
}

// Wednesday's floor message aged out; the engine restarts on Saturday after the collector has
// published Saturday's first snapshots. Nothing unapplied was lost and Wednesday is over, so
// Saturday is complete (checkpoint). Without a checkpoint the day is unknown: conservative.
func TestAgedOutFloorOnNewDay(t *testing.T) {
	sat := day("2026-09-26", 1, 3, 9)
	build := func(cp *bus.Checkpoint) (*fakeBus, *recPub, *processor) {
		f := &fakeBus{floor: 500, cp: cp}
		for i, s := range sat { // seqs 501.. : FirstSeq == floor+1
			f.add(uint64(501+i), s.SourceTime.Add(time.Second), s)
		}
		pub := &recPub{}
		p := newTestProcessor(f, pub)
		if _, err := p.recoverState(context.Background()); err != nil {
			t.Fatal(err)
		}
		return f, pub, p
	}
	f, pub, p := build(&bus.Checkpoint{Seq: 420, Day: "2026-09-23", Epoch: 1})
	games, _, issues := partialFlags(t, p, f, pub, 501, 503)
	if games["SYNTEST0000 2026-09-26"] || len(issues) != 0 {
		t.Fatalf("with checkpoint: games %v issues %v; Saturday must be complete", games, issues)
	}
	f, pub, p = build(nil)
	games, _, _ = partialFlags(t, p, f, pub, 501, 503)
	if !games["SYNTEST0000 2026-09-26"] {
		t.Fatalf("without checkpoint the loss cannot be placed and must be conservative: %v", games)
	}
}

// Never-applied snapshots were discarded (first retained seq > floor+1): days that began before
// the first retained message are partial; an instrument with no trades before its baseline is not.
func TestUnprocessedLossMarksDayPartial(t *testing.T) {
	d := day("2026-09-23", 1, 6, 10)
	// DiscardOld removes from the front: applied seqs 1-2 and never-applied 3-4 are all gone.
	f := &fakeBus{floor: 2, cp: &bus.Checkpoint{Seq: 1, Day: "2026-09-23", Epoch: 1}}
	for i := 4; i < 6; i++ {
		f.add(uint64(i+1), d[i].SourceTime, d[i])
	}
	quiet := d[5]
	quiet.InsCode, quiet.Symbol = "SYNTEST0009", "SYN-Q"
	quiet.Volume, quiet.Value, quiet.IndBuyVol, quiet.IndSellVol, quiet.InstBuyVol, quiet.InstSellVol = 0, 0, 0, 0, 0, 0
	quiet.IndBuyCount, quiet.IndSellCount = 0, 0
	f.add(7, quiet.SourceTime, quiet)
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	if _, err := p.recoverState(context.Background()); err != nil || p.loss == nil || p.loss.unprocessedBefore.IsZero() {
		t.Fatalf("err=%v loss=%+v", err, p.loss)
	}
	games, _, issues := partialFlags(t, p, f, pub, 5, 7)
	if !games["SYNTEST0000 2026-09-23"] || issues["SYNTEST0000"] != 1 || issues["SYNTEST0009"] != 0 {
		t.Fatalf("games %v issues %v", games, issues)
	}
	if fs := p.first[dayKey("SYNTEST0009", "2026-09-23")]; fs.partial {
		t.Fatal("instrument with zero day volume before its baseline marked partial")
	}
}

// Rule 5: without ALLOW_SYNTHETIC_ON_BUS a SYN* snapshot is terminated, never computed.
func TestSyntheticRefusedByDefault(t *testing.T) {
	d := day("2026-09-23", 1, 2, 5)
	f := &fakeBus{}
	f.add(1, d[0].SourceTime, d[0])
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	p.allowSynthetic = false
	m := f.msgs[1]
	err := p.handle(context.Background(), bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: 1})
	if !bus.IsPermanent(err) || len(pub.ids) != 0 || p.lastSeq != 1 {
		t.Fatalf("err=%v publishes=%d lastSeq=%d", err, len(pub.ids), p.lastSeq)
	}
}

// A message delivered poisonAfter times (every earlier attempt crashed or aborted) is reported
// once as POISON_SUSPECT and stops the engine without processing, acking or dropping it.
func TestPoisonSuspectStopsWithoutProcessing(t *testing.T) {
	d := day("2026-09-23", 1, 2, 5)
	f := &fakeBus{}
	f.add(1, d[0].SourceTime, d[0])
	f.add(2, d[1].SourceTime, d[1])
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	ctx := context.Background()
	m1 := f.msgs[1]
	if err := p.handle(ctx, bus.Msg{Subject: m1.subj, Data: m1.data, StreamSeq: 1, NumDelivered: 4}); err != nil {
		t.Fatalf("4th delivery must still be processed: %v", err)
	}
	m := f.msgs[2]
	err := p.handle(ctx, bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: 2, NumDelivered: poisonAfter, Stored: m.stored})
	if err == nil || bus.IsPermanent(err) || !strings.Contains(err.Error(), "POISON_SUSPECT: MD seq 2") {
		t.Fatalf("want abort with POISON_SUSPECT, got %v", err)
	}
	if p.lastSeq != 1 {
		t.Fatalf("poisoned message recorded as applied: lastSeq=%d", p.lastSeq)
	}
	last := pub.vals[len(pub.vals)-1]
	iss, ok := last.(model.QualityIssue)
	if !ok || iss.Code != quality.PoisonSuspect || pub.ids[len(pub.ids)-1] != "eng:1:2:poison:0" || pub.subj[len(pub.subj)-1] != "quality.SYNTEST0000" {
		t.Fatalf("last publish %T %+v id %s", last, last, pub.ids[len(pub.ids)-1])
	}
	// An already-applied sequence is acked, not poisoned, whatever its delivery count.
	if err := p.handle(ctx, bus.Msg{Subject: m1.subj, Data: m1.data, StreamSeq: 1, NumDelivered: 9}); err != nil {
		t.Fatalf("applied seq redelivered 9 times: %v", err)
	}
	// State untouched: the next accepted snapshot still sees seq 1 as its baseline.
	ref := flow.New(flow.DefaultConfig())
	ref.Process(d[0])
	if got, want := p.eng.Process(d[1]).Game, ref.Process(d[1]).Game; got == nil || *got != *want {
		t.Fatalf("poisoned message changed engine state: %+v vs %+v", got, want)
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

// The poison check runs before the gap fill and names the whole range; the operator override
// processes the message once more.
func TestPoisonRangeAndOperatorRelease(t *testing.T) {
	d := day("2026-09-23", 1, 4, 5)
	f := &fakeBus{}
	for i, s := range d {
		f.add(uint64(i+1), s.SourceTime, s)
	}
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	ctx := context.Background()
	m := f.msgs[4]
	msg := bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: 4, NumDelivered: 7, Stored: m.stored}
	err := p.handle(ctx, msg)
	if err == nil || !strings.Contains(err.Error(), "POISON_SUSPECT: MD seqs 1..4") || p.lastSeq != 0 || len(pub.ids) != 1 {
		t.Fatalf("err=%v lastSeq=%d publishes=%d (want range 1..4 reported, nothing applied)", err, p.lastSeq, len(pub.ids))
	}
	p.retryPoisonSeq = 4
	if err := p.handle(ctx, msg); err != nil || p.lastSeq != 4 {
		t.Fatalf("released message not processed: err=%v lastSeq=%d", err, p.lastSeq)
	}
}

// AI-02 must not run on a partial window (its NetHot and PriceOpen are incomplete).
func TestDivergenceSkippedOnPartialWindow(t *testing.T) {
	at := tehranTime("2026-09-23", "10:00")
	mk := func(at time.Time, vol, indBuy, indSell, buyN, sellN int64) model.Snapshot {
		return model.Snapshot{InsCode: "SYNTEST0000", Symbol: "SYN-D", Source: "synthetic", SourceTime: at, IngestTime: at,
			PriceLast: 10_000, Volume: vol, Value: vol * 10_000, IndBuyVol: indBuy, InstBuyVol: vol - indBuy,
			IndSellVol: indSell, InstSellVol: vol - indSell, IndBuyCount: buyN, IndSellCount: sellN}
	}
	s1 := mk(at, 1_000_000, 600_000, 600_000, 50, 60)
	// +600,000 shares at a flat 10,000: one new buyer takes all (6,000,000,000 rial, hot), the
	// sell side goes to 100 new sellers (60,000,000 each, retail). NetHot 6e9 >= 5e9, price
	// change 0% <= 0.2%: an "absorption" divergence on a complete window.
	s2 := mk(at.Add(5*time.Second), 1_600_000, 1_200_000, 1_200_000, 51, 160)
	ai := func(p *processor) int {
		p.compute(s1)
		n := 0
		for _, o := range p.compute(s2) {
			if strings.HasPrefix(o.subject, "ai.signal.") {
				n++
			}
		}
		return n
	}
	if n := ai(newProcessor(flow.DefaultConfig(), &recPub{})); n != 1 {
		t.Fatalf("setup: complete window produced %d AI-02 events, want 1", n)
	}
	p := newProcessor(flow.DefaultConfig(), &recPub{})
	p.loss = &lossInfo{replayDay: "2026-09-23"}
	if n := ai(p); n != 0 {
		t.Fatalf("partial window produced %d AI events", n)
	}
}

// A checkpoint from an earlier (recreated) MD stream is ignored: the day is unknown again and
// the rule stays conservative.
func TestCheckpointFromOtherStreamIgnored(t *testing.T) {
	sat := day("2026-09-26", 1, 3, 9)
	f := &fakeBus{floor: 500, cp: &bus.Checkpoint{Seq: 420, Day: "2026-09-23", Epoch: 99}}
	for i, s := range sat {
		f.add(uint64(501+i), s.SourceTime.Add(time.Second), s)
	}
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	if _, err := p.recoverState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.loss == nil || p.loss.replayDay != "" || p.loss.replayBefore.IsZero() {
		t.Fatalf("foreign checkpoint used: loss=%+v", p.loss)
	}
}

// The checkpoint records the first sequence of each trading day with the stream epoch, and a
// snapshot dated far ahead of its ingest time does not move it.
func TestCheckpointSaving(t *testing.T) {
	d := day("2026-09-23", 1, 2, 5)
	next := day("2026-09-24", 1, 1, 5)
	future := next[0]
	future.SourceTime, future.IngestTime = tehranTime("2027-01-01", "10:00"), next[0].IngestTime
	future.InsCode, future.Symbol = "SYNTEST0007", "SYN-F"
	f := &fakeBus{}
	f.add(1, d[0].SourceTime, d[0])
	f.add(2, d[1].SourceTime, d[1])
	f.add(3, future.IngestTime, future)
	f.add(4, next[0].SourceTime, next[0])
	p := newTestProcessor(f, &recPub{})
	var got []string
	for seq := uint64(1); seq <= 4; seq++ {
		m := f.msgs[seq]
		if err := p.handle(context.Background(), bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: seq}); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d:%s", f.cp.Seq, f.cp.Day))
	}
	if fmt.Sprint(got) != "[1:2026-09-23 1:2026-09-23 1:2026-09-23 4:2026-09-24]" || f.cp.Epoch != 1 {
		t.Fatalf("checkpoints %v epoch %d", got, f.cp.Epoch)
	}
}

// Only "message not found" proves the floor message is gone; any other error (timeout) must
// stop recovery instead of taking the conservative loss path.
func TestRecoverStateGetMsgErrorIsNotLoss(t *testing.T) {
	d := day("2026-09-23", 1, 2, 5)
	f := &fakeBus{floor: 1, getErr: errors.New("nats: timeout")}
	f.add(1, d[0].SourceTime, d[0])
	p := newTestProcessor(f, &recPub{})
	if _, err := p.recoverState(context.Background()); err == nil || p.loss != nil {
		t.Fatalf("err=%v loss=%+v", err, p.loss)
	}
}

// A previous-day snapshot inside the replay's 1h margin is seen first; the damaged day must
// still be decided on its own baseline (decision per instrument AND day).
func TestPartialDecidedPerDay(t *testing.T) {
	prev := day("2026-09-22", 1, 1, 5)[0]
	prev.SourceTime = tehranTime("2026-09-22", "23:40")
	d := day("2026-09-23", 1, 3, 5)
	f := &fakeBus{floor: 3}
	// seq 1 (applied, today) was discarded: the floor day lost data.
	f.add(2, prev.SourceTime, prev) // late previous-day snapshot, inside the margin
	f.add(3, d[1].SourceTime, d[1]) // floor: today's first retained snapshot
	f.add(4, d[2].SourceTime, d[2])
	pub := &recPub{}
	p := newTestProcessor(f, pub)
	if _, err := p.recoverState(context.Background()); err != nil || p.loss == nil || p.loss.replayDay != "2026-09-23" {
		t.Fatalf("err=%v loss=%+v", err, p.loss)
	}
	games, _, issues := partialFlags(t, p, f, pub, 4, 4)
	if !games["SYNTEST0000 2026-09-23"] || issues["SYNTEST0000"] != 1 {
		t.Fatalf("games %v issues %v", games, issues)
	}
}

// ENGINE_RETRY_POISON_SEQ is one-shot: recorded before processing, so a second delivery (e.g.
// the released message crashed again) is reported as POISON_SUSPECT, and a restart with the
// same value ignores it.
func TestPoisonReleaseIsOneShot(t *testing.T) {
	d := day("2026-09-23", 1, 2, 5)
	f := &fakeBus{}
	f.add(1, d[0].SourceTime, d[0])
	f.add(2, d[1].SourceTime, d[1])
	p := newTestProcessor(f, &recPub{})
	p.retryPoisonSeq = 2
	ctx := context.Background()
	if err := p.handle(ctx, bus.Msg{Subject: f.msgs[1].subj, Data: f.msgs[1].data, StreamSeq: 1}); err != nil {
		t.Fatal(err)
	}
	m := f.msgs[2]
	p.lastSeq = 1
	p.guard = func() error { return errors.New("crash stand-in") } // processing fails after the release
	if err := p.handle(ctx, bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: 2, NumDelivered: 6}); err == nil || strings.Contains(err.Error(), "POISON") {
		t.Fatalf("released message not attempted: %v", err)
	}
	if string(f.state[poisonReleaseKey]) != "2" || p.retryPoisonSeq != 0 {
		t.Fatalf("release not recorded: %q, retryPoisonSeq=%d", f.state[poisonReleaseKey], p.retryPoisonSeq)
	}
	p.guard = nil
	if err := p.handle(ctx, bus.Msg{Subject: m.subj, Data: m.data, StreamSeq: 2, NumDelivered: 7}); err == nil || !strings.Contains(err.Error(), "POISON_SUSPECT") {
		t.Fatalf("second delivery after a used release: %v", err)
	}
	q := newTestProcessor(f, &recPub{}) // a restart with the same ENGINE_RETRY_POISON_SEQ
	q.retryPoisonSeq = 2
	_ = q.runNATS(ctx, f, engineConsumer(), false)
	if q.retryPoisonSeq != 0 {
		t.Fatal("used release honoured again after a restart")
	}
}
