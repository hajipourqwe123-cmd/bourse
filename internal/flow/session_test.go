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
		{"pre-open non-zero baseline 08:59:55 (yesterday's totals)", "IRSTOCK", tt("2026-09-26", "08:59:55"), tt("2026-09-26", "09:00:05"), 1_000_000, true, false},
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
		"days": ["sat","sun","mon","tue","wed"], "pre_open": "08:45", "open": "09:00", "close": "12:30"}]}},
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
		"days": ["sat","sun","mon","tue","wed"], "pre_open": "08:50", "open": "09:05", "close": "12:30"}]}},
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
