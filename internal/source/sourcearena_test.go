package source

import (
	"os"
	"testing"
	"time"

	"bourse/internal/model"
)

// Contract test against the vendor's PUBLICLY DOCUMENTED sample (testdata). Only documented keys
// are present, so the unverified keys must surface as Missing and block flow metrics.
func TestParseDocumentedSample(t *testing.T) {
	body, err := os.ReadFile("testdata/sourcearena_documented_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 9, 30, 0, 0, time.UTC)
	got, err := Parse(body, now)
	if err != nil || len(got) != 1 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	s := got[0]
	if s.InsCode != "204092872752957" || s.Symbol != "شصدف" {
		t.Fatalf("keys: %+v", s)
	}
	if s.PriceLast != 33493 || s.PriceClose != 32023 || s.Volume != 123747 || s.Value != 4144658271 ||
		s.IndBuyVol != 123747 || s.InstSellVol != 16084 || s.IndBuyCount != 5 || s.TradeCount != 4726 {
		t.Fatalf("mapping: %+v", s)
	}
	// Documented sample is internally consistent: each side sums to total volume.
	if s.IndBuyVol+s.InstBuyVol != s.Volume || s.IndSellVol+s.InstSellVol != s.Volume {
		t.Fatalf("sample side sums do not match volume")
	}
	for _, f := range []string{model.FIndSellCount, model.FInstBuyCount, model.FInstSellCount, model.FBook} {
		if s.Has(f) {
			t.Fatalf("%s must be reported missing until verified", f)
		}
	}
	if !s.SourceTimeEstimated || !s.IngestTime.Equal(now) {
		t.Fatalf("time flags: %+v", s)
	}
}

func TestNumRejectsDecimalsAndGarbage(t *testing.T) {
	body := []byte(`[{"instance_code":"1","close_price":"12.5","trade_volume":"abc","trade_value":"1,000"}]`)
	got, err := Parse(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := got[0]
	if s.Has(model.FPriceLast) || s.Has(model.FVolume) {
		t.Fatalf("decimal/garbage must be missing, got %+v", s)
	}
	if !s.Has(model.FValue) || s.Value != 1000 {
		t.Fatalf("thousand separators should parse: %+v", s)
	}
}

func TestRowWithoutInsCodeSkipped(t *testing.T) {
	got, _ := Parse([]byte(`[{"name":"x"}]`), time.Now())
	if len(got) != 0 {
		t.Fatalf("rows without instance_code must be skipped")
	}
}

func TestRedact(t *testing.T) {
	if r := redact("GET https://x/?token=SECRET: timeout", "SECRET"); r != "GET https://x/?token=***: timeout" {
		t.Fatalf("redact: %s", r)
	}
}
