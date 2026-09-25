// Package quality holds the data-quality rules applied to every snapshot before any metric is computed.
package quality

import (
	"fmt"
	"time"

	"bourse/internal/model"
)

// Issue codes. Every code is documented in docs/data-quality.md.
const (
	Stale              = "STALE"               // ingest − source time above threshold
	Incomplete         = "INCOMPLETE"          // a field required by flow metrics is missing
	OutOfOrder         = "OUT_OF_ORDER"        // source time not after the previous snapshot
	CumulativeDecrease = "CUMULATIVE_DECREASE" // a day-to-date total went down within the same day
	SideMismatch       = "SIDE_MISMATCH"       // buy-side (or sell-side) volume ≠ total volume delta
	TimeEstimated      = "SOURCE_TIME_ESTIMATED"
)

func issue(s *model.Snapshot, code, detail string) model.QualityIssue {
	return model.QualityIssue{InsCode: s.InsCode, Code: code, Detail: detail, At: s.IngestTime}
}

// CheckSingle applies rules that need only the current snapshot.
func CheckSingle(s *model.Snapshot, staleAfter time.Duration) []model.QualityIssue {
	var out []model.QualityIssue
	if s.SourceTimeEstimated {
		out = append(out, issue(s, TimeEstimated, "source provided no timestamp; ingest time used"))
	} else if lag := s.IngestTime.Sub(s.SourceTime); lag > staleAfter {
		out = append(out, issue(s, Stale, fmt.Sprintf("lag %s > %s", lag.Round(time.Second), staleAfter)))
	}
	var miss []string
	for _, f := range model.FlowFields {
		if !s.Has(f) {
			miss = append(miss, f)
		}
	}
	if len(miss) > 0 {
		out = append(out, issue(s, Incomplete, fmt.Sprintf("missing %v", miss)))
	}
	return out
}

// Delta is the change between two same-day snapshots of one instrument.
type Delta struct {
	Volume, Value, Trades                          int64
	IndBuyVol, IndSellVol, InstBuyVol, InstSellVol int64
	IndBuyCount, IndSellCount                      int64
}

// Diff computes cur − prev and returns issues when the pair is not usable.
// ok=false means the pair MUST NOT feed any metric.
func Diff(prev, cur *model.Snapshot) (d Delta, issues []model.QualityIssue, ok bool) {
	if !cur.SourceTime.After(prev.SourceTime) {
		return d, []model.QualityIssue{issue(cur, OutOfOrder,
			fmt.Sprintf("source time %s not after %s", cur.SourceTime.Format(time.RFC3339), prev.SourceTime.Format(time.RFC3339)))}, false
	}
	d = Delta{
		Volume: cur.Volume - prev.Volume, Value: cur.Value - prev.Value, Trades: cur.TradeCount - prev.TradeCount,
		IndBuyVol: cur.IndBuyVol - prev.IndBuyVol, IndSellVol: cur.IndSellVol - prev.IndSellVol,
		InstBuyVol: cur.InstBuyVol - prev.InstBuyVol, InstSellVol: cur.InstSellVol - prev.InstSellVol,
		IndBuyCount: cur.IndBuyCount - prev.IndBuyCount, IndSellCount: cur.IndSellCount - prev.IndSellCount,
	}
	for name, v := range map[string]int64{
		"volume": d.Volume, "value": d.Value, "trades": d.Trades,
		"ind_buy_vol": d.IndBuyVol, "ind_sell_vol": d.IndSellVol, "inst_buy_vol": d.InstBuyVol, "inst_sell_vol": d.InstSellVol,
		"ind_buy_count": d.IndBuyCount, "ind_sell_count": d.IndSellCount,
	} {
		if v < 0 {
			issues = append(issues, issue(cur, CumulativeDecrease, fmt.Sprintf("%s decreased by %d", name, -v)))
		}
	}
	if len(issues) > 0 {
		return d, issues, false
	}
	// On TSE every traded share has exactly one buyer and one seller, so each side must sum to total volume.
	if cur.Has(model.FInstBuyVol) && cur.Has(model.FInstSellVol) {
		if d.IndBuyVol+d.InstBuyVol != d.Volume || d.IndSellVol+d.InstSellVol != d.Volume {
			return d, []model.QualityIssue{issue(cur, SideMismatch, fmt.Sprintf(
				"buy %d+%d, sell %d+%d, volume %d", d.IndBuyVol, d.InstBuyVol, d.IndSellVol, d.InstSellVol, d.Volume))}, false
		}
	}
	return d, nil, true
}
