package flow

import (
	"testing"
	"time"

	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

var t0 = time.Date(2026, 9, 23, 9, 5, 0, 0, tehran.Loc)

// snap builds a consistent snapshot: every share bought by persons+institutions is sold by persons+institutions.
func snap(at time.Time, price, vol, indBuy, indSell, buyN, sellN int64) model.Snapshot {
	return model.Snapshot{
		InsCode: "IRO1TEST0001", Symbol: "تست", Source: "test",
		SourceTime: at, IngestTime: at.Add(time.Second),
		PriceLast: price, Volume: vol, Value: vol * price,
		IndBuyVol: indBuy, InstBuyVol: vol - indBuy,
		IndSellVol: indSell, InstSellVol: vol - indSell,
		IndBuyCount: buyN, IndSellCount: sellN,
	}
}

func hasIssue(r Result, code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestFirstSnapshotIsBaselineOnly(t *testing.T) {
	e := New(DefaultConfig())
	r := e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	if len(r.Events) != 0 || r.Game != nil || r.Window != nil {
		t.Fatalf("first snapshot must not produce metrics: %+v", r)
	}
}

func TestHotBuyAttributed(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	// +300k shares at 10,000 rial = 3e9 rial bought by 1 new person -> avg 3e9 >= 2e9 -> hot.
	// Sell side: 300k shares sold by 100 new persons -> avg 3e7 -> retail, no event.
	r := e.Process(snap(t0.Add(5*time.Second), 10_000, 1_300_000, 900_000, 900_000, 51, 160))
	if len(r.Events) != 1 {
		t.Fatalf("want 1 event, got %d: %+v", len(r.Events), r.Events)
	}
	ev := r.Events[0]
	if ev.Side != model.Buy || ev.Band != model.BandHot || ev.Attribution != model.Attributed ||
		ev.Participants != 1 || ev.AvgTicket != 3_000_000_000 || ev.Value != 3_000_000_000 {
		t.Fatalf("unexpected event %+v", ev)
	}
	if r.Game.NetHot != 3_000_000_000 || r.Game.NetRetail != -3_000_000_000 {
		t.Fatalf("game totals wrong: %+v", *r.Game)
	}
	if r.Window == nil || r.Window.NetHot != 3_000_000_000 {
		t.Fatalf("window wrong: %+v", r.Window)
	}
}

func TestHotPlusBand(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	// 150k shares x 10,000 = 1.5e9 by 1 new seller -> hot_plus sell.
	r := e.Process(snap(t0.Add(5*time.Second), 10_000, 1_150_000, 750_000, 750_000, 80, 61))
	var got *model.FlowEvent
	for i := range r.Events {
		if r.Events[i].Side == model.Sell {
			got = &r.Events[i]
		}
	}
	if got == nil || got.Band != model.BandHotPlus || got.AvgTicket != 1_500_000_000 {
		t.Fatalf("want hot_plus sell, got %+v", r.Events)
	}
	if r.Game.NetHotPlus != -1_500_000_000 {
		t.Fatalf("game hot_plus wrong: %+v", *r.Game)
	}
}

func TestUnattributedNeverBanded(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	// Buyers count unchanged (existing buyers traded again) -> unattributed.
	r := e.Process(snap(t0.Add(5*time.Second), 10_000, 1_300_000, 900_000, 900_000, 50, 160))
	if r.Game.NetHot != 0 || r.Game.NetUnattributed != 3_000_000_000 {
		t.Fatalf("unattributed flow leaked into a band: %+v", *r.Game)
	}
	if len(r.Events) != 1 || r.Events[0].Attribution != model.Unattributed || r.Events[0].AvgTicket != 0 {
		t.Fatalf("want one unattributed event without avg ticket, got %+v", r.Events)
	}
	if r.Window.NetHot != 0 {
		t.Fatalf("unattributed flow must not enter NetHot window: %+v", r.Window)
	}
}

func TestIncompleteSnapshotProducesNoMetricAndIsNotBaseline(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	bad := snap(t0.Add(5*time.Second), 10_000, 1_300_000, 900_000, 900_000, 51, 160)
	bad.Missing = []string{model.FIndBuyCount}
	r := e.Process(bad)
	if len(r.Events) != 0 || r.Game != nil || !hasIssue(r, quality.Incomplete) {
		t.Fatalf("incomplete snapshot must yield only an INCOMPLETE issue: %+v", r)
	}
	// Next good snapshot is diffed against the last GOOD baseline (t0), not the incomplete one.
	r = e.Process(snap(t0.Add(10*time.Second), 10_000, 1_300_000, 900_000, 900_000, 51, 160))
	if len(r.Events) != 1 || r.Events[0].IntervalFrom != t0 {
		t.Fatalf("expected event over [t0, t0+10s], got %+v", r.Events)
	}
}

func TestCumulativeDecreaseRebaselines(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	r := e.Process(snap(t0.Add(5*time.Second), 10_000, 900_000, 500_000, 500_000, 50, 60))
	if !hasIssue(r, quality.CumulativeDecrease) || len(r.Events) != 0 {
		t.Fatalf("want CUMULATIVE_DECREASE and no events: %+v", r)
	}
	r = e.Process(snap(t0.Add(10*time.Second), 10_000, 1_200_000, 800_000, 600_000, 51, 61))
	if len(r.Events) == 0 || r.Events[0].Side != model.Buy || r.Events[0].Volume != 300_000 ||
		!r.Events[0].IntervalFrom.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("expected delta vs the re-baselined snapshot: %+v", r.Events)
	}
}

func TestSideMismatchDropsInterval(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	s := snap(t0.Add(5*time.Second), 10_000, 1_300_000, 900_000, 900_000, 51, 160)
	s.InstBuyVol += 10 // buy side no longer sums to volume
	r := e.Process(s)
	if !hasIssue(r, quality.SideMismatch) || len(r.Events) != 0 {
		t.Fatalf("want SIDE_MISMATCH and no events: %+v", r)
	}
}

func TestOutOfOrderIgnored(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	r := e.Process(snap(t0, 10_000, 1_300_000, 900_000, 900_000, 51, 160))
	if !hasIssue(r, quality.OutOfOrder) || len(r.Events) != 0 {
		t.Fatalf("duplicate timestamp must be ignored: %+v", r)
	}
}

// A late snapshot from the previous day must not wipe today's totals (it used to reset state).
func TestEarlierDaySnapshotIgnored(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	// +300,000 shares; one new real buyer takes all of them: 300,000 × 10,000 = 3,000,000,000 rial (hot).
	r1 := e.Process(snap(t0.Add(5*time.Second), 10_000, 1_300_000, 900_000, 700_000, 51, 60))
	if r1.Game == nil || r1.Game.NetHot != 3_000_000_000 {
		t.Fatalf("setup: game %+v", r1.Game)
	}
	late := e.Process(snap(t0.Add(-24*time.Hour), 9_000, 5_000_000, 2_000_000, 2_000_000, 400, 400))
	if !hasIssue(late, quality.OutOfOrder) || late.Game != nil || len(late.Events) != 0 {
		t.Fatalf("earlier-day snapshot must be OUT_OF_ORDER with no metric: %+v", late)
	}
	// Today continues from the last accepted snapshot: +100,000 shares; buy side has no new buyer
	// (unattributed), sell side one new seller at 100,000 × 10,000 = 1,000,000,000 rial (hot-plus),
	// so today's NetHot stays 3,000,000,000.
	r2 := e.Process(snap(t0.Add(10*time.Second), 10_000, 1_400_000, 1_000_000, 800_000, 51, 61))
	if r2.Game == nil || r2.Game.Day != "2026-09-23" || r2.Game.NetHot != 3_000_000_000 || r2.Game.NetHotPlus != -1_000_000_000 {
		t.Fatalf("today's totals were reset by the late snapshot: %+v", r2.Game)
	}
}

func TestNewTradingDayResets(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	next := t0.Add(24 * time.Hour)
	r := e.Process(snap(next, 10_000, 200_000, 100_000, 100_000, 10, 10))
	if len(r.Issues) != 0 && hasIssue(r, quality.CumulativeDecrease) {
		t.Fatalf("new day must not be treated as a decrease: %+v", r.Issues)
	}
	if r.Game != nil || len(r.Events) != 0 {
		t.Fatalf("first snapshot of a new day is baseline only: %+v", r)
	}
}

func TestTenMinuteWindowRollsOver(t *testing.T) {
	e := New(DefaultConfig())
	e.Process(snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60))
	r := e.Process(snap(t0.Add(5*time.Second), 10_100, 1_100_000, 700_000, 700_000, 60, 70))
	w1 := r.Window.WindowStart
	r = e.Process(snap(t0.Add(6*time.Minute), 10_300, 1_200_000, 800_000, 800_000, 70, 80)) // 09:11
	if r.Window.WindowStart.Equal(w1) {
		t.Fatalf("window should roll from %s", w1)
	}
	if r.Window.PriceOpen != 10_100 || r.Window.PriceLastV != 10_300 {
		t.Fatalf("window prices wrong: %+v", r.Window)
	}
	if got := r.Window.ChangePct(); got < 1.98 || got > 1.99 {
		t.Fatalf("change pct %.4f", got)
	}
}

func TestStaleFlagged(t *testing.T) {
	s := snap(t0, 10_000, 1_000_000, 600_000, 600_000, 50, 60)
	s.IngestTime = t0.Add(2 * time.Minute)
	r := New(DefaultConfig()).Process(s)
	if !hasIssue(r, quality.Stale) {
		t.Fatalf("want STALE, got %+v", r.Issues)
	}
}
