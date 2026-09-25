package democlock

import (
	"context"
	"strings"
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/tehran"
)

func at(date, hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+hm, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNowAdvancesAtRate(t *testing.T) {
	anchor := time.Unix(1_790_000_000, 0)
	c, err := New(at("2026-09-23", "11:40"), anchor, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		wall time.Duration
		want string
	}{{0, "11:40:00"}, {time.Minute, "11:45:00"}, {10 * time.Minute, "12:30:00"}, {-12 * time.Second, "11:39:00"}} {
		c.wall = func() time.Time { return anchor.Add(tc.wall) }
		if got := c.Now().In(tehran.Loc).Format("15:04:05"); got != tc.want {
			t.Errorf("wall +%s: demo %s, want %s", tc.wall, got, tc.want)
		}
	}
	if got := c.Real(50 * time.Second); got != 10*time.Second {
		t.Errorf("Real(50s) = %s at ×5, want 10s", got)
	}
}

func TestSleepIsScaled(t *testing.T) {
	c, _ := New(at("2026-09-23", "11:40"), time.Now(), 50)
	start := time.Now()
	if err := c.Sleep(context.Background(), 5*time.Second); err != nil { // 100 ms real
		t.Fatal(err)
	}
	if el := time.Since(start); el < 80*time.Millisecond || el > 2*time.Second {
		t.Fatalf("slept %s real for 5s demo at ×50, want ~100ms", el)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Sleep(ctx, time.Hour); err == nil {
		t.Fatal("Sleep ignored a cancelled context")
	}
}

func TestRateBounds(t *testing.T) {
	for _, r := range []float64{0, -1, MaxRate + 1} {
		if _, err := New(time.Now(), time.Now(), r); err == nil {
			t.Errorf("rate %v accepted", r)
		}
	}
}

// The default calendar trades Saturday–Wednesday: on Friday 2026-09-25 (and Thursday) the most
// recent trading day is Wednesday 2026-09-23; on a trading day it is that day itself.
func TestLastTradingDay(t *testing.T) {
	cal := calendar.Default()
	for _, tc := range []struct{ now, want string }{
		{"2026-09-25 15:00", "2026-09-23"}, // Friday
		{"2026-09-24 09:30", "2026-09-23"}, // Thursday
		{"2026-09-26 00:10", "2026-09-26"}, // Saturday, before the open
		{"2026-09-23 23:59", "2026-09-23"},
	} {
		now, _ := time.ParseInLocation("2006-01-02 15:04", tc.now, tehran.Loc)
		d, ok := LastTradingDay(cal, now)
		if !ok || tehran.TradingDay(d) != tc.want || !d.Equal(tehran.DayStart(d)) {
			t.Errorf("now %s: last trading day %s (%v), want %s at 00:00", tc.now, d, ok, tc.want)
		}
	}
}

func TestParseStart(t *testing.T) {
	cal := calendar.Default()
	now := at("2026-09-25", "15:00")
	for spec, want := range map[string]time.Time{
		"last 11:40":       at("2026-09-23", "11:40"),
		"2026-09-20 09:05": at("2026-09-20", "09:05"),
	} {
		got, err := ParseStart(spec, cal, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: %s, %v; want %s", spec, got, err, want)
		}
	}
	for _, bad := range []string{"11:40", "yesterday 11:40", "last 25:00", "2026-13-01 10:00", "last 11:40 x"} {
		if _, err := ParseStart(bad, cal, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFromEnv(t *testing.T) {
	cal := calendar.Default()
	now := at("2026-09-25", "15:00")
	t.Setenv("DEMO_CLOCK", "")
	if c, err := FromEnv(cal, now); c != nil || err != nil {
		t.Fatalf("unset DEMO_CLOCK: %v, %v; want off", c, err)
	}
	t.Setenv("DEMO_CLOCK", "last 11:40")
	t.Setenv("DEMO_CLOCK_RATE", "5")
	if _, err := FromEnv(cal, now); err == nil || !strings.Contains(err.Error(), "DEMO_CLOCK_ANCHOR") {
		t.Fatalf("missing anchor: %v", err)
	}
	t.Setenv("DEMO_CLOCK_ANCHOR", "1790000000")
	c, err := FromEnv(cal, now)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Start.Equal(at("2026-09-23", "11:40")) || c.Rate != 5 || c.Anchor.Unix() != 1_790_000_000 {
		t.Fatalf("clock %+v", c)
	}
	t.Setenv("DEMO_CLOCK_RATE", "500")
	if _, err := FromEnv(cal, now); err == nil {
		t.Fatal("rate 500 accepted")
	}
}

func TestRequireLoopback(t *testing.T) {
	if err := RequireLoopback("nats://127.0.0.1:4224", "http://localhost:8000/api", "127.0.0.1:8080", "[::1]:8080", "ws://127.0.0.1:8000/connection/websocket"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"nats://10.0.0.5:4222", "http://example.com/api", "0.0.0.0:8080", ":8080"} {
		if err := RequireLoopback(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	err := RequireLoopback("nats://user:s3cret@10.0.0.5:4222")
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("credentials leaked or accepted: %v", err)
	}
}
