package tehran

import (
	"testing"
	"time"
)

func TestDayStart(t *testing.T) {
	// 2026-09-22 21:00 UTC = 2026-09-23 00:30 Tehran (+03:30): already the 23rd in Tehran.
	got := DayStart(time.Date(2026, 9, 22, 21, 0, 0, 0, time.UTC))
	want := time.Date(2026, 9, 22, 20, 30, 0, 0, time.UTC) // 2026-09-23 00:00 Tehran
	if !got.Equal(want) {
		t.Fatalf("DayStart = %s, want %s", got.UTC(), want)
	}
	if TradingDay(got) != "2026-09-23" || TradingDay(got.Add(-time.Nanosecond)) != "2026-09-22" {
		t.Fatalf("DayStart is not the first instant of its trading day")
	}
}
