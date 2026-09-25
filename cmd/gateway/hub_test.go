package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"bourse/internal/anomaly"
	"bourse/internal/bus"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// recPub records publications; fail makes the next Publish fail.
type recPub struct {
	mu   sync.Mutex
	pubs []publication
	fail bool
}

func (r *recPub) Publish(_ context.Context, p []publication) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		r.fail = false
		return errors.New("centrifugo down")
	}
	r.pubs = append(r.pubs, p...)
	return nil
}

func (r *recPub) on(ch string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []any
	for _, p := range r.pubs {
		if p.Channel == ch {
			out = append(out, p.Data)
		}
	}
	return out
}

func hubAt(t *testing.T, now time.Time, allowSyn bool) (*hub, *recPub, *time.Time) {
	t.Helper()
	cfg := testConfig("", "")
	cfg.AllowSynthetic = allowSyn
	p := &recPub{}
	clock := now
	h := newHub(cfg, p, func() error { return nil }, func() time.Time { return clock })
	return h, p, &clock
}

func msg(subject string, v any) bus.Msg {
	b, _ := json.Marshal(v)
	return bus.Msg{Subject: subject, Data: b}
}

func tehranAt(hm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-23 "+hm, tehran.Loc)
	return t
}

func TestFlushWaitsForRebuild(t *testing.T) {
	now := tehranAt("10:00")
	h, p, _ := hubAt(t, now, false)
	h.onSnapshot(msg("md.snap.S1", snapAt("S1", now, 1010, 5000)))
	if err := h.flush(context.Background()); err != nil || len(p.pubs) != 0 {
		t.Fatalf("published %d before the rebuild completed", len(p.pubs))
	}
	for i := 0; i < 4; i++ {
		h.caughtUp()
	}
	_ = h.flush(context.Background())
	if len(p.on(chSymbols)) != 1 || len(p.on(chSummary)) != 1 {
		t.Fatalf("after the rebuild: symbols %d summary %d", len(p.on(chSymbols)), len(p.on(chSummary)))
	}
	if m := p.on(chSymbols)[0].(symbolsMsg); m.Day != "2026-09-23" || m.Seq != 1 {
		t.Errorf("symbols msg day %q seq %d", m.Day, m.Seq)
	}
	// Unchanged summary is not re-sent.
	_ = h.flush(context.Background())
	if len(p.on(chSummary)) != 1 {
		t.Errorf("unchanged summary re-sent")
	}
}

func ready(h *hub) {
	for i := 0; i < 4; i++ {
		h.caughtUp()
	}
}

func TestSyntheticFilteredOnEveryStream(t *testing.T) {
	now := tehranAt("10:00")
	h, p, _ := hubAt(t, now, false)
	ready(h)
	// A synthetic snapshot of a non-SYN code (synthetic source): its totals, issues and signals
	// must be dropped too, not only the snapshot.
	s := snapAt("IRX1", now, 1, 1)
	s.Source = "synthetic"
	h.onSnapshot(msg("md.snap.IRX1", s))
	h.onFlow(msg("flow.game.IRX1", model.GameTotals{InsCode: "IRX1", Day: "2026-09-23", AsOf: now, NetHot: 5}))
	h.onIssue(msg("quality.IRX1", model.QualityIssue{InsCode: "IRX1", Code: quality.Stale, At: now}))
	h.onSignal(msg("ai.signal.IRX1", anomaly.Event{InsCode: "IRX1", Kind: "anomaly", At: now, Reason: "r"}))
	h.onFlow(msg("flow.event.SYN1", model.FlowEvent{InsCode: "SYN1", Band: model.BandHot, IntervalTo: now}))
	h.onSignal(msg("ai.signal.SYN1", anomaly.Event{InsCode: "SYN1", Kind: "anomaly", At: now, Reason: "r"}))
	h.onIssue(msg("quality.SYN1", model.QualityIssue{InsCode: "SYN1", Code: quality.Stale, At: now}))
	_ = h.flush(context.Background())
	st, _ := h.state(now)
	if len(st.Rows) != 0 || st.Summary.Issues != 0 || len(st.Radar) != 0 || len(p.on(chRadar)) != 0 || len(p.on(chHot)) != 0 {
		t.Errorf("synthetic data leaked: rows %d issues %d radar %d hot %d", len(st.Rows), st.Summary.Issues, len(st.Radar), len(p.on(chHot)))
	}
	// Allowed (local demo): shown, and labelled.
	h2, _, _ := hubAt(t, now, true)
	ready(h2)
	h2.onSnapshot(msg("md.snap.IRX1", s))
	if st, _ := h2.state(now); len(st.Rows) != 1 || !st.Summary.Syn || !st.Rows[0].Syn {
		t.Errorf("allowed synthetic data must be shown labelled: %+v", st.Summary)
	}
}

