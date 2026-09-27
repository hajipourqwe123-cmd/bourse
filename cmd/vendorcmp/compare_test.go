package main

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []row {
	t.Helper()
	b, err := os.ReadFile("../../internal/source/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseRows(b)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The committed fixtures hold the same trading day (2026-09-23, closed market) from both
// vendors: 18 BrsApi rows, 18 SourceArena rows, 14 instruments in both. By hand: the 4
// BrsApi-only rows are its fixed-price / issuance / block boards and the untraded …0003 row
// (سآبیک3, کیان2, تابان3, سپر4); the 4 SourceArena-only rows were picked outside the BrsApi fixture.
func TestCompareFixtures(t *testing.T) {
	rep := compare(fixture(t, "brsapi_allsymbols_type1.json"), fixture(t, "sourcearena_live_all_type0.json"))
	if len(rep.Pairs) != 14 || len(rep.OnlyBrs) != 4 || len(rep.OnlySa) != 4 || len(rep.Ambiguous) != 0 {
		t.Fatalf("pairs %d, only brsapi %d, only sourcearena %d, ambiguous %v", len(rep.Pairs), len(rep.OnlyBrs), len(rep.OnlySa), rep.Ambiguous)
	}
	var onlyBrs []string
	for _, r := range rep.OnlyBrs {
		onlyBrs = append(onlyBrs, r.text("l18"))
	}
	sort.Strings(onlyBrs)
	if strings.Join(onlyBrs, ",") != "تابان3,سآبیک3,سپر4,کیان2" {
		t.Errorf("BrsApi-only rows: %v", onlyBrs)
	}
	for _, p := range rep.Pairs {
		if p.ByName {
			t.Errorf("%s joined by name, want by instrument code", p.Brs.text("l18"))
		}
	}
	// Every pair lands in exactly one category; no field disagrees on these 14 instruments (eps:
	// SourceArena "" for funds is "nothing", BrsApi null likewise).
	for _, st := range rep.Stats {
		if sum := st.Match + st.Mismatch + st.OnlyBrs + st.OnlySa + st.Neither + st.BothBad; sum != 14 {
			t.Errorf("%s: categories sum to %d, want 14", st.Name, sum)
		}
		if st.Mismatch != 0 || st.BadBrs+st.BadSa != 0 {
			t.Errorf("%s: mismatch %d bad %d/%d %v", st.Name, st.Mismatch, st.BadBrs, st.BadSa, st.Examples)
		}
	}
	md := rep.Markdown()
	for _, want := range []string{"14 ردیف مشترک", "price_limit_max", "`daily_price_high`", "decimal string", "`real_buy_value`"} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

func TestIntFormats(t *testing.T) {
	r, _ := parseRows([]byte(`[{"a":"71690.00","b":"12.5","c":"1,000","d":123,"e":"","f":null,"g":"۱۲","h":"-","i":"+5","j":"1e3","k":"99999999999999999999","l":-7}]`))
	for k, want := range map[string]int64{"a": 71690, "c": 1000, "d": 123, "i": 5, "l": -7} {
		if v, st := r[0].parseInt(k); st != valOK || v != want {
			t.Errorf("%s = %d %v, want %d", k, v, st, want)
		}
	}
	for k, want := range map[string]valState{"b": valBad, "e": valAbsent, "f": valAbsent, "g": valBad, "h": valAbsent, "j": valBad, "k": valBad, "absent": valAbsent} {
		if _, st := r[0].parseInt(k); st != want {
			t.Errorf("%s: state %v, want %v", k, st, want)
		}
	}
	for k, want := range map[string]string{"a": "decimal string", "c": "numeric string with separators", "d": "integer number", "e": "empty string", "f": "null", "g": "Persian digits", "x": "absent"} {
		if got := r[0].format(k); got != want {
			t.Errorf("format(%s) = %s, want %s", k, got, want)
		}
	}
}

// Each pair lands in exactly one category. Seven pairs, price_last by hand:
// 5/"5" match (non-zero); 0/"0" match (zero); 9/"8" mismatch; 3/"5.5" mismatch (one side bad);
// ""/"7" only SourceArena; absent/"-" neither; overflow/"x" both bad.
func TestStatCategories(t *testing.T) {
	brs, _ := parseRows([]byte(`[{"id":"1","l18":"a","pl":5},{"id":"2","l18":"b","pl":0},{"id":"3","l18":"c","pl":9},
	 {"id":"4","l18":"d","pl":3},{"id":"5","l18":"e","pl":""},{"id":"6","l18":"f"},{"id":"7","l18":"g","pl":99999999999999999999}]`))
	sa, _ := parseRows([]byte(`[{"instance_code":"1","close_price":"5"},{"instance_code":"2","close_price":"0"},{"instance_code":"3","close_price":"8"},
	 {"instance_code":"4","close_price":"5.5"},{"instance_code":"5","close_price":"7"},{"instance_code":"6","close_price":"-"},{"instance_code":"7","close_price":"x"}]`))
	rep := compare(brs, sa)
	var s FieldStat
	for _, st := range rep.Stats {
		if st.Name == "price_last" {
			s = st
		}
	}
	got := []int{s.Both, s.Match, s.NonZeroMatch, s.Mismatch, s.OnlyBrs, s.OnlySa, s.Neither, s.BothBad, s.BadBrs, s.BadSa}
	want := []int{3, 2, 1, 2, 0, 1, 1, 1, 1, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("both/match/nonzero/mismatch/onlyB/onlyS/neither/bothbad/badB/badS = %v, want %v", got, want)
		}
	}
	if len(s.Examples) != 2 || s.Examples[0] != "c: 9 ≠ 8" || s.Examples[1] != "d: 3 ≠ 5.5" {
		t.Errorf("examples %v", s.Examples)
	}
}

// Joins: by code first (repeated codes are reported, not used); by symbol only for rows left,
// unique on both sides, with at least one side uncoded; two different codes are not joined.
func TestJoinRules(t *testing.T) {
	brs, _ := parseRows([]byte(`[
	 {"id":"1","l18":"كيان","pl":5},
	 {"l18":"وبملت","pl":7},
	 {"id":"3","l18":"خودرو","pl":9},
	 {"l18":"شپنا","pl":4},
	 {"id":"5","l18":"دوبل1"}, {"id":"5","l18":"دوبل2"},
	 {"id":"7","l18":"X"}, {"id":"8","l18":"Y"}
	]`))
	sa, _ := parseRows([]byte(`[
	 {"instance_code":"99","name":"کیان","close_price":"5"},
	 {"instance_code":"10","name":"وبملت","close_price":"7"},
	 {"instance_code":"11","name":"وب ملت","close_price":"7"},
	 {"instance_code":"3","name":"خودرو","close_price":"8"},
	 {"instance_code":"12","name":"شپنا","close_price":"4"},
	 {"instance_code":"5","name":"دوبل"},
	 {"instance_code":"8","name":"X"}
	]`))
	rep := compare(brs, sa)
	var got []string
	for _, p := range rep.Pairs {
		got = append(got, p.Brs.text("l18")+"="+p.Sa.text("instance_code")+map[bool]string{true: "/name", false: ""}[p.ByName])
	}
	sort.Strings(got)
	// خودرو and Y by code (8), شپنا by name; X is not joined to the SourceArena row X that code 8
	// already took; code 5 is repeated in BrsApi, so neither دوبل row joins.
	if strings.Join(got, ",") != "Y=8,خودرو=3,شپنا=12/name" {
		t.Errorf("pairs %v", got)
	}
	if len(rep.NameIDConflict) != 1 || rep.NameIDConflict[0] != "كيان (1 ≠ 99)" {
		t.Errorf("conflicts %v", rep.NameIDConflict)
	}
	if len(rep.Ambiguous) != 1 || rep.Ambiguous[0] != "وبملت" {
		t.Errorf("ambiguous %v", rep.Ambiguous)
	}
	if len(rep.DupCodeBrs) != 1 || rep.DupCodeBrs[0] != "5" || len(rep.DupCodeSa) != 0 {
		t.Errorf("duplicate codes %v %v", rep.DupCodeBrs, rep.DupCodeSa)
	}
	if len(rep.OnlyBrs)+len(rep.Pairs) != len(brs) || len(rep.OnlySa)+len(rep.Pairs) != len(sa) {
		t.Errorf("rows lost: %d+%d of %d, %d+%d of %d", len(rep.OnlyBrs), len(rep.Pairs), len(brs), len(rep.OnlySa), len(rep.Pairs), len(sa))
	}
	if rep.DupSymSa != 1 { // وبملت / وب ملت
		t.Errorf("SourceArena duplicate symbols %d", rep.DupSymSa)
	}
}

func TestClassAndScrub(t *testing.T) {
	for _, tc := range []struct{ isin, sector, name, want string }{
		{"IRO1FOLD0001", "27", "", "سهام بورس"},
		{"IRO1TBAN0003", "44", "", "تابلو غیرعادی (…0003)"},
		{"IRT3AVNF0001", "68", "صندوق س. آوند مفید-د", "صندوق درآمد ثابت"},
		{"IRTKSILV0001", "68", "صندوق س.بازده نقره نوا", "صندوق نقره"},
		{"IRR3ARFZ0101", "27", "", "حق تقدم"},
		{"IRB3TR3307B1", "69", "", "اوراق"},
	} {
		if got := class(tc.isin, tc.sector, tc.name, ""); got != tc.want {
			t.Errorf("class(%s) = %s, want %s", tc.isin, got, tc.want)
		}
	}
	if got := scrub(`Get "https://x/?token=a%2Bb": EOF key a+b`, "a+b"); strings.Contains(got, "a+b") || strings.Contains(got, "a%2Bb") {
		t.Errorf("scrub: %s", got)
	}
}

func TestSummarizeCountsOnly(t *testing.T) {
	brs, _ := parseRows([]byte(`[{"id":"1","l18":"الف","isin":"IRO1AAAA0001","cs_id":27,"py":100,"tvol":50},
		{"id":"2","l18":"ب","isin":"IRO1BBBB0001","cs_id":27,"py":200,"tvol":10}]`))
	sa, _ := parseRows([]byte(`[{"instance_code":"1","name":"الف","namad_code":"IRO1AAAA0001","industry_code":"27","yesterday_price":"100","trade_volume":"40"},
		{"instance_code":"2","name":"ب","namad_code":"IRO1BBBB0001","industry_code":"27","yesterday_price":"201","trade_volume":"10"}]`))
	s := summarize(compare(brs, sa), brs, sa)
	// Side sums are absent in these rows: every traded row is a SIDE_MISMATCH for both vendors.
	if s.Joined != 2 || s.Static["price_yesterday"] != 1 || s.VolumeAhead["brsapi"] != 1 || s.VolumeAhead["equal"] != 1 ||
		s.Traded["brsapi"] != 2 || s.SideMismatch["sourcearena"] != 2 {
		t.Fatalf("%+v", s)
	}
}

func TestSideMismatchAndBothZero(t *testing.T) {
	brs, _ := parseRows([]byte(`[{"id":"1","l18":"الف","tvol":100,"Buy_I_Volume":60,"Buy_N_Volume":40,"Sell_I_Volume":100,"Sell_N_Volume":0},
		{"id":"2","l18":"ب","tvol":0,"Buy_I_Volume":0,"Buy_N_Volume":0,"Sell_I_Volume":0,"Sell_N_Volume":0}]`))
	sa, _ := parseRows([]byte(`[{"instance_code":"1","name":"الف","trade_volume":"100","real_buy_volume":"60","co_buy_volume":"30","real_sell_volume":"100","co_sell_volume":"0"},
		{"instance_code":"2","name":"ب","trade_volume":"0"}]`))
	s := summarize(compare(brs, sa), brs, sa)
	if s.SideMismatch["brsapi"] != 0 || s.SideMismatch["sourcearena"] != 1 || s.Traded["sourcearena"] != 1 ||
		s.VolumeAhead["both_zero"] != 1 || s.VolumeAhead["equal"] != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestWatchBudgetFailsClosed(t *testing.T) {
	c := watchConfig{maxBrs: 90, sum: t.TempDir() + "/runs.ndjson"}
	if c.loadUsed("2026-09-27") != 0 {
		t.Fatal("no file: nothing used")
	}
	c.saveUsed("2026-09-27", 7)
	if c.loadUsed("2026-09-27") != 7 || c.loadUsed("2026-09-28") != 0 {
		t.Fatal("count must persist for the day and reset on the next")
	}
	os.WriteFile(c.budgetFile(), []byte("{bad"), 0o644)
	if c.loadUsed("2026-09-27") != 90 {
		t.Fatal("a corrupt budget file must count as the budget spent")
	}
}

func TestScorecardCounts(t *testing.T) {
	dir := t.TempDir()
	log := dir + "/runs.ndjson"
	lines := `{"kind":"run","at":"2026-09-27T10:00:00+03:30","fetch_gap_s":10,"brs_rows":10,"sa_rows":9,"joined":9,"only_brs":1,"volume_ahead":{"brsapi":3,"equal":5,"both_zero":1},"traded":{"brsapi":8,"sourcearena":8},"side_mismatch":{"brsapi":2,"sourcearena":6},"brsapi_used_today":1}
{"kind":"skip","at":"2026-09-27T10:05:00+03:30","reason":"sourcearena_stale 6m0s","brsapi_used_today":1}
{"kind":"error","at":"2026-09-27T10:10:00+03:30","reason":"brsapi: HTTP 500","brsapi_used_today":2}
{"at":"2026-09-26T10:00:00+03:30","joined":5,"volume_ahead":{"sourcearena":5}}
`
	os.WriteFile(log, []byte(lines), 0o644)
	os.WriteFile(dir+"/sa.json", []byte(`{"day":"2026-09-27","used":44}`), 0o644)
	sc, err := score("2026-09-27", log, dir+"/none.json", dir+"/sa.json", 100, 0, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	b, s := sc.V["brsapi"], sc.V["sourcearena"]
	if sc.Runs != 1 || sc.Skips != 1 || sc.Errors != 1 || b.Ahead != 3 || s.Ahead != 0 || b.SideMismatch != 2 ||
		s.SideMismatch != 6 || s.Outages != 1 || b.Errors != 1 || s.QuotaUsed != 44 || b.QuotaUsed != 1 {
		t.Fatalf("%+v brs=%+v sa=%+v", sc, *b, *s)
	}
	md := sc.Markdown()
	for _, want := range []string{"25.0٪", "75.0٪", "44 از نامعلوم"} {
		if !strings.Contains(md, want) {
			t.Errorf("scorecard lacks %q", want)
		}
	}
}
