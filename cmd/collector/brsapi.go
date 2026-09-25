package main

import (
	"fmt"
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
// request per type every interval while the calendar union is open (span = its longest day).
// It names the smallest interval that fits, so exceeding the plan is always a deliberate change.
func brsapiBudget(types int, interval, span time.Duration, daily, per5min int64) error {
	if interval <= 0 {
		return fmt.Errorf("POLL_INTERVAL must be positive")
	}
	perDay := int64(types) * (int64(span/interval) + 1)
	per5 := int64(types) * int64((5*time.Minute+interval-1)/interval)
	if perDay <= daily && per5 <= per5min {
		return nil
	}
	need := interval
	if polls := daily/int64(types) - 1; polls <= 0 {
		need = span + time.Second
	} else if d := (span + time.Duration(polls) - 1) / time.Duration(polls); d > need {
		need = d
	}
	if per5min > 0 {
		if d := 5 * time.Minute * time.Duration(types) / time.Duration(per5min); d > need {
			need = d
		}
	}
	return fmt.Errorf("brsapi: POLL_INTERVAL=%s over a %s session window needs %d requests/day and %d per 5 min; "+
		"the plan allows %d and %d (BRSAPI_DAILY_LIMIT, BRSAPI_5MIN_LIMIT). Use POLL_INTERVAL>=%s, fewer BRSAPI_TYPES, "+
		"or a larger plan", interval, span, perDay, per5, daily, per5min, need.Round(time.Second)+time.Second)
}
