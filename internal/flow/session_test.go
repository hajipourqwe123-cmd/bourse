package flow

import (
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// 2026-09-26 is a Saturday (a trading day); 2026-10-01 a Thursday (closed).
func tt(day, hms string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", day+" "+hms, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

// sessEngine maps IRSTOCK to stock (09:00–12:30) and IRGOLD to gold (12:00–18:00).
func sessEngine() *Engine {
	cfg := DefaultConfig()
	cfg.Sessions = calendar.Default().WithInstruments(map[string]string{"IRSTOCK": "stock", "IRGOLD": "gold"})
	return New(cfg)
}

func sn(ins string, at time.Time, price, vol, indBuy, indSell, buyN, sellN int64) model.Snapshot {
	s := snap(at, price, vol, indBuy, indSell, buyN, sellN)
	s.InsCode = ins
	return s
}

// Two snapshots: the day baseline, then +300,000 shares all bought by one new real buyer at
// 10,000 rial (3,000,000,000 rial, hot). Returns the second result.
func dayOf(t *testing.T, e *Engine, ins string, base, next time.Time, baseVol int64) (Result, Result) {
	t.Helper()
	r0 := e.Process(sn(ins, base, 10_000, baseVol, baseVol*6/10, baseVol*6/10, 50, 60))
	r1 := e.Process(sn(ins, next, 10_000, baseVol+300_000, baseVol*6/10+300_000, baseVol*6/10+200_000, 51, 60))
	if r1.Game == nil || r1.Game.NetHot != 3_000_000_000 {
		t.Fatalf("setup: game %+v", r1.Game)
	}
	return r0, r1
}

// Owner rule (DL-01): a day is complete only if its first accepted baseline was taken before the
// instrument's OWN open with zero day volume.
func TestLateStartRule(t *testing.T) {
	for _, c := range []struct {
		name       string
		ins        string
		base, next time.Time
		baseVol    int64
		partial    bool // day totals + DAY_START_MISSED
		window     bool // the window of the second snapshot (partial only if it holds the baseline)
	}{
		{"late start: first baseline 09:05 with volume", "IRSTOCK", tt("2026-09-26", "09:05:00"), tt("2026-09-26", "09:05:05"), 1_000_000, true, true},
		{"pre-open zero baseline 08:59:55", "IRSTOCK", tt("2026-09-26", "08:59:55"), tt("2026-09-26", "09:00:05"), 0, false, false},
		// The missing activity lies before the 08:50 window holding the baseline: 09:00 is complete.
		{"zero-volume baseline after the open", "IRSTOCK", tt("2026-09-26", "09:10:00"), tt("2026-09-26", "09:10:05"), 0, true, true},
		{"gold fund: 11:59:55 zero baseline (its own open is 12:00)", "IRGOLD", tt("2026-09-26", "11:59:55"), tt("2026-09-26", "12:00:05"), 0, false, false},
		{"gold fund: first baseline 12:05 with volume", "IRGOLD", tt("2026-09-26", "12:05:00"), tt("2026-09-26", "12:05:05"), 1_000_000, true, true},
		{"stock hours do not apply to a gold fund: 10:00 zero baseline", "IRGOLD", tt("2026-09-26", "10:00:00"), tt("2026-09-26", "12:00:05"), 0, false, false},
	} {
		e := sessEngine()
		r0, r1 := dayOf(t, e, c.ins, c.base, c.next, c.baseVol)
		if r1.Game.Partial != c.partial || r1.Window.Partial != c.window || hasIssue(r0, quality.DayStartMissed) != c.partial {
			t.Errorf("%s: game.partial=%v window.partial=%v DAY_START_MISSED=%v, want %v/%v/%v",
				c.name, r1.Game.Partial, r1.Window.Partial, hasIssue(r0, quality.DayStartMissed), c.partial, c.window, c.partial)
		}
		wantClass := map[string]string{"IRSTOCK": "stock", "IRGOLD": "gold"}[c.ins]
		if r1.Game.Class != wantClass || r1.Window.Class != wantClass || len(r1.Events) == 0 || r1.Events[0].Class != wantClass {
			t.Errorf("%s: class game=%q window=%q", c.name, r1.Game.Class, r1.Window.Class)
		}
	}
}

// Only the window holding the late baseline is partial; the day stays partial all day, and the
// next day is judged on its own baseline.
func TestPartialWindowAndDayScope(t *testing.T) {
	e := sessEngine()
	dayOf(t, e, "IRSTOCK", tt("2026-09-26", "09:05:00"), tt("2026-09-26", "09:05:05"), 1_000_000)
	// polled every 5 s: 09:05:10 … 09:09:55 carry no new trades (no polling gap)
	for at := tt("2026-09-26", "09:05:10"); at.Before(tt("2026-09-26", "09:10:00")); at = at.Add(5 * time.Second) {
		e.Process(sn("IRSTOCK", at, 10_000, 1_300_000, 900_000, 800_000, 51, 60))
	}
	r := e.Process(sn("IRSTOCK", tt("2026-09-26", "09:10:00"), 10_000, 1_400_000, 1_000_000, 900_000, 52, 61))
	if r.Window == nil || r.Window.Partial || !r.Game.Partial {
		t.Fatalf("09:10 window must be complete while the day stays partial: window %+v game %+v", r.Window, r.Game)
	}
	_, next := dayOf(t, e, "IRSTOCK", tt("2026-09-27", "08:59:55"), tt("2026-09-27", "09:00:05"), 0)
	if next.Game.Partial || next.Game.Day != "2026-09-27" {
		t.Fatalf("next day inherited partial: %+v", next.Game)
	}
}

// Session-relative windows: a gold fund's windows start at 12:00, 12:10, …
func TestWindowsRelativeToOwnOpen(t *testing.T) {
	e := sessEngine()
	_, r := dayOf(t, e, "IRGOLD", tt("2026-09-26", "11:59:55"), tt("2026-09-26", "12:09:59"), 0)
	if got := r.Window.WindowStart.In(tehran.Loc).Format("15:04"); got != "12:00" {
		t.Fatalf("window %s, want 12:00", got)
	}
}

// STALE only while the instrument's session is open: a stock snapshot at 13:00 whose source
// time stopped at 12:29:59 is not stale; the same lag at 10:00 is.
func TestStaleOnlyInSession(t *testing.T) {
	e := sessEngine()
	lagged := func(ins string, src, ingest time.Time) bool {
		s := sn(ins, src, 10_000, 0, 0, 0, 0, 0)
		s.IngestTime = ingest
		return hasIssue(e.Process(s), quality.Stale)
	}
	if !lagged("IRSTOCK", tt("2026-09-26", "09:59:00"), tt("2026-09-26", "10:00:00")) {
		t.Error("60s lag in session not STALE")
	}
	if lagged("IRSTOCK", tt("2026-09-26", "12:29:59"), tt("2026-09-26", "13:00:00")) {
		t.Error("STALE after the stock session closed")
	}
	if !lagged("IRGOLD", tt("2026-09-26", "12:59:00"), tt("2026-09-26", "13:00:00")) {
		t.Error("gold fund in its afternoon session not STALE")
	}
	if lagged("IRSTOCK", tt("2026-09-30", "12:29:59"), tt("2026-10-01", "10:00:00")) {
		t.Error("STALE on a Thursday (closed)")
	}
	cal, err := calendar.Parse([]byte(`{"classes": {"stock": {"rules": [{"effective_from": "2025-01-01",
		"days": ["sat","sun","mon","tue","wed"], "pre_open": "08:45", "open": "09:00", "close": "12:30", "verified": false}]}},
		"instruments": {"IRSTOCK": "stock"}, "holidays": [{"date": "2026-09-26", "name": "test"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sessions = cal
	e = New(cfg)
	if lagged("IRSTOCK", tt("2026-09-26", "09:59:00"), tt("2026-09-26", "10:00:00")) {
		t.Error("STALE on a holiday")
	}
}

// With a class opening off the 10-minute grid (09:05), the flow engine's windows follow that
// open (09:05, 09:15, …), not the clock (09:00, 09:10, …).
func TestWindowsFollowOffGridOpen(t *testing.T) {
	cal, err := calendar.Parse([]byte(`{"classes": {"odd": {"rules": [{"effective_from": "2025-01-01",
		"days": ["sat","sun","mon","tue","wed"], "pre_open": "08:50", "open": "09:05", "close": "12:30", "verified": false}]}},
		"instruments": {"IRODD": "odd"}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sessions = cal
	e := New(cfg)
	_, r := dayOf(t, e, "IRODD", tt("2026-09-26", "09:04:55"), tt("2026-09-26", "09:14:59"), 0)
	if got := r.Window.WindowStart.In(tehran.Loc).Format("15:04"); got != "09:05" {
		t.Fatalf("window %s, want 09:05", got)
	}
}

func codes(r Result) map[string]bool {
	m := map[string]bool{}
	for _, i := range r.Issues {
		m[i.Code] = true
	}
	return m
}

// A re-baseline (CUMULATIVE_DECREASE / SIDE_MISMATCH) drops an interval: the day and the window
// it falls in become partial. (a) Owner's scenario: yesterday's totals at 08:59:55, then the
// vendor resets after the open (09:00:10, 200,000 shares today): those 200,000 are swallowed.
// (b) A complete day where a count glitch at 09:00:10 drops 300,000 shares.
func TestRebaselineMarksPartial(t *testing.T) {
	e := sessEngine()
	// Since the owner's decision on PR #3 the 08:59:55 carryover is never a baseline: the
	// 09:00:10 snapshot is the first accepted one, a late baseline (DAY_START_MISSED).
	if r := e.Process(sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 5_000_000, 3_000_000, 3_000_000, 900, 900)); !codes(r)[quality.PrevDayCarryover] {
		t.Fatalf("setup: %+v", r.Issues)
	}
	r := e.Process(sn("IRSTOCK", tt("2026-09-26", "09:00:10"), 10_000, 200_000, 120_000, 120_000, 20, 20))
	if !codes(r)[quality.DayStartMissed] || codes(r)[quality.CumulativeDecrease] {
		t.Fatalf("setup: %+v", r.Issues)
	}
	r = e.Process(sn("IRSTOCK", tt("2026-09-26", "09:00:15"), 10_000, 500_000, 420_000, 320_000, 21, 20))
	if r.Game == nil || !r.Game.Partial || r.Window == nil || !r.Window.Partial {
		t.Errorf("(a) after the reset: game %+v window %+v, want both partial", r.Game, r.Window)
	}

	e = sessEngine()
	e.Process(sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	r = e.Process(sn("IRSTOCK", tt("2026-09-26", "09:00:05"), 10_000, 100_000, 60_000, 60_000, 5, 5))
	if r.Game.Partial || r.Window.Partial {
		t.Fatalf("(b) setup: complete day expected: %+v %+v", r.Game, r.Window)
	}
	// Δvolume 300,000 but Δbuy = (300,000−60,000) + (150,000−40,000) = 350,000: SIDE_MISMATCH,
	// the interval (300,000 shares) is dropped and the glitch becomes the new baseline.
	glitch := sn("IRSTOCK", tt("2026-09-26", "09:00:10"), 10_000, 400_000, 300_000, 260_000, 6, 5)
	glitch.InstBuyVol = 150_000
	if r = e.Process(glitch); !codes(r)[quality.SideMismatch] {
		t.Fatalf("(b) setup: %+v", r.Issues)
	}
	// The flip to partial is published at once (there may be no later trade), as of the glitch.
	if r.Game == nil || !r.Game.Partial || !r.Game.AsOf.Equal(glitch.SourceTime) || r.Game.Volume != 400_000 {
		t.Errorf("(b) re-baseline must publish partial totals as of the glitch: %+v", r.Game)
	}
	// A second re-baseline on an already partial day still moves as_of/volume (else consumers
	// would see the totals lagging behind the snapshots until the next trade).
	glitch2 := sn("IRSTOCK", tt("2026-09-26", "09:00:12"), 10_000, 450_000, 330_000, 260_000, 6, 5)
	glitch2.InstBuyVol = 200_000
	if r2 := e.Process(glitch2); !codes(r2)[quality.SideMismatch] || r2.Game == nil || r2.Game.Volume != 450_000 {
		t.Errorf("(b) second re-baseline: issues %+v game %+v, want totals as of 450,000", r2.Issues, r2.Game)
	}
	next := sn("IRSTOCK", tt("2026-09-26", "09:00:15"), 10_000, 550_000, 380_000, 360_000, 7, 5)
	next.InstBuyVol, next.IndBuyVol = 250_000, 380_000 // Δbuy 50,000 + 50,000 = Δvolume 100,000
	r = e.Process(next)
	if r.Game == nil || !r.Game.Partial || !r.Window.Partial {
		t.Errorf("(b) after the glitch: game %+v window %+v, want both partial", r.Game, r.Window)
	}
}

// A polling gap while trading (09:05:05 → 09:12:00, nothing ingested for ~7 minutes) books the
// earlier flow into the 09:10 window: that window is partial; the day totals are still complete
// (cumulative values lose nothing) and the next window is complete.
func TestPollingGapMarksLandingWindow(t *testing.T) {
	e := sessEngine()
	e.Process(sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	e.Process(sn("IRSTOCK", tt("2026-09-26", "09:05:05"), 10_000, 100_000, 60_000, 60_000, 5, 5))
	r := e.Process(sn("IRSTOCK", tt("2026-09-26", "09:12:00"), 10_000, 400_000, 360_000, 260_000, 6, 8))
	if !r.Window.Partial || r.Game.Partial {
		t.Fatalf("gap: window %+v game %+v", r.Window, r.Game)
	}
	for at := tt("2026-09-26", "09:12:05"); at.Before(tt("2026-09-26", "09:20:00")); at = at.Add(5 * time.Second) {
		e.Process(sn("IRSTOCK", at, 10_000, 400_000, 360_000, 260_000, 6, 8)) // polled every 5 s
	}
	r = e.Process(sn("IRSTOCK", tt("2026-09-26", "09:20:00"), 10_000, 400_000, 360_000, 260_000, 6, 8))
	if r.Window == nil || r.Window.Partial {
		t.Fatalf("next window after a normal poll must be complete: %+v", r.Window)
	}
}

// SOURCE_TIME_ESTIMATED describes the source: once per instrument per day.
func TestTimeEstimatedOncePerDay(t *testing.T) {
	e := sessEngine()
	n := 0
	for _, at := range []time.Time{tt("2026-09-26", "09:00:00"), tt("2026-09-26", "09:00:05"), tt("2026-09-26", "11:00:00"), tt("2026-09-27", "09:00:00")} {
		s := sn("IRSTOCK", at, 10_000, 0, 0, 0, 0, 0)
		s.SourceTimeEstimated = true
		if codes(e.Process(s))[quality.TimeEstimated] {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("SOURCE_TIME_ESTIMATED %d times over two days, want 2", n)
	}
}

// Baseline edge cases of the late-start rule.
func TestLateStartEdgeCases(t *testing.T) {
	// Closed day (Thursday): a zero-volume baseline is complete, one with volume is not.
	for vol, want := range map[int64]bool{0: false, 1_000_000: true} {
		e := sessEngine()
		r := e.Process(sn("IRSTOCK", tt("2026-10-01", "10:00:00"), 10_000, vol, vol*6/10, vol*6/10, 5, 5))
		if codes(r)[quality.DayStartMissed] != want {
			t.Errorf("closed day, volume %d: DAY_START_MISSED=%v want %v", vol, !want, want)
		}
	}
	// "First ACCEPTED": an incomplete pre-open snapshot is not a baseline; the first complete one
	// (09:10, with volume) is, so the day is partial.
	e := sessEngine()
	inc := sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0)
	inc.Missing = []string{model.FIndSellCount}
	e.Process(inc)
	if r := e.Process(sn("IRSTOCK", tt("2026-09-26", "09:10:00"), 10_000, 1_000_000, 600_000, 600_000, 5, 5)); !codes(r)[quality.DayStartMissed] {
		t.Error("incomplete pre-open snapshot counted as the day baseline")
	}
	// Unmapped instrument: session = union, its open is 08:30.
	for at, want := range map[string]bool{"08:29:55": false, "08:35:00": true} {
		e := sessEngine()
		if r := e.Process(sn("IRUNMAPPED", tt("2026-09-26", at), 10_000, 0, 0, 0, 0, 0)); codes(r)[quality.DayStartMissed] != want {
			t.Errorf("unknown class zero baseline at %s: DAY_START_MISSED want %v", at, want)
		}
	}
}

// STALE is not evaluated in the pre-open (no continuous trading) nor for estimated source times.
func TestStalePreOpenAndEstimated(t *testing.T) {
	e := sessEngine()
	s := sn("IRSTOCK", tt("2026-09-26", "08:50:00"), 10_000, 0, 0, 0, 0, 0)
	s.IngestTime = tt("2026-09-26", "08:52:00")
	if codes(e.Process(s))[quality.Stale] {
		t.Error("STALE in pre-open")
	}
	s = sn("IRSTOCK", tt("2026-09-26", "10:00:00"), 10_000, 0, 0, 0, 0, 0)
	s.IngestTime, s.SourceTimeEstimated = tt("2026-09-26", "10:05:00"), true
	if c := codes(e.Process(s)); c[quality.Stale] || !c[quality.TimeEstimated] {
		t.Errorf("estimated time: %v", c)
	}
}

// Owner decision on PR #3 (DL-01): before the instrument's own open, a snapshot with volume,
// value and trade count all zero is a valid day baseline even after carryover polls; non-zero
// pre-open snapshots are previous-day carryover (one PREV_DAY_CARRYOVER per instrument and day,
// never a baseline); no zero before the open leaves the day partial.
func TestPreOpenCarryover(t *testing.T) {
	carry := func(at string, vol, trades int64) model.Snapshot {
		n := int64(0)
		if vol > 0 {
			n = 900
		}
		s := sn("IRSTOCK", tt("2026-09-26", at), 10_000, vol, vol*6/10, vol*6/10, n, n)
		s.TradeCount = trades
		return s
	}
	count := func(rs []Result, code string) (n int) {
		for _, r := range rs {
			for _, i := range r.Issues {
				if i.Code == code {
					n++
				}
			}
		}
		return n
	}
	trade := sn("IRSTOCK", tt("2026-09-26", "09:00:05"), 10_000, 300_000, 300_000, 200_000, 1, 0)
	trade.TradeCount = 12

	// (1) Carryover polls, then a zero pre-open snapshot: complete day.
	e := sessEngine()
	rs := []Result{e.Process(carry("08:50:00", 5_000_000, 3_000)), e.Process(carry("08:55:00", 5_000_000, 3_000)),
		e.Process(carry("08:58:00", 0, 0)), e.Process(trade)}
	if n := count(rs, quality.PrevDayCarryover); n != 1 {
		t.Errorf("(1) PREV_DAY_CARRYOVER x%d, want once per instrument and day", n)
	}
	if count(rs, quality.DayStartMissed) != 0 || count(rs, quality.CumulativeDecrease) != 0 || rs[0].Game != nil || rs[1].Game != nil {
		t.Errorf("(1) carryover must be neither a baseline nor an interval: %+v", rs)
	}
	if g := rs[3].Game; g == nil || g.Partial || g.NetHot != 3_000_000_000 {
		t.Errorf("(1) game %+v, want complete (zero pre-open baseline after carryover)", g)
	}

	// (2) Zero pre-open snapshot directly: complete, no carryover issue.
	e = sessEngine()
	rs = []Result{e.Process(carry("08:58:00", 0, 0)), e.Process(trade)}
	if count(rs, quality.PrevDayCarryover) != 0 || rs[1].Game == nil || rs[1].Game.Partial {
		t.Errorf("(2) zero baseline: %+v", rs)
	}

	// (3) Carryover only, no zero before the open: the first snapshot after the open is a late
	// baseline, the day partial.
	e = sessEngine()
	rs = []Result{e.Process(carry("08:50:00", 5_000_000, 3_000)), e.Process(trade)}
	next := sn("IRSTOCK", tt("2026-09-26", "09:00:10"), 10_000, 600_000, 600_000, 200_000, 2, 0)
	rs = append(rs, e.Process(next))
	if count(rs, quality.PrevDayCarryover) != 1 || !hasIssue(rs[1], quality.DayStartMissed) || rs[2].Game == nil || !rs[2].Game.Partial {
		t.Errorf("(3) no zero before the open: %+v", rs)
	}

	// Any non-zero field is activity: value only, or trade count only.
	for name, s := range map[string]model.Snapshot{"trades only": carry("08:55:00", 0, 7), "value only": func() model.Snapshot {
		c := carry("08:55:00", 0, 0)
		c.Value = 1
		return c
	}()} {
		e = sessEngine()
		if r := e.Process(s); !hasIssue(r, quality.PrevDayCarryover) {
			t.Errorf("%s: not treated as carryover: %+v", name, r.Issues)
		}
		if r := e.Process(trade); !hasIssue(r, quality.DayStartMissed) {
			t.Errorf("%s: carryover used as the baseline", name)
		}
	}

	// Reported again on the next trading day.
	e = sessEngine()
	e.Process(carry("08:50:00", 5_000_000, 3_000))
	c2 := carry("08:50:00", 5_000_000, 3_000)
	c2.SourceTime, c2.IngestTime = tt("2026-09-27", "08:50:00"), tt("2026-09-27", "08:50:01")
	if r := e.Process(c2); !hasIssue(r, quality.PrevDayCarryover) {
		t.Error("carryover of the next day not reported")
	}
}

// dayEnd feeds IRSTOCK a complete 2026-09-26 (Saturday): zero pre-open baseline, one trade
// bringing it to 5,000,000 shares. Returns the engine.
func dayEnd(t *testing.T) *Engine {
	t.Helper()
	e := sessEngine()
	e.Process(sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	last := sn("IRSTOCK", tt("2026-09-26", "12:29:55"), 10_000, 5_000_000, 3_000_000, 3_000_000, 900, 900)
	last.TradeCount = 900
	if r := e.Process(last); r.Game == nil {
		t.Fatalf("setup: %+v", r)
	}
	return e
}

// yesterday's last totals shown again on 2026-09-27 (Sunday) at hms.
func repeat(hms string) model.Snapshot {
	s := sn("IRSTOCK", tt("2026-09-27", hms), 10_000, 5_000_000, 3_000_000, 3_000_000, 900, 900)
	s.TradeCount = 900
	return s
}

// Owner decision on PR #3 (rule 1), engine side: after the open, totals equal to the last
// totals of the previous day are carryover, never a baseline or an interval (review E1: a late
// reset above yesterday's totals must not book today − yesterday as one hot interval).
func TestPostOpenCarryoverEngine(t *testing.T) {
	e := dayEnd(t)
	rs := []Result{e.Process(repeat("09:00:05")), e.Process(repeat("09:30:00"))}
	if !hasIssue(rs[0], quality.PrevDayCarryover) || hasIssue(rs[1], quality.PrevDayCarryover) || rs[0].Game != nil || rs[1].Game != nil {
		t.Fatalf("carryover after the open: %+v", rs)
	}
	late := sn("IRSTOCK", tt("2026-09-27", "10:30:00"), 10_000, 6_000_000, 3_600_000, 3_000_000, 950, 900) // reset "above" yesterday
	late.TradeCount = 1000
	r := e.Process(late)
	if !hasIssue(r, quality.DayStartMissed) || r.Game != nil || len(r.Events) != 0 {
		t.Fatalf("the first changed snapshot is a late baseline, no interval from yesterday: %+v", r)
	}
	next := sn("IRSTOCK", tt("2026-09-27", "10:30:05"), 10_000, 6_100_000, 3_700_000, 3_000_000, 951, 900)
	next.TradeCount = 1001
	if r = e.Process(next); r.Game == nil || !r.Game.Partial || r.Game.NetHotPlus+r.Game.NetHot+r.Game.NetRetail+r.Game.NetUnattributed != 1_000_000_000 {
		t.Errorf("today's flow only (+100,000 shares × 10,000): %+v", r.Game)
	}
}

// Review E2: a zero pre-open baseline, then the source flips back to yesterday's totals after
// the open: never booked as today's.
func TestFlipBackAfterZeroBaseline(t *testing.T) {
	e := dayEnd(t)
	e.Process(sn("IRSTOCK", tt("2026-09-27", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	if r := e.Process(repeat("09:00:05")); !hasIssue(r, quality.PrevDayCarryover) || r.Game != nil {
		t.Fatalf("flip-back: %+v", r)
	}
	today := sn("IRSTOCK", tt("2026-09-27", "09:00:10"), 10_000, 300_000, 300_000, 200_000, 1, 0)
	if r := e.Process(today); r.Game == nil || r.Game.Partial || r.Game.NetHot != 3_000_000_000 {
		t.Errorf("today's interval from the zero baseline: %+v", r.Game)
	}
}

// Review E3: a missing trade count cannot contradict zero volume and value (documented).
func TestZeroBaselineWithMissingTradeCount(t *testing.T) {
	e := sessEngine()
	z := sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0)
	z.Missing = []string{model.FTradeCount}
	if r := e.Process(z); hasIssue(r, quality.PrevDayCarryover) || hasIssue(r, quality.DayStartMissed) {
		t.Errorf("zero volume and value, trade count missing: %+v", r.Issues)
	}
}

// The reference survives a restart (recovery replays only the current day) and is pruned.
func TestPrevTotalsPersistence(t *testing.T) {
	e := dayEnd(t)
	e.TakeRolled()
	e.Process(repeat("08:30:00")) // first snapshot of 09-27: rolls (pre-open carryover)
	if !e.TakeRolled() || e.TakeRolled() {
		t.Fatal("the roll must be reported exactly once")
	}
	day, m := e.PrevTotals()
	if day != "2026-09-26" || m["IRSTOCK"].Volume != 5_000_000 || m["IRSTOCK"].Seen != "2026-09-26" {
		t.Fatalf("reference = %s %+v", day, m)
	}
	fresh := sessEngine() // a restarted engine, reference loaded from storage
	fresh.SetPrevTotals(day, m)
	if r := fresh.Process(repeat("09:00:05")); !hasIssue(r, quality.PrevDayCarryover) {
		t.Errorf("restarted engine must apply the loaded reference: %+v", r.Issues)
	}
	// An instrument not seen for more than PrevKeepDays is dropped at the next roll.
	old := sessEngine()
	old.SetPrevTotals("2026-08-01", map[string]model.Totals{"GONE": {Volume: 1, Seen: "2026-08-01"}, "IRSTOCK": {Volume: 1, Seen: "2026-09-20"}})
	old.Process(sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	old.Process(sn("IRSTOCK", tt("2026-09-27", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	if _, m := old.PrevTotals(); m["GONE"] != (model.Totals{}) || m["IRSTOCK"].Seen != "2026-09-26" {
		t.Errorf("pruning: %+v", m)
	}
}

// Review round 2, item 1: a snapshot with a source time far ahead of its ingest time must not
// roll the whole market's reference mid-day.
func TestFutureSnapshotDoesNotRoll(t *testing.T) {
	e := dayEnd(t)
	e.TakeRolled()
	f := sn("IRGOLD", tt("2026-09-28", "10:00:00"), 10_000, 1, 1, 0, 1, 0)
	f.IngestTime = tt("2026-09-26", "12:30:00") // source time two days ahead
	e.Process(f)
	if e.TakeRolled() {
		t.Fatal("a future-dated snapshot rolled the reference")
	}
	if r := e.Process(repeat("09:00:05")); !hasIssue(r, quality.PrevDayCarryover) {
		t.Errorf("the next day's repeat must still be caught: %+v", r.Issues)
	}
}

// Item 5: the reference is what the source showed last, also on a snapshot the engine could not
// use (a flow field missing).
func TestReferenceIsLastSeenTotals(t *testing.T) {
	e := dayEnd(t)
	inc := sn("IRSTOCK", tt("2026-09-26", "12:30:10"), 10_000, 5_100_000, 3_000_000, 3_000_000, 900, 900)
	inc.TradeCount = 910
	inc.Missing = []string{model.FIndBuyCount}
	e.Process(inc)
	r := sn("IRSTOCK", tt("2026-09-27", "09:00:05"), 10_000, 5_100_000, 3_000_000, 3_000_000, 900, 900)
	r.TradeCount = 910
	if res := e.Process(r); !hasIssue(res, quality.PrevDayCarryover) {
		t.Errorf("the last seen totals are the reference: %+v", res.Issues)
	}
}

// Items 4 and 7: a halted instrument showing its old totals every day stays in the reference
// (seen) beyond PrevKeepDays; an all-zero day does not replace an active reference.
func TestHaltedAndZeroDays(t *testing.T) {
	e := dayEnd(t) // 2026-09-26: 5,000,000
	d0, _ := time.ParseInLocation("2006-01-02", "2026-09-26", tehran.Loc)
	for i := 1; i <= PrevKeepDays+5; i++ {
		s := repeat("09:30:00")
		s.SourceTime = d0.AddDate(0, 0, i).Add(9*time.Hour + 30*time.Minute)
		s.IngestTime = s.SourceTime.Add(time.Second)
		if r := e.Process(s); r.Game != nil || hasIssue(r, quality.DayStartMissed) {
			t.Fatalf("day +%d: halted instrument's old totals accepted: %+v", i, r)
		}
	}
	z := sessEngine()
	z.Process(sn("IRSTOCK", tt("2026-09-26", "08:59:55"), 10_000, 0, 0, 0, 0, 0))
	last := sn("IRSTOCK", tt("2026-09-26", "12:29:55"), 10_000, 5_000_000, 3_000_000, 3_000_000, 900, 900)
	last.TradeCount = 900
	z.Process(last)
	z.Process(sn("IRSTOCK", tt("2026-09-27", "08:59:55"), 10_000, 0, 0, 0, 0, 0)) // reset, no trade all day
	flip := repeat("09:00:00")
	flip.SourceTime, flip.IngestTime = tt("2026-09-28", "09:00:00"), tt("2026-09-28", "09:00:01")
	if r := z.Process(flip); !hasIssue(r, quality.PrevDayCarryover) {
		t.Errorf("an all-zero day must not clear the reference: %+v", r.Issues)
	}
}
