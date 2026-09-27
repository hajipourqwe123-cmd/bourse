// Package democlock is a DEVELOPMENT-ONLY virtual clock for reviewing the dashboard while the
// market is closed: a SYNTHETIC day is replayed as if it were happening now, from a chosen
// Tehran time of a chosen trading day, at a chosen speed. It is off unless DEMO_CLOCK is set;
// production never sets it, and every process refuses it outside a loopback-only, synthetic-only
// setup (rules 5 and 7; docs/demo-clock.md).
//
//	DEMO_CLOCK="last 11:40"        # or "2026-09-23 11:40"; "last" = most recent trading day
//	DEMO_CLOCK_RATE=5              # demo seconds per real second, 1..60 (default 1)
//	DEMO_CLOCK_ANCHOR=1790000000   # real unix seconds at which the demo clock reads DEMO_CLOCK;
//	                               # the SAME value in every process, so they share one timeline
package democlock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/config"
	"bourse/internal/tehran"
)

// MaxRate bounds DEMO_CLOCK_RATE: faster, the collector's batches (5 s apart) outrun the engine.
const MaxRate = 60

// Clock maps the real wall clock onto the demo timeline: Now = Start + Rate·(wall − Anchor).
type Clock struct {
	Start  time.Time // demo instant at Anchor
	Anchor time.Time // real instant
	Rate   float64
	wall   func() time.Time
}

// New returns a clock reading start at the real instant anchor and advancing rate times faster.
func New(start, anchor time.Time, rate float64) (*Clock, error) {
	if !(rate > 0 && rate <= MaxRate) {
		return nil, fmt.Errorf("DEMO_CLOCK_RATE %v: want a number in (0, %d]", rate, MaxRate)
	}
	return &Clock{Start: start, Anchor: anchor, Rate: rate, wall: time.Now}, nil
}

// Now is the current demo instant.
func (c *Clock) Now() time.Time {
	return c.Start.Add(time.Duration(float64(c.wall().Sub(c.Anchor)) * c.Rate))
}

// Real converts a demo duration into the real duration it takes.
func (c *Clock) Real(d time.Duration) time.Duration { return time.Duration(float64(d) / c.Rate) }

// Sleep waits until the demo clock has advanced by d (or ctx ends).
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(c.Real(d))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Clock) String() string {
	return fmt.Sprintf("%s Tehran ×%g", c.Start.In(tehran.Loc).Format("2006-01-02 15:04"), c.Rate)
}

// FromEnv reads DEMO_CLOCK, DEMO_CLOCK_RATE and DEMO_CLOCK_ANCHOR; nil (and no error) when
// DEMO_CLOCK is unset. "last" is resolved with cal relative to the real instant now.
func FromEnv(cal *calendar.Calendar, now time.Time) (*Clock, error) {
	spec := strings.TrimSpace(config.Str("DEMO_CLOCK", ""))
	if spec == "" || spec == "off" {
		return nil, nil
	}
	start, err := ParseStart(spec, cal, now)
	if err != nil {
		return nil, err
	}
	rate := 1.0
	if v := config.Str("DEMO_CLOCK_RATE", ""); v != "" {
		if rate, err = strconv.ParseFloat(v, 64); err != nil {
			return nil, fmt.Errorf("DEMO_CLOCK_RATE %q: want a number", v)
		}
	}
	v := config.Str("DEMO_CLOCK_ANCHOR", "")
	if v == "" {
		return nil, errors.New("DEMO_CLOCK_ANCHOR is required with DEMO_CLOCK (real unix seconds; the same value in every process)")
	}
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("DEMO_CLOCK_ANCHOR %q: want unix seconds", v)
	}
	return New(start, time.Unix(sec, 0), rate)
}

// ParseStart parses "<day> <HH:MM>" (Tehran), day being YYYY-MM-DD or "last" (LastTradingDay).
func ParseStart(spec string, cal *calendar.Calendar, now time.Time) (time.Time, error) {
	f := strings.Fields(spec)
	if len(f) != 2 {
		return time.Time{}, fmt.Errorf("DEMO_CLOCK %q: want \"last HH:MM\" or \"YYYY-MM-DD HH:MM\" (Tehran)", spec)
	}
	var day time.Time
	if f[0] == "last" {
		d, ok := LastTradingDay(cal, now)
		if !ok {
			return time.Time{}, errors.New("DEMO_CLOCK: no trading day in the last 30 days of the session calendar")
		}
		day = d
	} else {
		d, err := time.ParseInLocation("2006-01-02", f[0], tehran.Loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("DEMO_CLOCK day %q: want YYYY-MM-DD or last", f[0])
		}
		day = d
	}
	hm, err := time.Parse("15:04", f[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("DEMO_CLOCK time %q: want HH:MM", f[1])
	}
	return day.Add(time.Duration(hm.Hour())*time.Hour + time.Duration(hm.Minute())*time.Minute), nil
}

// LastTradingDay is 00:00 Tehran of the most recent day, today included, on which at least one
// instrument class trades according to cal.
func LastTradingDay(cal *calendar.Calendar, now time.Time) (time.Time, bool) {
	day := tehran.DayStart(now)
	for i := 0; i < 30; i++ {
		// Noon avoids any DST edge of the Tehran calendar day.
		if _, open := cal.Union(day.Add(12 * time.Hour)); open {
			return day, true
		}
		day = tehran.DayStart(day.Add(-12 * time.Hour))
	}
	return time.Time{}, false
}

// RequireLoopback checks that every endpoint (URL or host:port) is on this machine: demo data
// must never leave it.
func RequireLoopback(endpoints ...string) error {
	for _, e := range endpoints {
		host := e
		if u, err := url.Parse(e); err == nil && u.Host != "" {
			host = u.Hostname()
		} else if h, _, err := net.SplitHostPort(e); err == nil {
			host = h
		}
		if host == "localhost" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			continue
		}
		return fmt.Errorf("DEMO_CLOCK needs loopback endpoints only; %q is not (host %q)", redactUserinfo(e), host)
	}
	return nil
}

// redactUserinfo hides credentials of a URL in errors (rule 3).
func redactUserinfo(e string) string {
	if u, err := url.Parse(e); err == nil && u.User != nil {
		u.User = url.User("REDACTED")
		return u.String()
	}
	return e
}
