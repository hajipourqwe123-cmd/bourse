package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

// IndexTotal is the canonical name of the Tehran Stock Exchange total index.
const IndexTotal = "bourse_total"

// ParseSAMarket parses SourceArena's market=market_bourse payload (sample:
// testdata/sourcearena_market_bourse.json). Levels are strings with thousands separators
// ("7,159,333.68"); the payload has NO time, so SourceTime = ingest and SourceTimeEstimated.
func ParseSAMarket(body []byte, ingest time.Time) (model.IndexSnapshot, error) {
	var p struct {
		Bourse map[string]json.RawMessage `json:"bourse"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Bourse == nil {
		return model.IndexSnapshot{}, fmt.Errorf("sourcearena: market payload has no \"bourse\" object")
	}
	ix := model.IndexSnapshot{Index: IndexTotal, Source: "sourcearena", SourceTime: ingest, IngestTime: ingest,
		SourceTimeEstimated: true, State: str(p.Bourse, "state")}
	var ok bool
	if ix.ValueMilli, ok = milli(p.Bourse["index"]); !ok || ix.ValueMilli <= 0 {
		return model.IndexSnapshot{}, fmt.Errorf("sourcearena: market: index level missing or unparseable")
	}
	if ix.ChangeMilli, ok = milli(p.Bourse["index_change"]); !ok {
		return model.IndexSnapshot{}, fmt.Errorf("sourcearena: market: index change missing or unparseable")
	}
	ew, ok1 := milli(p.Bourse["index_h"])
	ewc, ok2 := milli(p.Bourse["index_h_change"])
	if ok1 && ok2 && ew > 0 {
		ix.EqualWeightMilli, ix.EqualWeightChangeMilli = ew, ewc
	} else {
		ix.Missing = append(ix.Missing, "equal_weight")
	}
	return ix, nil
}

// ParseBrsIndex parses BrsApi's Tsetmc/Index.php?type=1 payload (sample:
// testdata/brsapi_index_type1.json). It HAS a source time: date (Jalali, "1405-07-01") and time.
func ParseBrsIndex(body []byte, ingest time.Time) (model.IndexSnapshot, error) {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(body, &p); err != nil {
		return model.IndexSnapshot{}, fmt.Errorf("brsapi: index payload is not an object")
	}
	ix := model.IndexSnapshot{Index: IndexTotal, Source: "brsapi", IngestTime: ingest, State: str(p, "state")}
	var ok bool
	if ix.ValueMilli, ok = milli(p["index"]); !ok || ix.ValueMilli <= 0 {
		return model.IndexSnapshot{}, fmt.Errorf("brsapi: index level missing or unparseable")
	}
	if ix.ChangeMilli, ok = milli(p["index_change"]); !ok {
		return model.IndexSnapshot{}, fmt.Errorf("brsapi: index change missing or unparseable")
	}
	ew, ok1 := milli(p["index_equalWeight"])
	ewc, ok2 := milli(p["index_equalWeight_change"])
	if ok1 && ok2 && ew > 0 {
		ix.EqualWeightMilli, ix.EqualWeightChangeMilli = ew, ewc
	} else {
		ix.Missing = append(ix.Missing, "equal_weight")
	}
	t, err := tehran.ParseJalali(str(p, "date"), str(p, "time"))
	if err != nil {
		ix.SourceTime, ix.SourceTimeEstimated = ingest, true
	} else {
		ix.SourceTime = t
	}
	return ix, nil
}

// milli parses a decimal number (JSON number or string, optional thousands separators, at most
// three decimals) into thousandths, exactly (no float). More decimals are refused, not rounded.
func milli(raw json.RawMessage) (int64, bool) {
	s := strings.Trim(strings.TrimSpace(string(bytes.TrimSpace(raw))), `"`)
	s = strings.ReplaceAll(s, ",", "")
	if s == "" || s == "null" {
		return 0, false
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 3 || whole == "" {
		return 0, false
	}
	frac += strings.Repeat("0", 3-len(frac))
	var v int64
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int64(c-'0')
		if v > 1<<50 {
			return 0, false
		}
	}
	if neg {
		v = -v
	}
	return v, true
}
