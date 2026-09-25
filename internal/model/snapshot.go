// Package model defines the canonical market-data contract shared by every service.
//
// Units: all prices and values are in RIAL (int64); volumes and counts are shares/people (int64).
// Cumulative fields (Volume, Value, Ind*/Inst*) are day-to-date totals as published by the source.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Level is one level of the order book.
type Level struct {
	BidPrice, BidVol, BidCount int64
	AskPrice, AskVol, AskCount int64
}

// Snapshot is one observation of one instrument at one moment.
type Snapshot struct {
	InsCode string `json:"ins_code"` // TSETMC internal code (stable key)
	Symbol  string `json:"symbol"`   // Persian ticker, display only

	Source              string    `json:"source"`
	SourceTime          time.Time `json:"source_time"`           // time reported by the source
	IngestTime          time.Time `json:"ingest_time"`           // time we received it
	SourceTimeEstimated bool      `json:"source_time_estimated"` // true when the source gave no timestamp

	PriceLast      int64 `json:"price_last"`  // آخرین معامله
	PriceClose     int64 `json:"price_close"` // قیمت پایانی
	PriceFirst     int64 `json:"price_first"`
	PriceYesterday int64 `json:"price_yesterday"`
	PriceMin       int64 `json:"price_min"`
	PriceMax       int64 `json:"price_max"`

	TradeCount int64 `json:"trade_count"`
	Volume     int64 `json:"volume"`
	Value      int64 `json:"value"`

	IndBuyVol     int64 `json:"ind_buy_vol"`
	IndSellVol    int64 `json:"ind_sell_vol"`
	InstBuyVol    int64 `json:"inst_buy_vol"`
	InstSellVol   int64 `json:"inst_sell_vol"`
	IndBuyCount   int64 `json:"ind_buy_count"`
	IndSellCount  int64 `json:"ind_sell_count"`
	InstBuyCount  int64 `json:"inst_buy_count"`
	InstSellCount int64 `json:"inst_sell_count"`

	Book []Level `json:"book,omitempty"`

	// Missing lists canonical fields the source did not provide for this snapshot.
	// A non-empty Missing means downstream metrics that need those fields MUST NOT be computed
	// (rule: incomplete data produces "data incomplete", never a neutral or estimated value).
	Missing []string `json:"missing,omitempty"`
}

// Complete reports whether no canonical field is missing.
func (s *Snapshot) Complete() bool { return len(s.Missing) == 0 }

// Has reports whether field f is present.
func (s *Snapshot) Has(f string) bool {
	for _, m := range s.Missing {
		if m == f {
			return false
		}
	}
	return true
}

// Field names used in Missing.
const (
	FPriceLast     = "price_last"
	FVolume        = "volume"
	FValue         = "value"
	FIndBuyVol     = "ind_buy_vol"
	FIndSellVol    = "ind_sell_vol"
	FIndBuyCount   = "ind_buy_count"
	FIndSellCount  = "ind_sell_count"
	FInstBuyVol    = "inst_buy_vol"
	FInstSellVol   = "inst_sell_vol"
	FInstBuyCount  = "inst_buy_count"
	FInstSellCount = "inst_sell_count"
	FBook          = "book"
)

// FlowFields are required for any real-person money-flow metric.
var FlowFields = []string{FPriceLast, FVolume, FValue, FIndBuyVol, FIndSellVol, FIndBuyCount, FIndSellCount}

// DecodeSnapshot parses one canonical snapshot and rejects payloads that would otherwise be
// silently zero-filled by encoding/json (rule 1): null or non-object JSON, an empty ins_code,
// a missing source_time, or a flow field (FlowFields) that is absent or null without being
// listed in "missing".
func DecodeSnapshot(data []byte) (Snapshot, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Snapshot{}, fmt.Errorf("not a JSON object: %w", err)
	}
	if raw == nil {
		return Snapshot{}, errors.New("null snapshot")
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return Snapshot{}, err
	}
	if s.InsCode == "" {
		return Snapshot{}, errors.New("empty ins_code")
	}
	if s.SourceTime.IsZero() {
		return Snapshot{}, errors.New("missing source_time")
	}
	for _, f := range FlowFields {
		if v, ok := raw[f]; (!ok || string(v) == "null") && s.Has(f) {
			return Snapshot{}, fmt.Errorf("field %s absent but not listed in missing", f)
		}
	}
	return s, nil
}

// RebasedPrefix marks the source of a recording re-timed to the present (collector
// REPLAY_REBASE, development only): its times are not the market's.
const RebasedPrefix = "rebase:"

// IsSynthetic reports data that is not real market data (rule 5: never shown to users or used
// in backtests): source "synthetic", a SYN* instrument code or symbol, or a re-timed recording.
func IsSynthetic(s *Snapshot) bool {
	return s.Source == "synthetic" || strings.HasPrefix(s.Source, RebasedPrefix) ||
		strings.HasPrefix(s.InsCode, "SYN") || strings.HasPrefix(s.Symbol, "SYN")
}