func TestHotChannelFilters(t *testing.T) {
	now := tehranAt("10:00")
	h, p, _ := hubAt(t, now, false)
	hot := model.FlowEvent{InsCode: "S1", Band: model.BandHot, IntervalTo: now}
	h.onFlow(msg("flow.event.S1", hot)) // during the rebuild: not re-published
	ready(h)
	plus := hot
	plus.Band = model.BandHotPlus
	old := hot
	old.IntervalTo = now.AddDate(0, 0, -1)
	h.onFlow(msg("flow.event.S1", plus))
	h.onFlow(msg("flow.event.S1", old))
	h.onFlow(msg("flow.event.S1", hot))
	_ = h.flush(context.Background())
	if got := p.on(chHot); len(got) != 1 {
		t.Errorf("flow:hot published %d, want only the live, hot, today event", len(got))
	}
}

func TestFailedPublishResendsRowsAndSummary(t *testing.T) {
	now := tehranAt("10:00")
	h, p, _ := hubAt(t, now, false)
	ready(h)
	h.onSnapshot(msg("md.snap.S1", snapAt("S1", now, 1010, 5000)))
	p.fail = true
	_ = h.flush(context.Background())
	if len(p.pubs) != 0 {
		t.Fatal("setup: publish should have failed")
	}
	_ = h.flush(context.Background())
	sym := p.on(chSymbols)
	if len(sym) != 1 || len(p.on(chSummary)) != 1 {
		t.Fatalf("after a failed publish: symbols %d summary %d, want both re-sent", len(sym), len(p.on(chSummary)))
	}
	// New sequence (2): a client that missed 1 sees the gap and re-reads the state.
	if m := sym[0].(symbolsMsg); m.Seq != 2 || len(m.Rows) != 1 {
		t.Errorf("re-sent delta = seq %d rows %d", m.Seq, len(m.Rows))
	}
}

func TestDayAdvancesFromClock(t *testing.T) {
	now := tehranAt("17:59")
	h, p, clock := hubAt(t, now, false)
	ready(h)
	h.onSnapshot(msg("md.snap.S1", snapAt("S1", now, 1010, 5000)))
	_ = h.flush(context.Background())
	// A next-day message before midnight changes nothing.
	h.onSnapshot(msg("md.snap.S2", snapAt("S2", now.Add(15*time.Hour), 1, 1)))
	if st, _ := h.state(now); st.Day != "2026-09-23" || len(st.Rows) != 1 {
		t.Fatalf("day %s rows %d", st.Day, len(st.Rows))
	}
	*clock = now.Add(7 * time.Hour) // 00:59 next day
	_ = h.flush(context.Background())
	sums := p.on(chSummary)
	last := sums[len(sums)-1].(summaryMsg)
	if last.Summary.Day != "2026-09-24" || last.Summary.Instruments != 0 {
		t.Errorf("new-day summary = day %s instruments %d", last.Summary.Day, last.Summary.Instruments)
	}
}
