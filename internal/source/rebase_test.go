package source

import (
	"context"
	"testing"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

type sliceSource struct{ batches [][]model.Snapshot }

func (s *sliceSource) Name() string { return "slice" }
func (s *sliceSource) Fetch(context.Context) ([]model.Snapshot, error) {
	if len(s.batches) == 0 {
		return nil, ErrDone
	}
	b := s.batches[0]
	s.batches = s.batches[1:]
	return b, nil
}

func tt(date, hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", date+" "+hm, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func rec(hm string, ingestLag time.Duration) model.Snapshot {
	t := tt("2026-09-23", hm)
	return model.Snapshot{InsCode: "A", SourceTime: t, IngestTime: t.Add(ingestLag)}
}

func TestRebaseTodayKeepsTimeOfDayAndPaces(t *testing.T) {
	src := &sliceSource{batches: [][]model.Snapshot{
		{rec("09:00:00", 500*time.Millisecond)},
		{rec("10:00:00", 300*time.Millisecond), rec("10:00:00", 900*time.Millisecond)},
	}}
	now := tt("2026-09-26", "09:30:00") // Saturday, between the two batches
	r, err := NewRebase(src, RebaseToday, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	r.now = func() time.Time { return now }
	r.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }

	b1, err := r.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !b1[0].SourceTime.Equal(tt("2026-09-26", "09:00:00")) || len(slept) != 0 {
		t.Fatalf("past batch: source %s, slept %v; want 2026-09-26 09:00 at once", b1[0].SourceTime, slept)
	}
	if b1[0].IngestTime.Sub(b1[0].SourceTime) != 500*time.Millisecond {
		t.Errorf("ingest lag not kept")
	}
	b2, _ := r.Fetch(context.Background())
	if !b2[1].SourceTime.Equal(tt("2026-09-26", "10:00:00")) {
		t.Errorf("source = %s", b2[1].SourceTime)
	}
	// Held until the batch's latest ingest time: 10:00:00.9 − 09:30.
	if len(slept) != 1 || slept[0] != 30*time.Minute+900*time.Millisecond {
		t.Errorf("slept %v, want [30m0.9s]", slept)
	}
	if _, err := r.Fetch(context.Background()); err != ErrDone {
		t.Errorf("end = %v", err)
	}
}

func TestRebaseNowMapsAtToNow(t *testing.T) {
	src := &sliceSource{batches: [][]model.Snapshot{{rec("09:59:00", 0)}, {rec("10:00:05", 0)}}}
	now := tt("2026-09-25", "21:00:00")
	r, _ := NewRebase(src, RebaseNow, time.Time{}, 10*time.Hour)
	var slept []time.Duration
	r.now = func() time.Time { return now }
	r.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	b1, _ := r.Fetch(context.Background())
	if !b1[0].SourceTime.Equal(now.Add(-time.Minute)) || len(slept) != 0 {
		t.Errorf("first = %s slept %v", b1[0].SourceTime, slept)
	}
	b2, _ := r.Fetch(context.Background())
	if !b2[0].SourceTime.Equal(now.Add(5*time.Second)) || len(slept) != 1 || slept[0] != 5*time.Second {
		t.Errorf("second = %s slept %v", b2[0].SourceTime, slept)
	}
	if r.Shift() != now.Sub(tt("2026-09-23", "10:00:00")) {
		t.Errorf("shift = %s", r.Shift())
	}
}

func TestRebaseUnknownMode(t *testing.T) {
	if _, err := NewRebase(&sliceSource{}, "tomorrow", time.Time{}, 0); err == nil {
		t.Error("unknown mode accepted")
	}
}

func TestRebaseMarksDataNotRealAndShiftsByIngestDay(t *testing.T) {
	// First snapshot carries the previous day's source time (vendor pre-open), ingested on 09-23.
	first := rec("08:40:00", 0)
	first.SourceTime = tt("2026-09-22", "12:29:59")
	first.Source = "sourcearena"
	src := &sliceSource{batches: [][]model.Snapshot{{first}}}
	now := tt("2026-09-24", "08:00:00")
	r, _ := NewRebase(src, RebaseToday, now, 0)
	r.now = func() time.Time { return now }
	var slept time.Duration
	r.sleep = func(_ context.Context, d time.Duration) error { slept = d; return nil }
	b, _ := r.Fetch(context.Background())
	if r.Shift() != 24*time.Hour {
		t.Errorf("shift = %s, want one day (ingest day 09-23 → 09-24)", r.Shift())
	}
	if b[0].Source != "rebase:sourcearena" || !model.IsSynthetic(&b[0]) {
		t.Errorf("source = %q: re-timed data must be marked not real", b[0].Source)
	}
	if slept != 40*time.Minute {
		t.Errorf("slept %s, want 40m (08:40 on the target day)", slept)
	}
}
