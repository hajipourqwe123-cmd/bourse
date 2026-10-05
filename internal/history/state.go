package history

import "fmt"

type TradingState string

const (
	StateTrading         TradingState = "TRADING"
	StateHalted          TradingState = "HALTED"
	StateSuspended       TradingState = "SUSPENDED"
	StateReopened        TradingState = "REOPENED"
	StateLimitUpLocked   TradingState = "LIMIT_UP_LOCKED"
	StateLimitDownLocked TradingState = "LIMIT_DOWN_LOCKED"
	StateUnknown         TradingState = "UNKNOWN"
)

// Evidence says how a state was reached.
type Evidence string

const (
	EvidenceExplicit Evidence = "explicit" // source status or known limit price
	EvidenceInferred Evidence = "inferred" // derived from OHLCV only
	EvidenceNone     Evidence = "none"     // state is UNKNOWN
)

type StateConfig struct {
	// SuspendedMinRun is the number of consecutive zero-volume rows after which
	// an inferred halt is called a suspension. Configurable policy, not a vendor fact.
	SuspendedMinRun int
}

func DefaultStateConfig() StateConfig { return StateConfig{SuspendedMinRun: 10} }

type StateResult struct {
	State    TradingState
	Evidence Evidence
	Reason   string
}

// ClassifyDay classifies one bar. prev is the previous row's result (zero value
// for the first row) and zeroRun is the count of consecutive zero-volume rows
// ending at the previous row. Insufficient or contradictory evidence yields
// UNKNOWN; a zero-volume, zero-range row is never a price-limit lock.
func ClassifyDay(b RawBar, prev StateResult, zeroRun int, cfg StateConfig) StateResult {
	if cfg.SuspendedMinRun <= 0 {
		cfg = DefaultStateConfig()
	}
	switch b.SourceStatus {
	case "suspended":
		return StateResult{StateSuspended, EvidenceExplicit, "source status"}
	case "halted":
		return StateResult{StateHalted, EvidenceExplicit, "source status"}
	}

	if !b.Traded() {
		if b.High != 0 || b.Low != 0 {
			return StateResult{StateUnknown, EvidenceNone, "zero volume but non-zero trade range"}
		}
		if b.PrevClose > 0 && b.Close > 0 && b.Close != b.PrevClose {
			return StateResult{StateUnknown, EvidenceNone, "zero volume but close differs from base price"}
		}
		if b.Volume < 0 {
			return StateResult{StateUnknown, EvidenceNone, "negative volume"}
		}
		if zeroRun+1 >= cfg.SuspendedMinRun {
			return StateResult{StateSuspended, EvidenceInferred, fmt.Sprintf("zero-volume run >= %d", cfg.SuspendedMinRun)}
		}
		return StateResult{StateHalted, EvidenceInferred, "no trades, price unchanged"}
	}

	if b.High <= 0 || b.Low <= 0 || b.High < b.Low {
		return StateResult{StateUnknown, EvidenceNone, "volume without a coherent trade range"}
	}

	if prev.State == StateHalted || prev.State == StateSuspended {
		return StateResult{StateReopened, prev.Evidence, "first traded row after halt/suspension"}
	}

	if b.Low == b.High {
		// All trades at one price. Direction comes from that traded price, not from
		// the closing price (which may differ from every traded price).
		p := b.High
		if b.UpperLimit > 0 && p == b.UpperLimit {
			return StateResult{StateLimitUpLocked, EvidenceExplicit, "all trades at known upper limit"}
		}
		if b.LowerLimit > 0 && p == b.LowerLimit {
			return StateResult{StateLimitDownLocked, EvidenceExplicit, "all trades at known lower limit"}
		}
		if b.UpperLimit > 0 || b.LowerLimit > 0 {
			return StateResult{StateTrading, EvidenceExplicit, "single price away from known limits"}
		}
		switch {
		case b.PrevClose > 0 && p > b.PrevClose:
			return StateResult{StateLimitUpLocked, EvidenceInferred, "proxy: pmin==pmax above base price"}
		case b.PrevClose > 0 && p < b.PrevClose:
			return StateResult{StateLimitDownLocked, EvidenceInferred, "proxy: pmin==pmax below base price"}
		default:
			return StateResult{StateUnknown, EvidenceNone, "single price equal to base price or no base price"}
		}
	}
	return StateResult{StateTrading, EvidenceInferred, "volume with a price range"}
}

// ClassifySeries classifies bars in date order.
func ClassifySeries(bars []RawBar, cfg StateConfig) []StateResult {
	out := make([]StateResult, len(bars))
	var prev StateResult
	zeroRun := 0
	for i, b := range bars {
		out[i] = ClassifyDay(b, prev, zeroRun, cfg)
		if b.Traded() {
			zeroRun = 0
		} else {
			zeroRun++
		}
		prev = out[i]
	}
	return out
}
