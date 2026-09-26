package main

import (
	"os"
	"sort"
	"strings"
	"testing"
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
	// Every compared field agrees on all 14 instruments, except eps where one side is empty.
	for _, s := range rep.Stats {
		if s.Name == "eps" {
			continue
		}
		if s.Match != s.Both || s.Both+s.OnlyBrs+s.OnlySa != 14 {
			t.Errorf("%s: both %d match %d only-brs %d only-sa %d %v", s.Name, s.Both, s.Match, s.OnlyBrs, s.OnlySa, s.Examples)
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
	r, _ := parseRows([]byte(`[{"a":"71690.00","b":"12.5","c":"1,000","d":123,"e":"","f":null,"g":"۱۲"}]`))
	for k, want := range map[string]int64{"a": 71690, "c": 1000, "d": 123} {
		if v, ok := r[0].int(k); !ok || v != want {
			t.Errorf("%s = %d %v, want %d", k, v, ok, want)
		}
	}
	for _, k := range []string{"b", "e", "f", "g", "absent"} {
		if _, ok := r[0].int(k); ok {
			t.Errorf("%s parsed as an integer", k)
		}
	}
	for k, want := range map[string]string{"a": "decimal string", "c": "text", "d": "integer number", "e": "empty string", "f": "null", "g": "Persian digits", "x": "absent"} {
		if got := r[0].format(k); got != want {
			t.Errorf("format(%s) = %s, want %s", k, got, want)
		}
	}
}

// Without a common instrument code, rows join by symbol after folding Arabic yeh/kaf and
// spaces — but only when the symbol is unique on both sides; otherwise it is listed ambiguous.
func TestNameFallbackAndAmbiguity(t *testing.T) {
	brs, _ := parseRows([]byte(`[
	 {"id":"1","l18":"كيان","pl":5},
	 {"l18":"وبملت","pl":7},
	 {"id":"3","l18":"خودرو","pl":9}
	]`))
	sa, _ := parseRows([]byte(`[
	 {"instance_code":"99","name":"کیان","close_price":"5"},
	 {"instance_code":"10","name":"وبملت","close_price":"7"},
	 {"instance_code":"11","name":"وب ملت","close_price":"7"},
	 {"instance_code":"3","name":"خودرو","close_price":"8"}
	]`))
	rep := compare(brs, sa)
	if len(rep.Pairs) != 2 || !rep.Pairs[0].ByName || rep.Pairs[1].ByName {
		t.Fatalf("pairs %+v", rep.Pairs)
	}
	if len(rep.NameIDConflict) != 1 || rep.NameIDConflict[0] != "كيان" {
		t.Errorf("name/code conflict %v", rep.NameIDConflict)
	}
	if len(rep.Ambiguous) != 1 || rep.Ambiguous[0] != "وبملت" {
		t.Errorf("ambiguous %v", rep.Ambiguous)
	}
	for _, s := range rep.Stats {
		if s.Name == "price_last" && (s.Both != 2 || s.Match != 1 || len(s.Examples) != 1 || s.Examples[0] != "خودرو: 9 ≠ 8") {
			t.Errorf("price_last %+v", s)
		}
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
		if got := class(tc.isin, tc.sector, tc.name); got != tc.want {
			t.Errorf("class(%s) = %s, want %s", tc.isin, got, tc.want)
		}
	}
	if got := scrub(`Get "https://x/?token=a%2Bb": EOF key a+b`, "a+b"); strings.Contains(got, "a+b") || strings.Contains(got, "a%2Bb") {
		t.Errorf("scrub: %s", got)
	}
}
