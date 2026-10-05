package history

import "math"

// ActionKind is the corporate-action taxonomy. Prices alone cannot tell the
// kinds apart, so price-derived events are PriceDiscontinuity until a Codal /
// event source supplies the kind.
type ActionKind string

const (
	ActionPriceDiscontinuity ActionKind = "PRICE_DISCONTINUITY"
	ActionCashDividend       ActionKind = "CASH_DIVIDEND"
	ActionCapitalIncrease    ActionKind = "CAPITAL_INCREASE"
	ActionRightsIssue        ActionKind = "RIGHTS_ISSUE"
	ActionBonusShares        ActionKind = "BONUS_SHARES"
	ActionSplit              ActionKind = "SPLIT"
	ActionRename             ActionKind = "RENAME"
	ActionMerger             ActionKind = "MERGER"
	ActionReopening          ActionKind = "REOPENING"
)

type Event struct {
	Date   string
	Kind   ActionKind
	Ratio  float64 // py(t) / pc(t-1)
	Review bool    // ratio > 1: not an expected dilution/dividend; needs manual review
}

const ratioEps = 1e-9

// DetectEvents finds rows where the base price py(t) differs from the previous
// traded row's closing price pc(t-1). Zero-volume rows with an unchanged base
// price create no event.
func DetectEvents(bars []RawBar) []Event {
	var ev []Event
	var prevClose float64
	for _, b := range bars {
		if prevClose > 0 && b.PrevClose > 0 {
			r := b.PrevClose / prevClose
			if math.Abs(r-1) > ratioEps {
				ev = append(ev, Event{Date: b.Date, Kind: ActionPriceDiscontinuity, Ratio: r, Review: r > 1})
			}
		}
		if b.Close > 0 {
			prevClose = b.Close
		}
	}
	return ev
}

// Adjust builds the analytical series anchored at the last bar (factor 1 at the
// end, earlier bars scaled by the product of later event ratios). The raw bars
// are not modified. The result type cannot be passed to the fill functions.
func Adjust(bars []RawBar) []AdjustedBar {
	out := make([]AdjustedBar, len(bars))
	factor := 1.0
	events := map[string]float64{}
	for _, e := range DetectEvents(bars) {
		events[e.Date] = e.Ratio
	}
	for i := len(bars) - 1; i >= 0; i-- {
		b := bars[i]
		out[i] = AdjustedBar{Date: b.Date, Open: b.Open * factor, Last: b.Last * factor, Close: b.Close * factor,
			High: b.High * factor, Low: b.Low * factor, Volume: b.Volume, Factor: factor}
		if r, ok := events[b.Date]; ok {
			factor *= r
		}
	}
	return out
}
