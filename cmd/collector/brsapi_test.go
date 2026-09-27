package main

import (
	"strings"
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/tehran"
)

// Budget arithmetic by hand, default calendar: union 08:25–18:00 = 9h35m = 34,500 s.
//   - free plan, 100/day, one type: ⌊34,500/I⌋ + 1 <= 100 ⇔ I > 345 s: at 346 s ⌊99.71⌋ + 1 =
//     100 (ok), at 345 s 100 + 1 = 101 (refused); the smallest whole second is 346 s.
//   - two types: 50 polls ⇔ I > 690 s: at 691 s 2·(49 + 1) = 100 (ok), at 690 s 2·51 = 102.
//   - 5-minute quota 300: at 1 s one type sends 300 (ok), two types 600 (refused).
//   - stock session only (08:45–12:30 = 13,500 s): I > 135 s → 136 s.
func TestBrsApiBudget(t *testing.T) {
	span := calendar.Default().MaxDailySpan()
	if span != 9*time.Hour+35*time.Minute {
		t.Fatalf("calendar union span %s, the hand calculation assumes 9h35m", span)
	}
	for _, tc := range []struct {
		types    int
		interval time.Duration
		daily    int64
		per5     int64
		ok       bool
	}{
		{1, 5 * time.Second, 100, 300, false}, // 6,901 requests/day on the free plan
		{1, 346 * time.Second, 100, 300, true},
		{1, 345 * time.Second, 100, 300, false}, // 101/day
		{2, 691 * time.Second, 100, 300, true},
		{2, 690 * time.Second, 100, 300, false},
		{1, time.Second, 1_000_000, 300, true},
		{2, time.Second, 1_000_000, 300, false},
		{1, 5 * time.Second, 7_000, 1_000, true}, // a plan that carries 5 s polling
	} {
		err := brsapiBudget(tc.types, tc.interval, span, tc.daily, tc.per5)
		if (err == nil) != tc.ok {
			t.Errorf("%+v: %v", tc, err)
		}
		if err != nil && !strings.Contains(err.Error(), "POLL_INTERVAL>=") {
			t.Errorf("error must name the interval that fits: %v", err)
		}
	}
	for want, sp := range map[string]time.Duration{"346s": span, "136s": 13_500 * time.Second} {
		if err := brsapiBudget(1, 5*time.Second, sp, 100, 300); err == nil || !strings.Contains(err.Error(), "POLL_INTERVAL>="+want) {
			t.Errorf("span %s: want the smallest interval %s, got %v", sp, want, err)
		}
	}
	if err := brsapiBudget(2, time.Hour, span, 1, 300); err == nil || !strings.Contains(err.Error(), "not one poll") {
		t.Errorf("daily < types: %v", err)
	}
	// The interval the error suggests must itself pass.
	err := brsapiBudget(1, 5*time.Second, span, 100, 300)
	var d time.Duration
	if _, rest, ok := strings.Cut(err.Error(), "POLL_INTERVAL>="); ok {
		d, _ = time.ParseDuration(strings.TrimSuffix(strings.Fields(rest)[0], ","))
	}
	if d == 0 || brsapiBudget(1, d, span, 100, 300) != nil {
		t.Errorf("suggested interval %s does not fit: %v", d, err)
	}
}

// Outside the session the collector wakes at the pre-open, not up to one interval later.
func TestOutsideWaitStopsAtPreOpen(t *testing.T) {
	cal := calendar.Default()
	at := func(s string) time.Time {
		v, _ := time.ParseInLocation("2006-01-02 15:04", s, tehran.Loc)
		return v
	}
	for _, tc := range []struct {
		now  string
		want time.Duration
	}{
		{"2026-09-26 08:20", 5 * time.Minute},   // Saturday: union pre-open 08:25
		{"2026-09-26 07:00", 346 * time.Second}, // pre-open further than one interval
		{"2026-09-26 18:30", 346 * time.Second}, // after the close
		{"2026-09-25 08:20", 346 * time.Second}, // Friday: no session
	} {
		if got := outsideWait(cal, at(tc.now), 346*time.Second); got != tc.want {
			t.Errorf("%s: wait %s, want %s", tc.now, got, tc.want)
		}
	}
}

func TestBrsApiConfigTypes(t *testing.T) {
	t.Setenv("BRSAPI_TYPES", "1, 4")
	cfg, per5, err := brsapiConfig()
	if err != nil || len(cfg.Types) != 2 || cfg.DailyLimit != 100 || per5 != 300 {
		t.Fatalf("%+v %d %v", cfg.Types, per5, err)
	}
	t.Setenv("BRSAPI_TYPES", "1,2")
	if _, _, err := brsapiConfig(); err == nil {
		t.Fatal("unverified type accepted")
	}
}

func TestDailyBudget(t *testing.T) {
	span := 9*time.Hour + 35*time.Minute // 34,500 s
	if err := dailyBudget("X", 90*time.Second, span, 500, 0); err != nil {
		t.Fatalf("90 s = 384/day fits 500: %v", err)
	}
	err := dailyBudget("X", 60*time.Second, span, 500, 0) // 576/day
	if err == nil || !strings.Contains(err.Error(), "POLL_INTERVAL>=70s") {
		t.Fatalf("want refusal naming 70s, got %v", err)
	}
	if err := dailyBudget("X", 70*time.Second, span, 500, 0); err != nil {
		t.Fatalf("the named interval must fit: %v", err)
	}
	if err := dailyBudget("X", 69*time.Second, span, 500, 0); err == nil { // 34500/69 = 500 exactly → 501 requests
		t.Fatal("an interval dividing the span exactly needs one more request than span/interval")
	}
	// Index polls share the quota: 40/day with the index every 30 min (20/day) leaves 20 snapshots.
	idx := perDay(span, 30*time.Minute) // ⌊34500/1800⌋ + 1 = 20
	if idx != 20 {
		t.Fatalf("index polls/day = %d, want 20", idx)
	}
	if err := dailyBudget("X", 30*time.Minute, span, 40, idx); err != nil { // 20 + 20 = 40
		t.Fatalf("20 + 20 fits 40: %v", err)
	}
	if err := dailyBudget("X", 20*time.Minute, span, 40, idx); err == nil || !strings.Contains(err.Error(), "POLL_INTERVAL>=1726s") {
		t.Fatalf("⌊34500/1200⌋+1 = 29 + 20 > 40 must name 34500/20+1 = 1726 s, got %v", err)
	}
	if err := dailyBudget("X", time.Hour, span, 20, idx); err == nil {
		t.Fatal("no request left for snapshots must be refused")
	}
	if err := dailyBudget("X", time.Second, span, 0, 0); err != nil {
		t.Fatalf("no limit set: %v", err)
	}
}
