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

// Contract test against 18 rows of ONE live all-symbols response (2026-09-26 07:00 Tehran,
// market closed, data of 2026-09-23), recorded without the token. The four keys that were
// unverified (real_sell_count, co_buy_count, co_sell_count, yesterday_price) are present on
// every row; numbers arrive as numeric strings. Expected values copied by hand; فولاد is the
// same instrument as in the BrsApi fixture and agrees with it field by field.
func TestParseLiveSample(t *testing.T) {
	body, err := os.ReadFile("testdata/sourcearena_live_all_type0.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 3, 30, 0, 0, time.UTC)
	got, err := Parse(body, now)
	if err != nil || len(got) != 18 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	by := map[string]model.Snapshot{}
	for _, s := range got {
		by[s.Symbol] = s
		for _, f := range model.FlowFields {
			if !s.Has(f) {
				t.Errorf("%s: flow field %s missing: %v", s.Symbol, f, s.Missing)
			}
		}
		if s.IndBuyVol+s.InstBuyVol != s.Volume || s.IndSellVol+s.InstSellVol != s.Volume {
			t.Errorf("%s: sides do not sum to the volume", s.Symbol)
		}
	}
	f := by["فولاد"]
	if f.InsCode != "46348559193224090" || f.PriceLast != 3350 || f.PriceClose != 3350 || f.PriceYesterday != 3260 ||
		f.PriceMin != 3280 || f.PriceMax != 3350 || f.TradeCount != 21174 || f.Volume != 2210827100 || f.Value != 7398241941410 ||
		f.IndBuyVol != 1345256558 || f.InstBuyVol != 865570542 || f.IndSellVol != 1703245171 || f.InstSellVol != 507581929 ||
		f.IndBuyCount != 2232 || f.InstBuyCount != 24 || f.IndSellCount != 4728 || f.InstSellCount != 27 {
		t.Fatalf("فولاد mapping: %+v", f)
	}
	// Book and price limits are in the payload but not mapped yet: reported missing, never zero.
	if f.Has(model.FBook) || f.Has(model.FPriceLimits) || !f.SourceTimeEstimated {
		t.Errorf("فولاد flags: %v", f.Missing)
	}
	// A suspended instrument and one with no trade date still parse (by instance_code).
	if s := by["حفارس"]; s.InsCode == "" {
		t.Error("suspended row dropped")
	}
	if p := by["پویا"]; p.InsCode != "48970598895465763" || p.Volume != 0 || !p.Has(model.FVolume) || p.PriceLast != 653121 {
		t.Errorf("no-trade row: %+v", p)
	}
}
