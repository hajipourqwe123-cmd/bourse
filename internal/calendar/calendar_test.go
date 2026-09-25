package calendar

import (
	"strings"
	"testing"
	"time"

	"bourse/internal/tehran"
)

func at(day, hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", day+" "+hm, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func hm(t time.Time) string { return t.In(tehran.Loc).Format("15:04") }

// 2026-09-26 is a Saturday, 2026-10-01 a Thursday, 2026-10-02 a Friday.
func TestEmbeddedCalendar(t *testing.T) {
	c := Default()
	if c.Unverified() < 6 {
		t.Fatalf("every owner-provided rule must be marked unverified, got %d", c.Unverified())
	}
	cases := []struct {
		class, pre, open, close string
	}{
		{"stock", "08:45", "09:00", "12:30"},
		{"equity_etf", "08:45", "09:00", "12:30"},
		{"fixed_income", "08:25", "08:30", "15:00"},
		{"gold", "11:45", "12:00", "18:00"},
		{"silver", "11:45", "12:00", "18:00"},
		{"other_fund", "08:45", "09:00", "12:30"},
		{Unknown, "08:25", "08:30", "18:00"}, // union of all classes
	}
	for _, cs := range cases {
		s, ok := c.ClassSession(cs.class, at("2026-09-26", "10:00:00"))
		if !ok || hm(s.PreOpen) != cs.pre || hm(s.Open) != cs.open || hm(s.Close) != cs.close || s.Day != "2026-09-26" || s.Verified {
			t.Errorf("%s: %+v ok=%v, want %s/%s/%s unverified", cs.class, s, ok, cs.pre, cs.open, cs.close)
		}
		for _, closed := range []string{"2026-10-01", "2026-10-02"} {
			if _, ok := c.ClassSession(cs.class, at(closed, "10:00:00")); ok {
				t.Errorf("%s open on %s", cs.class, closed)
			}
		}
	}
	if _, ok := c.Union(at("2026-10-01", "10:00:00")); ok {
		t.Error("union open on a Thursday")
	}
	if c.Class("46348559193224090") != Unknown || c.Mapped("46348559193224090") {
		t.Error("unmapped instrument must be unknown")
	}
	if c.Class("SYNTHETICG001") != "gold" {
		t.Error("synthetic gold fund mapping")
	}
}

const twoRules = `{
  "classes": {
    "gold": {"rules": [
      {"effective_from": "2025-01-01", "days": ["sat","sun","mon","tue","wed"], "pre_open": "12:45", "open": "13:00", "close": "17:00", "verified": false},
      {"effective_from": "2026-03-21", "days": ["sat","sun","mon","tue","wed"], "pre_open": "11:45", "open": "12:00", "close": "18:00", "verified": true}
    ]},
    "stock": {"rules": [
      {"effective_from": "2025-01-01", "days": ["sat","sun","mon","tue","wed"], "pre_open": "08:45", "open": "09:00", "close": "12:30", "verified": false}
    ]}
  },
  "instruments": {"IRXGOLD": "gold"},
  "holidays": [{"date": "2026-09-27", "name": "test holiday"}]
}`

// A backtest of an old day uses the rule in force on that day.
func TestDatedRulesAndHolidays(t *testing.T) {
	c, err := Parse([]byte(twoRules))
	if err != nil {
		t.Fatal(err)
	}
	old, ok := c.Session("IRXGOLD", at("2026-03-18", "14:00:00")) // Wednesday before the change
	if !ok || hm(old.Open) != "13:00" || hm(old.Close) != "17:00" || old.Verified {
		t.Errorf("before change: %+v", old)
	}
	cur, ok := c.Session("IRXGOLD", at("2026-03-21", "14:00:00")) // the change day (Saturday)
	if !ok || hm(cur.Open) != "12:00" || hm(cur.Close) != "18:00" || !cur.Verified {
		t.Errorf("from change: %+v", cur)
	}
	if _, ok := c.Session("IRXGOLD", at("2024-12-31", "14:00:00")); ok {
		t.Error("session before the first rule")
	}
	if _, ok := c.Session("IRXGOLD", at("2026-09-27", "14:00:00")); ok {
		t.Error("session on a holiday")
	}
	if _, ok := c.Union(at("2026-09-27", "14:00:00")); ok {
		t.Error("union on a holiday")
	}
	if c.Unverified() != 3 { // two unverified rules + one unverified holiday
		t.Errorf("unverified %d", c.Unverified())
	}
	u, _ := c.Union(at("2026-09-26", "10:00:00"))
	if hm(u.PreOpen) != "08:45" || hm(u.Close) != "18:00" {
		t.Errorf("union %+v", u)
	}
	w := c.WithInstruments(map[string]string{"IRXSTK": "stock"})
	if w.Class("IRXSTK") != "stock" || c.Class("IRXSTK") != Unknown || w.Class("IRXGOLD") != "gold" {
		t.Error("WithInstruments must copy")
	}
}

func TestSessionPredicates(t *testing.T) {
	s, _ := Default().ClassSession("gold", at("2026-09-26", "13:00:00"))
	for hms, want := range map[string][2]bool{ // Contains, Trading
		"11:44:59": {false, false}, "11:45:00": {true, false}, "11:59:55": {true, false},
		"12:00:00": {true, true}, "17:59:59": {true, true}, "18:00:00": {false, false},
	} {
		tt := at("2026-09-26", hms)
		if s.Contains(tt) != want[0] || s.Trading(tt) != want[1] {
			t.Errorf("%s: contains=%v trading=%v, want %v", hms, s.Contains(tt), s.Trading(tt), want)
		}
	}
}

// Windows are aligned to the instrument's own open.
func TestWindowStart(t *testing.T) {
	fi, _ := Default().ClassSession("fixed_income", at("2026-09-26", "09:00:00"))
	odd := Session{Open: at("2026-09-26", "09:05:00")}
	for _, c := range []struct {
		s       Session
		t, want string
	}{
		{fi, "08:30:00", "08:30"}, {fi, "08:39:59", "08:30"}, {fi, "08:40:00", "08:40"}, {fi, "14:59:59", "14:50"},
		{fi, "08:27:00", "08:20"}, // pre-open counts backwards from the open
		{odd, "09:14:59", "09:05"}, {odd, "09:15:00", "09:15"}, {odd, "09:04:00", "08:55"},
	} {
		if got := hm(WindowStart(c.s, at("2026-09-26", c.t))); got != c.want {
			t.Errorf("open %s, t %s: %s, want %s", hm(c.s.Open), c.t, got, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for name, js := range map[string]string{
		"no classes":       `{"classes": {}}`,
		"reserved name":    `{"classes": {"unknown": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"open after close": `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "11:00", "close": "10:00", "verified": false}]}}}`,
		"pre after open":   `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "09:30", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"bad day":          `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["saturday"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"bad time":         `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "8", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"duplicate date":   `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}, {"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"unmapped class":   `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}, "instruments": {"A": "y"}}`,
		"bad default":      `{"default_class": "y", "classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"typo top key":     `{"instrument": {}, "classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}}`,
		"typo verified":    `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verfied": true}]}}}`,
		"missing verified": `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00"}]}}}`,
		"bad holiday":      `{"classes": {"x": {"rules": [{"effective_from": "2025-01-01", "days": ["sat"], "pre_open": "08:00", "open": "09:00", "close": "10:00", "verified": false}]}}, "holidays": [{"date": "1405/01/01"}]}`,
	} {
		if _, err := Parse([]byte(js)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.HasPrefix(err.Error(), "calendar: ") {
			t.Errorf("%s: error %q not prefixed", name, err)
		}
	}
}

func TestOpens(t *testing.T) {
	var got []string
	for _, o := range Default().Opens(at("2026-09-26", "10:00:00")) {
		got = append(got, hm(o))
	}
	if strings.Join(got, ",") != "08:30,09:00,12:00" {
		t.Fatalf("opens %v", got)
	}
	if len(Default().Opens(at("2026-10-01", "10:00:00"))) != 0 {
		t.Fatal("opens on a Thursday")
	}
}

func TestSpanAndHolidays(t *testing.T) {
	if got := Default().MaxDailySpan(); got != 9*time.Hour+35*time.Minute {
		t.Fatalf("max span %s, want 9h35m (08:25–18:00)", got)
	}
	c, _ := Parse([]byte(twoRules))
	if n := c.HolidaysBetween(at("2026-09-01", "00:00:00"), at("2026-12-31", "00:00:00")); n != 1 {
		t.Fatalf("holidays %d", n)
	}
	if Default().HolidaysBetween(at("2026-01-01", "00:00:00"), at("2027-12-31", "00:00:00")) != 0 {
		t.Fatal("embedded calendar gained holidays: update docs/sessions.md")
	}
}
