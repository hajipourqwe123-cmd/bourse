package canonical

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"bourse/internal/history"
)

// Price-layer separation (docs/canonical_daily_dataset.md, Phase 5A
// executable_price_policy):
//
//	raw_traded_price        first/last/high/low, exactly as the provider sent
//	closing_price           exchange-defined; may never have traded; NOT executable
//	adjusted_analysis_price produced by the adjustment layer; never executes
//	executable_price        produced by the fill functions from raw bars only
//
// A canonical row holds the raw layer plus provenance. Adjusted values live in
// a different type (history.AdjustedBar) and are never written back here.

// DailyBar is one instrument-day in the canonical dataset. Prices are the
// provider's raw values; nothing here is adjusted or repaired in place.
type DailyBar struct {
	TradeDate    string `json:"trade_date"`
	InstrumentID string `json:"instrument_id"`

	FirstPrice    float64 `json:"first_price"`
	LastPrice     float64 `json:"last_price"`
	ClosingPrice  float64 `json:"closing_price"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	PreviousClose float64 `json:"previous_close"` // py: the day's official base price

	Volume     float64 `json:"volume"`
	Value      float64 `json:"value"`
	TradeCount int     `json:"trade_count"`

	RealBuyVolume   float64 `json:"real_buy_volume"`
	RealSellVolume  float64 `json:"real_sell_volume"`
	LegalBuyVolume  float64 `json:"legal_buy_volume"`
	LegalSellVolume float64 `json:"legal_sell_volume"`
	RealBuyCount    int     `json:"real_buy_count"`
	RealSellCount   int     `json:"real_sell_count"`
	LegalBuyCount   int     `json:"legal_buy_count"`
	LegalSellCount  int     `json:"legal_sell_count"`

	// SharesOutstanding and BaseVolume are point-in-time attributes. BrsApi
	// History does not carry them, so for historical rows they stay 0 with
	// quality code QMissingPointInTime rather than being backfilled from today.
	SharesOutstanding float64 `json:"shares_outstanding"`
	BaseVolume        float64 `json:"base_volume"`

	TradingState    string `json:"trading_state"`
	StateEvidence   string `json:"state_evidence"`
	Source          string `json:"source"`
	SourceTimestamp string `json:"source_timestamp"`
	// RequestedDate and ServedDate stay separate: a provider may answer a
	// request for one day with another day's data (MI-01b, 102 of 248 cases).
	RequestedDate string   `json:"requested_date"`
	ServedDate    string   `json:"served_date"`
	QualityCodes  []string `json:"quality_codes,omitempty"`
	RawSHA256     string   `json:"raw_sha256,omitempty"`
	// AbsentFields names canonical fields this provider does not publish at
	// all. "Not published" is a contract fact and must not be read as a zero
	// value or counted as provider disagreement.
	AbsentFields []string `json:"absent_fields,omitempty"`
}

// Absent reports whether the provider does not publish a canonical field.
func (d DailyBar) Absent(field string) bool {
	for _, f := range d.AbsentFields {
		if f == field {
			return true
		}
	}
	return false
}

// RawBar projects the canonical row onto the Phase 5A raw-bar type used by the
// state, fill and adjustment functions.
func (d DailyBar) RawBar() history.RawBar {
	return history.RawBar{
		Date: d.TradeDate, PrevClose: d.PreviousClose, Open: d.FirstPrice, Last: d.LastPrice,
		Close: d.ClosingPrice, High: d.High, Low: d.Low,
		Volume: d.Volume, Value: d.Value, Count: d.TradeCount,
	}
}

// brsapiType0Row is one row of BrsApi Tsetmc/History.php?type=0. Numeric fields
// arrive as int or float depending on the row, hence json.Number.
type brsapiType0Row struct {
	Date string      `json:"date"` // Jalali
	Time string      `json:"time"`
	TNo  json.Number `json:"tno"`
	TVol json.Number `json:"tvol"`
	TVal json.Number `json:"tval"`
	PMin json.Number `json:"pmin"`
	PMax json.Number `json:"pmax"`
	PY   json.Number `json:"py"`
	PF   json.Number `json:"pf"`
	PL   json.Number `json:"pl"`
	PC   json.Number `json:"pc"`
}

// brsapiType1Row is one row of type=1 (real/legal breakdown).
type brsapiType1Row struct {
	Date       string      `json:"date"`
	BuyCountI  json.Number `json:"Buy_CountI"`
	BuyCountN  json.Number `json:"Buy_CountN"`
	SellCountI json.Number `json:"Sell_CountI"`
	SellCountN json.Number `json:"Sell_CountN"`
	BuyIVolume json.Number `json:"Buy_I_Volume"`
	BuyNVolume json.Number `json:"Buy_N_Volume"`
	SelIVolume json.Number `json:"Sell_I_Volume"`
	SelNVolume json.Number `json:"Sell_N_Volume"`
}

func num(n json.Number) float64 {
	f, err := n.Float64()
	if err != nil {
		return 0
	}
	return f
}

// NormalizeBrsAPIHistory converts raw BrsApi history bodies into canonical rows
// for one instrument. type1 may be nil. Rows are returned oldest first.
//
// It normalizes, it does not repair: a row whose Jalali date cannot be parsed
// is reported as an error rather than dropped or guessed.
func NormalizeBrsAPIHistory(instrumentID, source string, type0, type1 []byte) ([]DailyBar, []string, error) {
	var t0 []brsapiType0Row
	if err := json.Unmarshal(type0, &t0); err != nil {
		return nil, nil, fmt.Errorf("type=0 body: %w", err)
	}
	flows := map[string]brsapiType1Row{}
	if len(type1) > 0 {
		var t1 []brsapiType1Row
		if err := json.Unmarshal(type1, &t1); err != nil {
			return nil, nil, fmt.Errorf("type=1 body: %w", err)
		}
		for _, r := range t1 {
			iso, err := ParseJalaliDate(r.Date)
			if err != nil {
				continue
			}
			flows[iso] = r
		}
	}
	var problems []string
	out := make([]DailyBar, 0, len(t0))
	for _, r := range t0 {
		iso, err := ParseJalaliDate(r.Date)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: unparsable provider date %q", instrumentID, r.Date))
			continue
		}
		d := DailyBar{
			TradeDate: iso, InstrumentID: instrumentID,
			FirstPrice: num(r.PF), LastPrice: num(r.PL), ClosingPrice: num(r.PC),
			High: num(r.PMax), Low: num(r.PMin), PreviousClose: num(r.PY),
			Volume: num(r.TVol), Value: num(r.TVal), TradeCount: int(num(r.TNo)),
			Source: source, SourceTimestamp: iso + "T" + nonEmpty(r.Time, "00:00:00") + "+03:30",
			RequestedDate: iso, ServedDate: iso,
			QualityCodes: []string{QMissingPointInTime}, // shares_outstanding/base_volume absent from History
		}
		if f, ok := flows[iso]; ok {
			d.RealBuyVolume, d.LegalBuyVolume = num(f.BuyIVolume), num(f.BuyNVolume)
			d.RealSellVolume, d.LegalSellVolume = num(f.SelIVolume), num(f.SelNVolume)
			d.RealBuyCount, d.LegalBuyCount = int(num(f.BuyCountI)), int(num(f.BuyCountN))
			d.RealSellCount, d.LegalSellCount = int(num(f.SellCountI)), int(num(f.SellCountN))
		} else if len(type1) > 0 {
			d.QualityCodes = append(d.QualityCodes, QMissingRealLegal)
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TradeDate < out[j].TradeDate })
	return out, problems, nil
}

// flexNum accepts a JSON number OR a quoted number. SourceArena mixes both
// within a single response (observed: trade_volume quoted on some rows and
// bare on others), so a fixed type would reject valid payloads.
type flexNum struct {
	Set   bool
	Value float64
	Raw   string
}

func (f *flexNum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` || s == "" {
		return nil
	}
	s = strings.Trim(s, `"`)
	f.Raw = s
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("value %q is not a number", s)
	}
	f.Set, f.Value = true, v
	return nil
}

