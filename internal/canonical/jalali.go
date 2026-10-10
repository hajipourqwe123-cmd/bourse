// Package canonical builds the canonical market dataset (Phase 5B): stable
// instrument identity, a verified trading calendar, temporal industry
// membership, whole-market daily history and the quality gates over it.
//
// Raw provider values are preserved exactly as received. Normalization,
// adjustment and repair are separate layers with their own provenance, so a
// canonical row can always be traced back to the raw observation it came from.
package canonical

import (
	"fmt"
	"time"
)

// Jalali dates arrive from BrsApi as "1405-07-01" or "1405/07/01" and must
// become ISO Gregorian trade dates. The conversion is exact, not approximate:
// a one-day error silently misaligns every instrument's history.
//
// The algorithm is the standard jalaali one (leap years from the 33-year cycle
// breaks). It is implemented over day numbers so it never depends on the host
// time zone. Years outside minJalaliYear..maxJalaliYear are rejected rather
// than silently extrapolated: the cycle breaks are only established inside
// that window, and market data never needs more.

// jalaaliBreaks are the years at which the Jalali leap-year pattern changes.
var jalaaliBreaks = [...]int{-61, 9, 38, 199, 426, 686, 756, 818, 1111, 1181,
	1210, 1635, 2060, 2097, 2192, 2262, 2324, 2394, 2456, 3178}

// Supported Jalali years: the window in which the jalaali leap-year breaks are
// documented as accurate (Gregorian 1799-2254).
const (
	minJalaliYear = 1178
	maxJalaliYear = 1633
)

// jalCal returns the Gregorian year of Farvardin 1 of jy, the March day on
// which it falls, and the number of leap years used for day-of-year maths.
func jalCal(jy int) (gy, march, leap int, err error) {
	if jy < minJalaliYear || jy > maxJalaliYear {
		return 0, 0, 0, fmt.Errorf("jalali year %d outside supported range %d-%d", jy, minJalaliYear, maxJalaliYear)
	}
	gy = jy + 621
	leapJ := -14
	jp := jalaaliBreaks[0]
	var jump int
	for i := 1; i < len(jalaaliBreaks); i++ {
		jm := jalaaliBreaks[i]
		jump = jm - jp
		if jy < jm {
			break
		}
		leapJ += (jump/33)*8 + (jump%33)/4
		jp = jm
	}
	n := jy - jp
	leapJ += (n/33)*8 + ((n%33)+3)/4
	if jump%33 == 4 && jump-n == 4 {
		leapJ++
	}
	leapG := (gy/4 - (gy/100+1)*3/4) - 150
	march = 20 + leapJ - leapG
	if jump-n < 6 {
		n = n - jump + (jump+4)/33*33
	}
	leap = ((n+1)%33 - 1) % 4
	if leap == -1 {
		leap = 4
	}
	return gy, march, leap, nil
}

// JalaliLeap reports whether a Jalali year has 366 days.
func JalaliLeap(jy int) (bool, error) {
	_, _, leap, err := jalCal(jy)
	if err != nil {
		return false, err
	}
	return leap == 0, nil
}

// JalaliToGregorian converts a Jalali calendar date to a UTC-anchored
// Gregorian date (time 00:00 UTC), so it is a pure date with no zone shift.
func JalaliToGregorian(jy, jm, jd int) (time.Time, error) {
	if jm < 1 || jm > 12 {
		return time.Time{}, fmt.Errorf("jalali month %d out of range", jm)
	}
	if jd < 1 || jd > 31 {
		return time.Time{}, fmt.Errorf("jalali day %d out of range", jd)
	}
	gy, march, leap, err := jalCal(jy)
	if err != nil {
		return time.Time{}, err
	}
	// Months 1-6 have 31 days, 7-11 have 30, month 12 has 29 or 30.
	maxDay := 31
	switch {
	case jm == 12:
		maxDay = 29 + boolInt(leap == 0)
	case jm > 6:
		maxDay = 30
	}
	if jd > maxDay {
		return time.Time{}, fmt.Errorf("jalali date %04d-%02d-%02d does not exist", jy, jm, jd)
	}
	doy := (jm - 1) * 31 // days before this month, if every month had 31
	if jm > 6 {
		doy = 6*31 + (jm-7)*30
	}
	start := time.Date(gy, time.March, march, 0, 0, 0, 0, time.UTC)
	return start.AddDate(0, 0, doy+jd-1), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ParseJalaliDate converts "1405-07-01" or "1405/07/01" (optionally with
// Persian digits already normalized) to an ISO Gregorian date string.
func ParseJalaliDate(s string) (string, error) {
	var jy, jm, jd int
	norm := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			norm = append(norm, byte(r))
		case r >= '۰' && r <= '۹': // Persian-Indic digits
			norm = append(norm, byte('0'+r-'۰'))
		case r == '-' || r == '/' || r == '.':
			norm = append(norm, '-')
		default:
			return "", fmt.Errorf("unexpected character %q in jalali date %q", r, s)
		}
	}
	if _, err := fmt.Sscanf(string(norm), "%d-%d-%d", &jy, &jm, &jd); err != nil {
		return "", fmt.Errorf("jalali date %q: %w", s, err)
	}
	g, err := JalaliToGregorian(jy, jm, jd)
	if err != nil {
		return "", err
	}
	return g.Format("2006-01-02"), nil
}
