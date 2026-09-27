package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

// SourceArena polls the vendor's "all instruments" endpoint (?token=…&all&type=0).
//
// STATUS: every mapped key VERIFIED against one live response (2026-09-26 07:00 Tehran, data of
// 2026-09-23; D-03, docs/source-mapping.md): 1102 rows, instance_code on every row. Numbers are
// mostly numeric strings (num() parses both). The payload has a 5-level book, the permitted
// price range and individual/institutional values too; they are not mapped yet (report only).
// last_trade_date/time is the last trade, not a snapshot time: SourceTimeEstimated stays set.
type SourceArena struct {
	base   string
	token  string // never logged
	client *http.Client
	now    func() time.Time

	// DailyLimit > 0: at most this many requests per Tehran day (plan quota; SOURCEARENA_DAILY_LIMIT).
	// The vendor enforces the real quota; its refusal is ErrBudget too.
	DailyLimit int
	// BudgetFile, if set, keeps the day's request count across restarts (SOURCEARENA_BUDGET_FILE).
	// An unreadable or corrupt file counts as the whole budget spent (fail closed).
	BudgetFile string
	day        string
	used       int
	// SaveLatest, if set, receives each successful raw payload (atomic replace) so a LOCAL
	// comparison (cmd/vendorcmp -watch) can reuse it instead of spending quota. Never commit it.
	SaveLatest string
}

// NewSourceArena builds the adapter. token comes from the environment, never from code.
// Redirects are not followed: they would carry the token-bearing query to another URL.
func NewSourceArena(base, token string, timeout time.Duration) *SourceArena {
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &SourceArena{base: base, token: token, client: client, now: time.Now}
}

func (s *SourceArena) Name() string { return "sourcearena" }

type fieldSpec struct {
	key      string // vendor JSON key
	verified bool   // seen in the vendor's documentation or a live response
}

// fieldMap maps canonical fields to vendor keys.
// Documented sample: close_price = last trade (آخرین), final_price = closing price (پایانی).
var fieldMap = map[string]fieldSpec{
	model.FPriceLast:     {"close_price", true},
	"price_close":        {"final_price", true},
	"price_first":        {"first_price", true},
	"price_max":          {"highest_price", true},
	"price_min":          {"lowest_price", true},
	"price_yesterday":    {"yesterday_price", true},
	"trade_count":        {"trade_number", true},
	model.FVolume:        {"trade_volume", true},
	model.FValue:         {"trade_value", true},
	model.FIndBuyVol:     {"real_buy_volume", true},
	model.FInstBuyVol:    {"co_buy_volume", true},
	model.FIndSellVol:    {"real_sell_volume", true},
	model.FInstSellVol:   {"co_sell_volume", true},
	model.FIndBuyCount:   {"real_buy_count", true},
	model.FIndSellCount:  {"real_sell_count", true},
	model.FInstBuyCount:  {"co_buy_count", true},
	model.FInstSellCount: {"co_sell_count", true},
}

// Fetch performs one poll.
func (s *SourceArena) Fetch(ctx context.Context) ([]model.Snapshot, error) {
	if s.token == "" {
		return nil, fmt.Errorf("sourcearena: SOURCEARENA_TOKEN is not set")
	}
	if d := tehran.TradingDay(s.now()); d != s.day {
		s.day, s.used = d, s.loadUsed(d)
	}
	if s.DailyLimit > 0 && s.used >= s.DailyLimit {
		return nil, fmt.Errorf("sourcearena: %w (%d requests on %s; SOURCEARENA_DAILY_LIMIT=%d)", ErrBudget, s.used, s.day, s.DailyLimit)
	}
	s.used++ // every attempt counts: the vendor counts failed ones too
	s.saveUsed()
	u := s.base + "?token=" + url.QueryEscape(s.token) + "&all&type=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("sourcearena: build request: %w", err) // URL not echoed: it carries the token
	}
	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error // its message carries the whole URL: report only the operation and cause
		if errors.As(err, &ue) {
			err = fmt.Errorf("%s: %w", ue.Op, ue.Err)
		}
		return nil, fmt.Errorf("sourcearena: request failed: %s", redact(err.Error(), s.token))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("sourcearena: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sourcearena: HTTP %d", resp.StatusCode)
	}
	if msg, ok := saVendorError(body); ok {
		msg = redact(msg, s.token)
		if strings.Contains(strings.ToLower(msg), "limit") {
			// {"Error":"daily request limit reached"} (seen 2026-09-27 after ~45 requests).
			return nil, fmt.Errorf("sourcearena: %w: vendor: %q (%d requests counted on %s)", ErrBudget, msg, s.used, s.day)
		}
		return nil, fmt.Errorf("sourcearena: vendor error: %q", msg)
	}
	snaps, err := Parse(body, s.now())
	if err == nil && s.SaveLatest != "" {
		if werr := saveAtomic(s.SaveLatest, body); werr != nil {
			log.Printf("sourcearena: save latest payload: %v", werr)
		}
	}
	return snaps, err
}

// Used returns the requests counted on the current Tehran day (quota reporting).
func (s *SourceArena) Used() (day string, used int) { return s.day, s.used }