// sourceArenaRow is one row of the SourceArena daily-history response. It
// carries no last price, traded value, trade count or real/legal breakdown.
type sourceArenaRow struct {
	Date     string  `json:"date"` // Jalali, "1405/06/30"
	Close    flexNum `json:"close_price"`
	First    flexNum `json:"first_price"`
	Highest  flexNum `json:"highest_price"`
	Lowest   flexNum `json:"lowest_price"`
	TradeVol flexNum `json:"trade_volume"`
}

// sourceArenaAbsent are the canonical fields SourceArena daily history omits.
//
// closing_price is on this list deliberately. The response's "close_price"
// field is NOT the exchange's official closing price: benchmarked against
// BrsApi over the overlapping window it equalled the last traded price in
// 22 of 22 rows and the official close in only 4 (the days the two coincide).
// It is therefore mapped to last_price, and SourceArena is treated as having
// no official close. Mapping it by name would silently corrupt every
// close-based analytic (docs/provider_truth_benchmark.md).
var sourceArenaAbsent = []string{"closing_price", "value", "trade_count",
	"real_buy_volume", "real_sell_volume", "legal_buy_volume", "legal_sell_volume"}

// NormalizeSourceArenaHistory converts a SourceArena daily-history body into
// canonical rows. Rows are decoded individually so one unusable row is
// reported and skipped instead of discarding an otherwise valid response; only
// a body that is not a JSON array is an error.
func NormalizeSourceArenaHistory(instrumentID, source string, body []byte) ([]DailyBar, []string, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		return nil, nil, fmt.Errorf("sourcearena body: %w", err)
	}
	var problems []string
	out := make([]DailyBar, 0, len(raws))
	for i, rawRow := range raws {
		var r sourceArenaRow
		if err := json.Unmarshal(rawRow, &r); err != nil {
			problems = append(problems, fmt.Sprintf("%s: row %d unusable: %v", instrumentID, i, err))
			continue
		}
		iso, err := ParseJalaliDate(r.Date)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: unparsable provider date %q", instrumentID, r.Date))
			continue
		}
		// A quoted number and a bare number are both accepted, but a field
		// that is absent stays zero and is reported, never guessed.
		for name, f := range map[string]flexNum{"close": r.Close, "first": r.First,
			"high": r.Highest, "low": r.Lowest, "volume": r.TradeVol} {
			if !f.Set {
				problems = append(problems, fmt.Sprintf("%s %s: %s absent from the response", instrumentID, iso, name))
			}
		}
		out = append(out, DailyBar{
			TradeDate: iso, InstrumentID: instrumentID,
			// "close_price" carries the last traded price: see sourceArenaAbsent.
			FirstPrice: r.First.Value, LastPrice: r.Close.Value,
			High: r.Highest.Value, Low: r.Lowest.Value, Volume: r.TradeVol.Value,
			Source: source, SourceTimestamp: iso, RequestedDate: iso, ServedDate: iso,
			AbsentFields: sourceArenaAbsent,
			QualityCodes: []string{QMissingPointInTime},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TradeDate < out[j].TradeDate })
	return out, problems, nil
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ClassifyStates fills TradingState/StateEvidence using the Phase 5A model.
// Bars must be for one instrument, oldest first.
func ClassifyStates(bars []DailyBar) {
	raw := make([]history.RawBar, len(bars))
	for i, b := range bars {
		raw[i] = b.RawBar()
	}
	for i, st := range history.ClassifySeries(raw, history.DefaultStateConfig()) {
		bars[i].TradingState = string(st.State)
		bars[i].StateEvidence = string(st.Evidence)
	}
}
