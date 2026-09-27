package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// perDay is the number of polls at interval over the calendar's longest day: ⌊span / interval⌋ + 1.
func perDay(span, interval time.Duration) int64 { return int64(span/interval) + 1 }

// dailyBudget refuses a polling plan whose requests over the calendar's longest day
// (⌊span / interval⌋ + 1, plus extra requests of other polls on the same quota, e.g. the index)
// exceed the daily quota in env var name; daily <= 0 disables the check.
func dailyBudget(name string, interval, span time.Duration, daily, extra int64) error {
	if daily <= 0 {
		return nil
	}
	if interval <= 0 {
		return fmt.Errorf("POLL_INTERVAL must be positive")
	}
	total := perDay(span, interval) + extra
	if total <= daily {
		return nil
	}
	left := daily - extra
	if left < 1 {
		return fmt.Errorf("%s=%d: the other polls on this quota already need %d requests/day", name, daily, extra)
	}
	need := (span/time.Duration(left) + time.Second).Truncate(time.Second)
	return fmt.Errorf("POLL_INTERVAL=%s over a %s session window needs %d requests/day (%d of them for other polls); %s=%d. "+
		"Use POLL_INTERVAL>=%ds, fewer other polls, or a larger plan", interval, span, total, extra, name, daily, need/time.Second)
}

// strictInt reads an optional integer env var: unset/empty = def; set but not an integer = error
// (a typo in a quota must not silently fall back to "no limit").
func strictInt(k string, def int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: not an integer", k, v)
	}
	return n, nil
}
