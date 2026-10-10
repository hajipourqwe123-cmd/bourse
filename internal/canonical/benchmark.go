package canonical

import (
	"math"
	"sort"
)

// Provider truth benchmark (docs/provider_truth_benchmark.md).
//
// Two providers disagreeing does not tell us which is right; it bounds how much
// trust any single one deserves. The canonical source is chosen on identifier
// stability, timestamp semantics, depth and coverage, NOT on field count.
// A field present in one provider and absent in the other is counted as
// missing, never as agreement.

// FieldStats is the comparison of one field over the overlapping observations.
type FieldStats struct {
	Field           string  `json:"field"`
	Compared        int     `json:"compared"`          // both providers had a usable value
	MissingA        int     `json:"missing_a"`         // overlap days where A had no value
	MissingB        int     `json:"missing_b"`         // overlap days where B had no value
	ExactMatches    int     `json:"exact_matches"`     //
	ExactMatchRate  float64 `json:"exact_match_rate"`  //
	MeanRelError    float64 `json:"mean_rel_error"`    // mean |a-b| / max(|a|,|b|)
	MaxRelError     float64 `json:"max_rel_error"`     //
	MeanSignedError float64 `json:"mean_signed_error"` // mean (a-b)/max(|a|,|b|): systematic bias
	BiasDirection   string  `json:"bias_direction"`    // a_higher, b_higher, none
	WorstDate       string  `json:"worst_date,omitempty"`
}

// BenchmarkResult compares provider A against provider B.
type BenchmarkResult struct {
	ProviderA    string       `json:"provider_a"`
	ProviderB    string       `json:"provider_b"`
	InstrumentID string       `json:"instrument_id"`
	DatesA       int          `json:"dates_a"`
	DatesB       int          `json:"dates_b"`
	OverlapDates int          `json:"overlap_dates"`
	OnlyInA      int          `json:"only_in_a"`
	OnlyInB      int          `json:"only_in_b"`
	MissingRateA float64      `json:"missing_rate_a"` // share of union absent from A
	MissingRateB float64      `json:"missing_rate_b"`
	Fields       []FieldStats `json:"fields"`
	EarliestA    string       `json:"earliest_a"`
	EarliestB    string       `json:"earliest_b"`
	LatestA      string       `json:"latest_a"`
	LatestB      string       `json:"latest_b"`
}

// fieldOf extracts the comparable numeric fields of a bar. A zero value means
// "absent" for the flow fields, which BrsApi omits for older rows; for price
// and volume a zero on a traded day is a real (and suspicious) value, so it is
// compared rather than skipped.
func fieldOf(b DailyBar) map[string]float64 {
	return map[string]float64{
		"last_price":        b.LastPrice,
		"closing_price":     b.ClosingPrice,
		"high":              b.High,
		"low":               b.Low,
		"first_price":       b.FirstPrice,
		"volume":            b.Volume,
		"value":             b.Value,
		"trade_count":       float64(b.TradeCount),
		"real_buy_volume":   b.RealBuyVolume,
		"real_sell_volume":  b.RealSellVolume,
		"legal_buy_volume":  b.LegalBuyVolume,
		"legal_sell_volume": b.LegalSellVolume,
	}
}

var benchmarkFields = []string{
	"last_price", "closing_price", "high", "low", "first_price",
	"volume", "value", "trade_count",
	"real_buy_volume", "real_sell_volume", "legal_buy_volume", "legal_sell_volume",
}

// Benchmark compares two providers' bars for the same instrument.
func Benchmark(instrumentID, nameA, nameB string, a, b []DailyBar) BenchmarkResult {
	res := BenchmarkResult{ProviderA: nameA, ProviderB: nameB, InstrumentID: instrumentID,
		DatesA: len(a), DatesB: len(b)}
	ma, mb := byDate(a), byDate(b)
	union := map[string]bool{}
	for d := range ma {
		union[d] = true
	}
	for d := range mb {
		union[d] = true
	}
	var overlap []string
	for d := range union {
		_, inA := ma[d]
		_, inB := mb[d]
		switch {
		case inA && inB:
			overlap = append(overlap, d)
		case inA:
			res.OnlyInA++
		default:
			res.OnlyInB++
		}
	}
	sort.Strings(overlap)
	res.OverlapDates = len(overlap)
	if n := len(union); n > 0 {
		res.MissingRateA = round4(float64(n-len(ma)) / float64(n))
		res.MissingRateB = round4(float64(n-len(mb)) / float64(n))
	}
	res.EarliestA, res.LatestA = rangeOf(ma)
	res.EarliestB, res.LatestB = rangeOf(mb)

	for _, f := range benchmarkFields {
		st := FieldStats{Field: f}
		var sumAbs, sumSigned float64
		for _, d := range overlap {
			barA, barB := ma[d], mb[d]
			va, vb := fieldOf(barA)[f], fieldOf(barB)[f]
			// A field the provider does not publish, or a zero flow value
			// (BrsApi omits the breakdown on older rows), counts as missing
			// for that side and is never scored as agreement or disagreement.
			isFlow := f == "real_buy_volume" || f == "real_sell_volume" ||
				f == "legal_buy_volume" || f == "legal_sell_volume"
			missA := barA.Absent(f) || (isFlow && va == 0)
			missB := barB.Absent(f) || (isFlow && vb == 0)
			if missA {
				st.MissingA++
			}
			if missB {
				st.MissingB++
			}
			if missA || missB {
				continue
			}
			st.Compared++
			if va == vb {
				st.ExactMatches++
				continue
			}
			scale := math.Max(math.Abs(va), math.Abs(vb))
			if scale == 0 {
				continue
			}
			rel := math.Abs(va-vb) / scale
			sumAbs += rel
			sumSigned += (va - vb) / scale
			if rel > st.MaxRelError {
				st.MaxRelError, st.WorstDate = round6(rel), d
			}
		}
		if st.Compared > 0 {
			st.ExactMatchRate = round4(float64(st.ExactMatches) / float64(st.Compared))
			st.MeanRelError = round6(sumAbs / float64(st.Compared))
			st.MeanSignedError = round6(sumSigned / float64(st.Compared))
		}
		switch {
		case st.MeanSignedError > 1e-9:
			st.BiasDirection = "a_higher"
		case st.MeanSignedError < -1e-9:
			st.BiasDirection = "b_higher"
		default:
			st.BiasDirection = "none"
		}
		res.Fields = append(res.Fields, st)
	}
	return res
}

func byDate(bars []DailyBar) map[string]DailyBar {
	m := make(map[string]DailyBar, len(bars))
	for _, b := range bars {
		m[b.TradeDate] = b
	}
	return m
}

func rangeOf(m map[string]DailyBar) (earliest, latest string) {
	for d := range m {
		if earliest == "" || d < earliest {
			earliest = d
		}
		if d > latest {
			latest = d
		}
	}
	return
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }
func round6(f float64) float64 { return math.Round(f*1e6) / 1e6 }
