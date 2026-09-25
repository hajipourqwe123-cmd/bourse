package main

import (
	"strings"
	"testing"
	"time"

	"bourse/internal/calendar"
)

// Budget arithmetic by hand, default calendar: union 08:25–18:00 = 9h35m = 34,500 s.
//   - free plan, 100/day, one type: polls allowed 100 → at most 99 intervals → interval >=
//     34,500/99 = 348.5 s; at 349 s: floor(34,500/349) + 1 = 98 + 1 = 99 requests.
//   - two types: 50 polls → 49 intervals → >= 704.1 s; at 705 s: 2·(48 + 1) = 98.
//   - 5-minute quota 300: at 1 s one type sends 300 (ok), two types 600 (refused).
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
		{1, 349 * time.Second, 100, 300, true},
		{1, 300 * time.Second, 100, 300, false}, // 116/day
		{2, 705 * time.Second, 100, 300, true},
		{2, 600 * time.Second, 100, 300, false},
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
