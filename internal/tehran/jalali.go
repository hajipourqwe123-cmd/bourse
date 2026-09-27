package tehran

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseJalali parses a Jalali (Solar Hijri) date "1405-07-01" or "1405/7/1" (Latin digits) and an
// optional time "17:15:36" or "17:15" as Tehran local time. Vendors send Jalali dates; nothing is
// guessed: any malformed part is an error.
func ParseJalali(date, clock string) (time.Time, error) {
	parts := strings.FieldsFunc(strings.TrimSpace(date), func(r rune) bool { return r == '-' || r == '/' })
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("jalali date %q: want Y-M-D", date)
	}
	var ymd [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return time.Time{}, fmt.Errorf("jalali date %q: %w", date, err)
		}
		ymd[i] = v
	}
	jy, jm, jd := ymd[0], ymd[1], ymd[2]
	if jy < 1300 || jy > 1500 || jm < 1 || jm > 12 || jd < 1 || jd > 31 || (jm > 6 && jd > 30) {
		return time.Time{}, fmt.Errorf("jalali date %q out of range", date)
	}
	gy, gm, gd := JalaliToGregorian(jy, jm, jd)
	var h, m, s int
	if c := strings.TrimSpace(clock); c != "" {
		f := strings.Split(c, ":")
		if len(f) < 2 || len(f) > 3 {
			return time.Time{}, fmt.Errorf("time %q: want HH:MM[:SS]", clock)
		}
		var hms [3]int
		for i, p := range f {
			v, err := strconv.Atoi(p)
			if err != nil {
				return time.Time{}, fmt.Errorf("time %q: %w", clock, err)
			}
			hms[i] = v
		}
		h, m, s = hms[0], hms[1], hms[2]
		if h > 23 || m > 59 || s > 59 {
			return time.Time{}, fmt.Errorf("time %q out of range", clock)
		}
	}
	t := time.Date(gy, time.Month(gm), gd, h, m, s, 0, Loc)
	if y, mo, d := t.Date(); y != gy || int(mo) != gm || d != gd {
		return time.Time{}, fmt.Errorf("jalali date %q is not a real day", date)
	}
	return t, nil
}

// JalaliToGregorian converts a Jalali date (the widely used arithmetic of jdf, valid for
// 1300–1500 AP, the range this project needs).
func JalaliToGregorian(jy, jm, jd int) (gy, gm, gd int) {
	jy += 1595
	days := -355668 + 365*jy + (jy/33)*8 + ((jy%33)+3)/4 + jd
	if jm < 7 {
		days += (jm - 1) * 31
	} else {
		days += (jm-7)*30 + 186
	}
	gy = 400 * (days / 146097)
	days %= 146097
	if days > 36524 {
		days--
		gy += 100 * (days / 36524)
		days %= 36524
		if days >= 365 {
			days++
		}
	}
	gy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		gy += (days - 1) / 365
		days = (days - 1) % 365
	}
	gd = days + 1
	feb := 28
	if (gy%4 == 0 && gy%100 != 0) || gy%400 == 0 {
		feb = 29
	}
	months := [13]int{0, 31, feb, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for gm = 1; gm <= 12 && gd > months[gm]; gm++ {
		gd -= months[gm]
	}
	return gy, gm, gd
}
