package tkarchive

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/tehran"
)

func tehranAt(day, hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hm, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

// A verified 15:00 class and a verified 18:00 class: the same capture time is
// complete for one and partial for the other, so no single cut-off is "EOD".
const verifiedCal = `{
  "classes": {
    "fixed_income": {"rules": [{"effective_from": "2026-01-01", "days": ["sat","sun","mon","tue","wed"], "pre_open": "08:25", "open": "08:30", "close": "15:00", "verified": true}]},
    "gold": {"rules": [{"effective_from": "2026-01-01", "days": ["sat","sun","mon","tue","wed"], "pre_open": "11:45", "open": "12:00", "close": "18:00", "verified": true}]}
  }
}`

func TestSessionCompletenessIsClassAware(t *testing.T) {
	cal, err := calendar.Parse([]byte(verifiedCal))
	if err != nil {
		t.Fatal(err)
	}
	// 2026-10-05 is a Monday
	cases := []struct {
		class, hm, want string
	}{
		{"fixed_income", "15:10", SessionClosedAtCapture},
		{"gold", "15:10", PartialIntraday},
		{"gold", "18:05", SessionClosedAtCapture},
		{calendar.Unknown, "15:10", PartialIntraday}, // union closes at 18:00
	}
	for _, c := range cases {
		if got := SessionCompleteness(cal, c.class, "2026-10-05", tehranAt("2026-10-05", c.hm)); got != c.want {
			t.Errorf("%s at %s: got %s want %s", c.class, c.hm, got, c.want)
		}
	}
	if got := SessionCompleteness(cal, "gold", "2026-10-05", tehranAt("2026-10-06", "09:00")); got != PostSessionDay {
		t.Errorf("later-day capture: %s", got)
	}
}

func TestSessionUnknownWhenCalendarUnverifiedOrUnmapped(t *testing.T) {
	cal := calendar.Default() // every embedded rule is unverified
	for _, class := range []string{"stock", calendar.Unknown} {
		if got := SessionCompleteness(cal, class, "2026-10-05", tehranAt("2026-10-05", "13:49")); got != SessionUnknown {
			t.Errorf("%s: unverified session must give SESSION_UNKNOWN, got %s", class, got)
		}
	}
	if got := SessionCompleteness(cal, "stock", "2026-10-05", time.Time{}); got != SessionUnknown {
		t.Errorf("unknown capture time: %s", got)
	}
	if got := SessionCompleteness(cal, "stock", "2026-10-05", tehranAt("2026-10-04", "20:00")); got != SessionUnknown {
		t.Errorf("capture before the market day: %s", got)
	}
}

// A same-day capture stays in the archive, is not counted as a day observation,
// and an annotation line is not a duplicate item. Scores never count as usable.
func TestValidatorSessionAndTemporalGates(t *testing.T) {
	root, dl := t.TempDir(), t.TempDir()
	s, _ := NewStore(root)
	mb := `{"success":true,"date":"2026-10-04","intervals":["09:10"],"data":[{"symbol":"AAA","hotMoneyValues":{},"priceChangeValues":{},"priceLevelValues":{},"hotMoneyTotal":0}]}`
	writeDL(t, dl, "tk__hot_money_matrix__2026-10-04.json", mb)
	writeDL(t, dl, "tk__hot_money_matrix__2026-10-05.json", `{"success":true,"date":"2026-10-05","intervals":["09:10"],"data":[{"symbol":"AAA","hotMoneyValues":{},"priceChangeValues":{},"priceLevelValues":{},"hotMoneyTotal":0}]}`)
	writeDL(t, dl, "tk__symbol_score_history__2026-10-04.json", `[{"symbol":"AAA","score_date":"2026-10-04T00:00:00","total_score":1,"created_at":"2026-10-04T05:30:00Z"}]`)
	// download times: 10-04 items fetched the next day, the 10-05 item the same day
	for name, at := range map[string]time.Time{
		"tk__hot_money_matrix__2026-10-04.json":     tehranAt("2026-10-05", "11:00"),
		"tk__hot_money_matrix__2026-10-05.json":     tehranAt("2026-10-05", "13:49"),
		"tk__symbol_score_history__2026-10-04.json": tehranAt("2026-10-05", "11:00"),
	} {
		if err := os.Chtimes(filepath.Join(dl, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Ingest(s, dl); err != nil {
		t.Fatal(err)
	}
	if err := s.Annotate(DatasetMatrix, "2026-10-05", "same_day_capture", SessionUnknown); err != nil {
		t.Fatal(err)
	}
	cal := calendar.Default()
	mx, _, err := validateDataset(s, cal, DatasetMatrix, "2026-10-04", "2026-10-05")
	if err != nil {
		t.Fatal(err)
	}
	if mx.DuplicateItems != 0 || mx.SuccessfulItems != 2 {
		t.Fatalf("annotation is not a duplicate; both raw items stay: %+v", mx)
	}
	if mx.Days[0].Completeness != PostSessionDay || mx.Days[0].Class != "ok" {
		t.Fatalf("next-day capture: %+v", mx.Days[0])
	}
	if mx.Days[1].Class != "session_unknown" || mx.GenuineDays != 1 || mx.UsableDays != 1 {
		t.Fatalf("same-day capture must not count as a day observation: %+v", mx)
	}
	sc, _, _ := validateDataset(s, cal, DatasetScores, "2026-10-04", "2026-10-04")
	if sc.TemporalStatus != TemporalAmbiguous || sc.GenuineDays != 1 || sc.UsableDays != 0 || sc.Coverage.UsableBacktestCoverage != 0 {
		t.Fatalf("temporally ambiguous scores must have zero usable backtest coverage: %+v", sc.Coverage)
	}
}

// Partial grids are judged against the provider's own neighbouring days, not a
// fixed interval count.
func TestPartialIntervalGridIsRelativeToNeighbours(t *testing.T) {
	days := []DayInfo{
		{Date: "2026-04-05", Class: "ok", Intervals: 21},
		{Date: "2026-04-06", Class: "ok", Intervals: 21},
		{Date: "2026-04-07", Class: "ok", Intervals: 2},
		{Date: "2026-04-08", Class: "ok", Intervals: 21},
		{Date: "2026-04-09", Class: "low_coverage", Intervals: 21},
	}
	markPartialGrids(days)
	for i, want := range []string{"ok", "ok", "partial_interval_grid", "ok", "low_coverage"} {
		if days[i].Class != want {
			t.Errorf("%s: got %s want %s", days[i].Date, days[i].Class, want)
		}
	}
}
