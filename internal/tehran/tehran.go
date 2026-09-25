// Package tehran provides the canonical market clock (Asia/Tehran).
package tehran

import (
	"time"
	_ "time/tzdata" // embed tz database so the binary does not depend on the host
)

// Loc is the Asia/Tehran location. All trading-day logic uses it.
var Loc = mustLoad()

func mustLoad() *time.Location {
	l, err := time.LoadLocation("Asia/Tehran")
	if err != nil {
		panic("tehran: cannot load Asia/Tehran: " + err.Error())
	}
	return l
}

// TradingDay returns the Tehran calendar date (YYYY-MM-DD) of t.
func TradingDay(t time.Time) string { return t.In(Loc).Format("2006-01-02") }

// Floor10m returns the start of the 10-minute window containing t, in Tehran time.
func Floor10m(t time.Time) time.Time {
	lt := t.In(Loc)
	m := lt.Minute() - lt.Minute()%10
	return time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), m, 0, 0, Loc)
}
