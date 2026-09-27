package source

import (
	"os"
	"slices"
	"testing"
	"time"
)

func TestParseSAMarketSample(t *testing.T) {
	b, err := os.ReadFile("testdata/sourcearena_market_bourse.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 5, 48, 50, 0, time.UTC)
	ix, err := ParseSAMarket(b, now)
	if err != nil {
		t.Fatal(err)
	}
	// "7,159,333.68", "-97708.82", "1,931,904.92", "-7116.21"
	if ix.Index != IndexTotal || ix.ValueMilli != 7_159_333_680 || ix.ChangeMilli != -97_708_820 ||
		ix.EqualWeightMilli != 1_931_904_920 || ix.EqualWeightChangeMilli != -7_116_210 || ix.State != "open" {
		t.Fatalf("%+v", ix)
	}
	if !ix.SourceTimeEstimated || !ix.SourceTime.Equal(now) {
		t.Error("SourceArena sends no time: the source time must be the ingest time, flagged estimated")
	}
	if _, err := ParseSAMarket([]byte(`{"Error":"daily request limit reached"}`), now); err == nil {
		t.Error("an error object must not parse as an index")
	}
}

func TestParseBrsIndexSample(t *testing.T) {
	b, err := os.ReadFile("testdata/brsapi_index_type1.json")
	if err != nil {
		t.Fatal(err)
	}
	ix, err := ParseBrsIndex(b, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// index 7257043.42, change 89637.91, equal weight 1939020.33 (+16706.95); 1405-07-01 17:15:36.
	if ix.ValueMilli != 7_257_043_420 || ix.ChangeMilli != 89_637_910 || ix.EqualWeightMilli != 1_939_020_330 ||
		ix.EqualWeightChangeMilli != 16_706_950 {
		t.Fatalf("%+v", ix)
	}
	if ix.SourceTimeEstimated || ix.SourceTime.Format("2006-01-02 15:04:05") != "2026-09-23 17:15:36" {
		t.Errorf("BrsApi sends a Jalali date and time: %v est=%v", ix.SourceTime, ix.SourceTimeEstimated)
	}
}

func TestMilli(t *testing.T) {
	for in, want := range map[string]int64{`"7,159,333.68"`: 7_159_333_680, `-97708.82`: -97_708_820, `"12"`: 12_000, `0.5`: 500} {
		if got, ok := milli([]byte(in)); !ok || got != want {
			t.Errorf("milli(%s) = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{`"1.2345"`, `""`, `null`, `"2.1e17"`, `"abc"`} {
		if _, ok := milli([]byte(in)); ok {
			t.Errorf("milli(%s) accepted", in)
		}
	}
	ix, _ := ParseSAMarket([]byte(`{"bourse":{"index":"1,000.00","index_change":"1"}}`), time.Now())
	if !slices.Contains(ix.Missing, "equal_weight") {
		t.Error("absent equal-weight index must be missing, not 0")
	}
}