// saVendorError recognises the vendor's error object ({"Error": "…"}, sent with HTTP 200).
func saVendorError(body []byte) (string, bool) {
	b := bytes.TrimSpace(body)
	if len(b) == 0 || b[0] != '{' {
		return "", false
	}
	var e map[string]any
	if json.Unmarshal(b, &e) != nil {
		return "", false
	}
	for _, k := range []string{"Error", "error"} {
		if v, ok := e[k]; ok {
			m := fmt.Sprint(v)
			if len(m) > 200 {
				m = m[:200]
			}
			return m, true
		}
	}
	return "", false
}

type budgetState struct {
	Day  string `json:"day"`
	Used int    `json:"used"`
}

func (s *SourceArena) loadUsed(day string) int {
	if s.BudgetFile == "" {
		return 0
	}
	b, err := os.ReadFile(s.BudgetFile)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	var st budgetState
	if err != nil || json.Unmarshal(b, &st) != nil {
		log.Printf("sourcearena: budget file unreadable (%v): counting the day's budget as spent", err)
		if s.DailyLimit > 0 {
			return s.DailyLimit
		}
		return 0
	}
	if st.Day != day {
		return 0
	}
	return st.Used
}

func (s *SourceArena) saveUsed() {
	if s.BudgetFile == "" {
		return
	}
	b, _ := json.Marshal(budgetState{Day: s.day, Used: s.used})
	if err := saveAtomic(s.BudgetFile, b); err != nil {
		log.Printf("sourcearena: save budget file: %v", err)
	}
}

// saveAtomic replaces path with b via a temporary file in the same directory.
func saveAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// redact removes secret from msg, also in its URL-encoded forms (a key in a query string shows
// up escaped in *url.Error messages).
func redact(msg, secret string) string {
	if secret == "" {
		return msg
	}
	for _, s := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	return msg
}

// Parse converts one vendor payload into snapshots. Exported for contract tests.
func Parse(body []byte, ingest time.Time) ([]model.Snapshot, error) {
	var rows []map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("sourcearena: payload is not a JSON array: %w", err)
	}
	out := make([]model.Snapshot, 0, len(rows))
	for _, row := range rows {
		sn := model.Snapshot{
			InsCode: str(row, "instance_code"), Symbol: str(row, "name"),
			Source: "sourcearena", IngestTime: ingest,
			SourceTime: ingest, SourceTimeEstimated: true, // no snapshot time (last_trade_* is the last trade)
		}
		if sn.InsCode == "" {
			continue // cannot key an instrument without its internal code
		}
		set := func(canon string, dst *int64) {
			spec := fieldMap[canon]
			v, ok := num(row, spec.key)
			// A price of 0 is the vendor's "no trade today" (first/high/low of an untraded
			// instrument), never a price; totals and counts cannot be negative.
			if price := strings.HasPrefix(canon, "price_"); ok && ((price && v <= 0) || (!price && v < 0)) {
				ok = false
			}
			if !ok {
				sn.Missing = append(sn.Missing, canon)
				return
			}
			*dst = v
		}
		set(model.FPriceLast, &sn.PriceLast)
		set("price_close", &sn.PriceClose)
		set("price_first", &sn.PriceFirst)
		set("price_max", &sn.PriceMax)
		set("price_min", &sn.PriceMin)
		set("price_yesterday", &sn.PriceYesterday)
		set("trade_count", &sn.TradeCount)
		set(model.FVolume, &sn.Volume)
		set(model.FValue, &sn.Value)
		set(model.FIndBuyVol, &sn.IndBuyVol)
		set(model.FInstBuyVol, &sn.InstBuyVol)
		set(model.FIndSellVol, &sn.IndSellVol)
		set(model.FInstSellVol, &sn.InstSellVol)
		set(model.FIndBuyCount, &sn.IndBuyCount)
		set(model.FIndSellCount, &sn.IndSellCount)
		set(model.FInstBuyCount, &sn.InstBuyCount)
		set(model.FInstSellCount, &sn.InstSellCount)
		// Permitted range: decimal strings ("3170.00"). ≤ 1 or ≥ 999,999,999 are the vendor's "no
		// limit" placeholders (energy products), recorded as missing like BrsApi's (rule 1).
		lo, okLo := wholeDecimal(row, "daily_price_low")
		hi, okHi := wholeDecimal(row, "daily_price_high")
		if okLo && okHi && lo > 1 && hi >= lo && hi < saNoLimit {
			sn.PriceLimitMin, sn.PriceLimitMax = lo, hi
		} else {
			sn.Missing = append(sn.Missing, model.FPriceLimits)
		}
		sn.Missing = append(sn.Missing, model.FBook) // in the payload, not mapped yet
		out = append(out, sn)
	}
	return out, nil
}

func str(row map[string]json.RawMessage, k string) string {
	raw, ok := row[k]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// num accepts JSON numbers or numeric strings (the vendor uses both). Decimals are rejected
// for integer canonical fields rather than silently truncated.
func num(row map[string]json.RawMessage, k string) (int64, bool) {
	raw, ok := row[k]
	if !ok || string(raw) == "null" {
		return 0, false
	}
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// saNoLimit is the vendor's "no upper price limit" placeholder (999999999.00).
const saNoLimit = 999_999_999

// wholeDecimal parses a rial amount sent as a decimal string ("3170.00"): accepted only when the
// fraction is zero (a real fraction would be a unit error, never truncated).
func wholeDecimal(row map[string]json.RawMessage, k string) (int64, bool) {
	raw, ok := row[k]
	if !ok {
		return 0, false
	}
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		if strings.Trim(s[i+1:], "0") != "" {
			return 0, false
		}
		s = s[:i]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}
