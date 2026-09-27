package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"bourse/internal/config"
	"bourse/internal/source"
)

// brsapiConfig reads SOURCE=brsapi settings. The quota defaults are the free plan's, as the
// vendor reported them on 2026-09-25 (AllSymbols: 100 requests/day; 300 per 5 minutes across
// endpoints); set them to your plan's values only after upgrading (docs/source-mapping.md).
func brsapiConfig() (source.BrsApiConfig, int64, error) {
	var types []string
	for _, t := range strings.Split(config.Str("BRSAPI_TYPES", "1"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			if t != "1" && t != "4" {
				return source.BrsApiConfig{}, 0, fmt.Errorf("BRSAPI_TYPES: %q is not a verified type (1 = stocks, rights, funds; 4 = bonds)", t)
			}
			types = append(types, t)
		}
	}
	if len(types) == 0 {
		return source.BrsApiConfig{}, 0, fmt.Errorf("BRSAPI_TYPES is empty")
	}
	return source.BrsApiConfig{
		URL:        config.Str("BRSAPI_URL", "https://api.brsapi.ir/Tsetmc/AllSymbols.php"),
		Key:        os.Getenv("BRSAPI_KEY"),
		Types:      types,
		Timeout:    config.Dur("HTTP_TIMEOUT", 10*time.Second),
		DailyLimit: int(config.Int("BRSAPI_DAILY_LIMIT", 100)),
	}, config.Int("BRSAPI_5MIN_LIMIT", 300), nil
}

// brsapiBudget refuses a polling plan the vendor quota cannot carry: the collector sends one
// request per type every interval while the calendar union is open (span = its longest day):
//
//	per day   = types · (⌊span / interval⌋ + 1)
//	per 5 min = types · ⌈5 min / interval⌉
//
// It names the smallest whole-second interval that fits, so exceeding the plan is always a
// deliberate change.
func brsapiBudget(types int, interval, span time.Duration, daily, per5min int64) error {
	if interval <= 0 || types <= 0 {
		return fmt.Errorf("POLL_INTERVAL and BRSAPI_TYPES must be positive")
	}
	perDay := int64(types) * (int64(span/interval) + 1)
	per5 := int64(types) * int64((5*time.Minute+interval-1)/interval)
	if perDay <= daily && per5 <= per5min {
		return nil
	}
	polls, polls5 := daily/int64(types), per5min/int64(types) // polls a day / per 5 min
	if polls < 1 || polls5 < 1 {
		return fmt.Errorf("brsapi: the plan allows %d requests/day and %d per 5 min: not one poll of %d type(s) fits; "+
			"use fewer BRSAPI_TYPES or a larger plan", daily, per5min, types)
	}
	// ⌊span/I⌋ + 1 <= polls  ⇔  I > span/polls;  ⌈5m/I⌉ <= polls5  ⇔  I >= 5m/polls5.
	need := span/time.Duration(polls) + 1
	if d := (5*time.Minute + time.Duration(polls5) - 1) / time.Duration(polls5); d > need {
		need = d
	}
	need = (need + time.Second - 1).Truncate(time.Second)
	return fmt.Errorf("brsapi: POLL_INTERVAL=%s over a %s session window needs %d requests/day and %d per 5 min; "+
		"the plan allows %d and %d (BRSAPI_DAILY_LIMIT, BRSAPI_5MIN_LIMIT). Use POLL_INTERVAL>=%ds, fewer BRSAPI_TYPES, "+
		"or a larger plan", interval, span, perDay, per5, daily, per5min, need/time.Second)
}

// brsapiIntervalWarning: an interval longer than the engine's polling-gap threshold (30 s) or
// STALE_AFTER turns every interval into a gap (partial windows) and every row stale, and the
// hot-money bands, calibrated at 5 s, lose their meaning (docs/source-mapping.md).
func brsapiIntervalWarning(interval time.Duration) {
	gap := 30 * time.Second
	if stale := config.Dur("STALE_AFTER", 30*time.Second); stale < gap {
		gap = stale
	}
	if interval > gap {
		log.Printf("collector: WARNING: POLL_INTERVAL=%s is longer than the %s polling-gap/stale threshold: "+
			"10-minute windows will be partial, rows stale, and hot-money bands are not calibrated for it", interval, gap)
	}
}
