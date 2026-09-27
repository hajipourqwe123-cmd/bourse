package tehran

import "testing"

func TestJalaliToGregorian(t *testing.T) {
	for _, c := range []struct{ jy, jm, jd, gy, gm, gd int }{
		{1405, 7, 1, 2026, 9, 23},   // the D-03 payload day (docs/source-mapping.md)
		{1405, 7, 4, 2026, 9, 26},   // the pre-open recording
		{1405, 7, 5, 2026, 9, 27},   // the live review day
		{1404, 1, 1, 2025, 3, 21},   // Nowruz 1404
		{1403, 12, 30, 2025, 3, 20}, // last day of the leap year 1403
		{1399, 1, 1, 2020, 3, 20},   // Nowruz 1399 (Gregorian leap year)
	} {
		gy, gm, gd := JalaliToGregorian(c.jy, c.jm, c.jd)
		if gy != c.gy || gm != c.gm || gd != c.gd {
			t.Errorf("%d/%d/%d → %d-%d-%d, want %d-%d-%d", c.jy, c.jm, c.jd, gy, gm, gd, c.gy, c.gm, c.gd)
		}
	}
}

func TestParseJalali(t *testing.T) {
	got, err := ParseJalali("1405-07-01", "17:15:36")
	if err != nil || got.Format("2006-01-02 15:04:05 -0700") != "2026-09-23 17:15:36 +0330" {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := ParseJalali("1405/7/1", ""); err != nil || got.Format("2006-01-02 15:04") != "2026-09-23 00:00" {
		t.Fatalf("slash form: %v %v", got, err)
	}
	for _, bad := range [][2]string{{"1405-13-01", ""}, {"1405-07-31", ""}, {"abc", ""}, {"1405-07-01", "25:00"}, {"1405-07-01", "12"}} {
		if _, err := ParseJalali(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
