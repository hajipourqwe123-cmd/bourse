package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bourse/internal/model"
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
	return Parse(body, s.now())
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
		sn.Missing = append(sn.Missing, model.FBook, model.FPriceLimits) // in the payload, not mapped yet (D-03 report)
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
