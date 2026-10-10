package canonical

import "testing"

// Anchors are independent of this code: Nowruz dates, the repo's own recorded
// facts (archive_feasibility: Foolad history starts 1385/12/20 = 2007-03-11)
// and the BrsApi sample's latest row (1405-07-01, a Wednesday, recorded
// 2026-09-25 after Thu/Fri non-trading days).
func TestJalaliToGregorianAnchors(t *testing.T) {
	cases := []struct {
		jy, jm, jd int
		want       string
	}{
		{1385, 12, 20, "2007-03-11"}, // Foolad's earliest history row
		{1405, 7, 1, "2026-09-23"},   // BrsApi sample's latest row
		{1405, 1, 1, "2026-03-21"},   // Nowruz 1405
		{1404, 1, 1, "2025-03-21"},
		{1403, 1, 1, "2024-03-20"},
		{1399, 1, 1, "2020-03-20"},
		{1378, 10, 11, "2000-01-01"}, // Gregorian millennium
		{1404, 12, 29, "2026-03-20"}, // last day of a 365-day year, i.e. the day before Nowruz 1405
		{1403, 12, 30, "2025-03-20"}, // 1403 is a leap year: Esfand 30 exists
	}
	for _, c := range cases {
		got, err := JalaliToGregorian(c.jy, c.jm, c.jd)
		if err != nil {
			t.Errorf("%04d-%02d-%02d: %v", c.jy, c.jm, c.jd, err)
			continue
		}
		if g := got.Format("2006-01-02"); g != c.want {
			t.Errorf("%04d-%02d-%02d = %s, want %s", c.jy, c.jm, c.jd, g, c.want)
		}
	}
}

func TestJalaliLeapAndImpossibleDates(t *testing.T) {
	for jy, want := range map[int]bool{1403: true, 1404: false, 1399: true, 1405: false} {
		if got, err := JalaliLeap(jy); err != nil || got != want {
			t.Errorf("leap(%d) = %v, %v; want %v", jy, got, err, want)
		}
	}
	// Esfand 30 exists only in a leap year; month and day ranges are enforced.
	for _, c := range [][3]int{{1404, 12, 30}, {1405, 13, 1}, {1405, 7, 31}, {1405, 0, 1}, {1405, 1, 32}} {
		if _, err := JalaliToGregorian(c[0], c[1], c[2]); err == nil {
			t.Errorf("%v must be rejected", c)
		}
	}
	for _, jy := range []int{900, 1177, 1634, 3000} {
		if _, err := JalaliToGregorian(jy, 1, 1); err == nil {
			t.Errorf("year %d outside the supported range must be rejected", jy)
		}
	}
}

// Every Jalali day in a long span maps to a distinct, strictly increasing
// Gregorian day: this catches off-by-one and month-length errors that a few
// spot anchors would miss.
func TestJalaliRoundTripIsMonotonicAndGapless(t *testing.T) {
	prev, err := JalaliToGregorian(1380, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for jy := 1380; jy <= 1410; jy++ {
		leap, err := JalaliLeap(jy)
		if err != nil {
			t.Fatal(err)
		}
		for jm := 1; jm <= 12; jm++ {
			last := 31
			switch {
			case jm == 12:
				last = 29 + boolInt(leap)
			case jm > 6:
				last = 30
			}
			for jd := 1; jd <= last; jd++ {
				if jy == 1380 && jm == 1 && jd == 1 {
					continue
				}
				g, err := JalaliToGregorian(jy, jm, jd)
				if err != nil {
					t.Fatalf("%04d-%02d-%02d: %v", jy, jm, jd, err)
				}
				if d := g.Sub(prev).Hours(); d != 24 {
					t.Fatalf("%04d-%02d-%02d follows %s by %v hours, want 24",
						jy, jm, jd, prev.Format("2006-01-02"), d)
				}
				prev = g
			}
		}
	}
}

func TestParseJalaliDateFormats(t *testing.T) {
	for _, s := range []string{"1405-07-01", "1405/07/01", "1405.07.01", "۱۴۰۵/۰۷/۰۱"} {
		got, err := ParseJalaliDate(s)
		if err != nil || got != "2026-09-23" {
			t.Errorf("%q = %q, %v; want 2026-09-23", s, got, err)
		}
	}
	for _, s := range []string{"", "1405-07", "2026-09-23x", "abcd-ef-gh"} {
		if _, err := ParseJalaliDate(s); err == nil {
			t.Errorf("%q must be rejected", s)
		}
	}
}
