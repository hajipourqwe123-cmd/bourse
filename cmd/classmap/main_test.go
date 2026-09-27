package main

import (
	"encoding/json"
	"os"
	"testing"

	"bourse/internal/calendar"
)

// The recorded SourceArena contract fixture maps to a valid, provisional calendar.
func TestBuildFromFixture(t *testing.T) {
	b, err := os.ReadFile("../../internal/source/testdata/sourcearena_live_all_type0.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	m, st := build(rows)
	if len(m) == 0 || st.rows != len(rows) {
		t.Fatalf("mapped %d of %d", len(m), st.rows)
	}
	n := 0
	for _, v := range st.byClass {
		n += v
	}
	if n != len(rows) {
		t.Fatalf("class counts %d != rows %d", n, len(rows))
	}
	out, err := provisional(calendar.EmbeddedJSON(), m)
	if err != nil {
		t.Fatal(err)
	}
	cal, err := calendar.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if !cal.ClassMapProvisional() || cal.Instruments() != len(m) || cal.Mapped("SYNTHETIC0001") {
		t.Fatalf("provisional=%v n=%d", cal.ClassMapProvisional(), cal.Instruments())
	}
}

func TestDuplicateCodesNeverMapped(t *testing.T) {
	row := func(code, isin string) map[string]any {
		return map[string]any{"instance_code": code, "namad_code": isin, "industry_code": "27", "name": "x"}
	}
	// Three rows share code 1 (the first has no class): none may be mapped; code 2 is fine.
	m, st := build([]map[string]any{row("1", "IRE9XXXX0001"), row("1", "IRO1AAAA0001"), row("1", "IRO1AAAA0001"), row("2", "IRO1BBBB0001")})
	if _, ok := m["1"]; ok || m["2"] != "stock" || len(st.dups) != 1 {
		t.Fatalf("m=%v dups=%v", m, st.dups)
	}
}
