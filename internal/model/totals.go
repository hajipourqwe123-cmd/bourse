package model

// Totals are the day-to-date totals of a snapshot that the previous-day carryover rules compare
// (DL-01 and the dashboard's «در انتظار بازنشانی منبع»; owner decisions on PR #3). One definition
// shared by the engine and the gateway.
type Totals struct {
	Volume     int64  `json:"v"`
	Value      int64  `json:"val"`
	TradeCount int64  `json:"n"`
	HasCount   bool   `json:"c"`              // trade_count was present (not in Missing)
	Seen       string `json:"seen,omitempty"` // last trading day a snapshot showed these (pruning)
}

// TotalsOf returns s's totals; false when volume or value is missing (nothing can be compared).
func TotalsOf(s *Snapshot) (Totals, bool) {
	if !s.Has(FVolume) || !s.Has(FValue) {
		return Totals{}, false
	}
	return Totals{Volume: s.Volume, Value: s.Value, TradeCount: s.TradeCount, HasCount: s.Has(FTradeCount)}, true
}

// Active reports any day activity (volume, value or a present trade count ≠ 0).
func (t Totals) Active() bool {
	return t.Volume != 0 || t.Value != 0 || (t.HasCount && t.TradeCount != 0)
}

// Same reports equal totals: volume and value, and the trade count when both sides have one.
func (t Totals) Same(o Totals) bool {
	return t.Volume == o.Volume && t.Value == o.Value && (!t.HasCount || !o.HasCount || t.TradeCount == o.TradeCount)
}
