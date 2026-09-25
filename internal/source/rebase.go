package source

import (
	"context"
	"fmt"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

// Rebase replays a recording as if it were happening now (local development only: the dashboard,
// session strip and latency need current timestamps). Every snapshot's source and ingest time is
// shifted by one fixed duration, fixed on the first batch:
//
//   - RebaseToday: by whole days, onto Target's Tehran date (time of day kept);
//   - RebaseNow: so that the recording's time of day At maps to the current instant (time of day
//     not kept; any hour works, e.g. for the Gate 2 measurement).
//
// A batch whose shifted times are already past is returned at once (fast-forward); a later one is
// held until the wall clock reaches its latest shifted ingest time (real-time pacing).
//
// Re-timed data is not real market data: its source becomes "rebase:<source>", which
// model.IsSynthetic reports, so the bus guards refuse it unless ALLOW_SYNTHETIC_ON_BUS=1 and the
// dashboard labels it «داده نمایشی – غیرواقعی» (rules 2 and 5).
type Rebase struct {
	src    Source
	mode   RebaseMode
	target time.Time     // RebaseToday: any instant on the target day
	at     time.Duration // RebaseNow: recording time of day mapped to now
	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration) error

	shift time.Duration
	init  bool
}

// RebaseMode selects how the shift is chosen.
type RebaseMode string

const (
	RebaseToday RebaseMode = "today"
	RebaseNow   RebaseMode = "now"
)

// NewRebase wraps src. target is used by RebaseToday, at by RebaseNow.
func NewRebase(src Source, mode RebaseMode, target time.Time, at time.Duration) (*Rebase, error) {
	if mode != RebaseToday && mode != RebaseNow {
		return nil, fmt.Errorf("rebase: unknown mode %q (want today or now)", mode)
	}
	return &Rebase{src: src, mode: mode, target: target, at: at, now: time.Now, sleep: sleepCtx}, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SetClock replaces the wall clock used for the shift and the pacing (DEMO_CLOCK: a virtual clock
// whose sleep takes a demo duration). Call it before the first Fetch.
func (r *Rebase) SetClock(now func() time.Time, sleep func(ctx context.Context, d time.Duration) error) {
	r.now, r.sleep = now, sleep
}

func (r *Rebase) Name() string { return fmt.Sprintf("rebase(%s):%s", r.mode, r.src.Name()) }

// Shift is the applied shift (zero before the first batch).
func (r *Rebase) Shift() time.Duration { return r.shift }

func (r *Rebase) Fetch(ctx context.Context) ([]model.Snapshot, error) {
	batch, err := r.src.Fetch(ctx)
	if err != nil || len(batch) == 0 {
		return batch, err
	}
	if !r.init {
		// The recording's day and time are those it was ingested on (a first snapshot may
		// still carry the previous day's source time).
		first := batch[0].IngestTime
		switch r.mode {
		case RebaseToday:
			r.shift = tehran.DayStart(r.target).Sub(tehran.DayStart(first))
		case RebaseNow:
			r.shift = r.now().Sub(tehran.DayStart(first).Add(r.at))
		}
		r.init = true
	}
	var due time.Time
	for i := range batch {
		batch[i].Source = model.RebasedPrefix + batch[i].Source // not real market data (model.IsSynthetic)
		batch[i].SourceTime = batch[i].SourceTime.Add(r.shift)
		batch[i].IngestTime = batch[i].IngestTime.Add(r.shift)
		if batch[i].IngestTime.After(due) {
			due = batch[i].IngestTime
		}
	}
	if wait := due.Sub(r.now()); wait > 0 {
		if err := r.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	return batch, nil
}
