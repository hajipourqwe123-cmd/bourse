package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// dailyBudget refuses a polling plan whose requests over the calendar's longest day
// (⌊span / interval⌋ + 1) exceed the daily quota in env var name; daily <= 0 disables the check.
func dailyBudget(name string, interval, span time.Duration, daily int64) error {
	if daily <= 0 {
		return nil
	}
	if interval <= 0 {
		return fmt.Errorf("POLL_INTERVAL must be positive")
	}
	perDay := int64(span/interval) + 1
	if perDay <= daily {
		return nil
	}
	need := (span/time.Duration(daily) + time.Second).Truncate(time.Second)
	return fmt.Errorf("POLL_INTERVAL=%s over a %s session window needs %d requests/day; %s=%d. Use POLL_INTERVAL>=%ds or a larger plan",
		interval, span, perDay, name, daily, need/time.Second)
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
