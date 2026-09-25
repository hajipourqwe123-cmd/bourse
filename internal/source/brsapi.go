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
	"regexp"
	"strings"
	"sync"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

// BrsApi polls BrsApi's "all symbols" endpoint (api.brsapi.ir Tsetmc/AllSymbols.php; D-03,
// docs/source-mapping.md). Verified against one live response of 2026-09-25 (last trading day
// 2026-09-23) and the vendor's public sample; contract test: testdata/brsapi_*.json.
//
//   - The key travels in the URL query: it is never logged and every error is redacted.
//   - The vendor's firewall wants a browser-like User-Agent.
//   - The payload has no snapshot timestamp: `time` is the instrument's last-event time of day
//     (no date; 06:10 for an untraded instrument = the vendor's daily reset), so SourceTime is
//     the ingest time and SourceTimeEstimated is set.
//   - The plan's request budget is enforced here (DailyLimit per Tehran day, per request type):
//     a poll beyond it is refused without calling the vendor (ErrBudget).
type BrsApi struct {
	cfg    BrsApiConfig
	client *http.Client
	now    func() time.Time

	mu   sync.Mutex
	day  string // Tehran day of used
	used int    // requests sent on day
}

// BrsApiConfig configures the adapter; Key comes from the environment only.
type BrsApiConfig struct {
	URL        string        // e.g. https://api.brsapi.ir/Tsetmc/AllSymbols.php
	Key        string        // secret: never logged
	Types      []string      // vendor "type" values polled each Fetch (1 = stocks, rights, funds; 4 = bonds)
	Timeout    time.Duration // per request
	DailyLimit int           // requests per Tehran day this process may send (plan quota); <= 0 = none allowed
}

// BrsUserAgent is sent with every request (the vendor's firewall rejects non-browser agents).
const BrsUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"

// ErrBudget: the day's request budget (plan quota) is used up; nothing was sent.
var ErrBudget = errors.New("brsapi: daily request budget reached")

// NewBrsApi builds the adapter.
func NewBrsApi(cfg BrsApiConfig) *BrsApi {
	return &BrsApi{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}, now: time.Now}
}

func (b *BrsApi) Name() string { return "brsapi" }

// Config returns the configuration (the key included: never log it).
func (b *BrsApi) Config() BrsApiConfig { return b.cfg }

// Fetch polls every configured type once and returns the union (first row wins on a repeated
// instrument code).
func (b *BrsApi) Fetch(ctx context.Context) ([]model.Snapshot, error) {
	if b.cfg.Key == "" {
		return nil, errors.New("brsapi: BRSAPI_KEY is not set")
	}
	var out []model.Snapshot
	seen := map[string]bool{}
	for _, typ := range b.cfg.Types {
		if err := b.take(); err != nil {
			return nil, err
		}
		body, err := b.get(ctx, typ)
		if err != nil {
			return nil, err
		}
		snaps, err := ParseBrsApi(body, b.now())
		if err != nil {
			return nil, fmt.Errorf("brsapi type=%s: %w", typ, err)
		}
		for _, s := range snaps {
			if !seen[s.InsCode] {
				seen[s.InsCode] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// take counts one request against the day's budget, or refuses it.
func (b *BrsApi) take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d := tehran.TradingDay(b.now()); d != b.day {
		b.day, b.used = d, 0
	}
	if b.used >= b.cfg.DailyLimit {
		return fmt.Errorf("%w (%d requests on %s; BRSAPI_DAILY_LIMIT=%d)", ErrBudget, b.used, b.day, b.cfg.DailyLimit)
	}
	b.used++
	return nil
}

func (b *BrsApi) get(ctx context.Context, typ string) ([]byte, error) {
	u, err := url.Parse(b.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("brsapi: BRSAPI_URL: %w", err)
	}
	q := u.Query()
	q.Set("type", typ)
	q.Set("key", b.cfg.Key)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("brsapi: build request failed") // the error would echo the URL (key)
	}
	req.Header.Set("User-Agent", BrsUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brsapi: request failed: %s", redact(err.Error(), b.cfg.Key))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("brsapi: read body: %s", redact(err.Error(), b.cfg.Key))
	}
	if msg, isErr := vendorError(body); resp.StatusCode != http.StatusOK || isErr {
		return nil, fmt.Errorf("brsapi type=%s: HTTP %d: %s", typ, resp.StatusCode, redact(msg, b.cfg.Key))
	}
	return body, nil
}

// vendorError reads the vendor's error object ({"successful": false, "status", "message_error",
// "account": {usage…}}); a JSON array is a data payload.
func vendorError(body []byte) (string, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return "", false
	}
	var e struct {
		Successful *bool  `json:"successful"`
		Status     string `json:"status"`
		Message    string `json:"message_error"`
		Account    *struct {
			Type       string `json:"type"`
			Today      int64  `json:"usage_today"`
			TodayLimit int64  `json:"usage_today_limit"`
			Min5       int64  `json:"usage_5min"`
			Min5Limit  int64  `json:"usage_5min_limit"`
			Block      int64  `json:"request_block"`
		} `json:"account"`
	}
	if json.Unmarshal(trimmed, &e) != nil || e.Successful == nil {
		return "unexpected non-JSON-array response", true
	}
	msg := strings.TrimSpace(e.Status + ": " + e.Message)
	if a := e.Account; a != nil {
		msg += fmt.Sprintf(" (plan %s: today %d/%d, 5 min %d/%d, blocked %d)", a.Type, a.Today, a.TodayLimit, a.Min5, a.Min5Limit, a.Block)
	}
	return msg, !*e.Successful
}

var digitsOnly = regexp.MustCompile(`^[0-9]+$`)

// brsFlow maps canonical fields to the vendor's keys (all verified live, 2026-09-25). price:
// a value <= 1 rial is the vendor's placeholder (ISIN …0002 fund rows, likely issuance and
// redemption, carry price 1 with py = the real price), never a price: reported missing.
var brsFlow = []struct {
	canon, key string
	price      bool
	dst        func(*model.Snapshot) *int64
}{
	{model.FPriceLast, "pl", true, func(s *model.Snapshot) *int64 { return &s.PriceLast }},
	{"price_close", "pc", true, func(s *model.Snapshot) *int64 { return &s.PriceClose }},
	{"price_first", "pf", true, func(s *model.Snapshot) *int64 { return &s.PriceFirst }},
	{"price_yesterday", "py", true, func(s *model.Snapshot) *int64 { return &s.PriceYesterday }},
	{"price_min", "pmin", true, func(s *model.Snapshot) *int64 { return &s.PriceMin }},
	{"price_max", "pmax", true, func(s *model.Snapshot) *int64 { return &s.PriceMax }},
	{model.FTradeCount, "tno", false, func(s *model.Snapshot) *int64 { return &s.TradeCount }},
	{model.FVolume, "tvol", false, func(s *model.Snapshot) *int64 { return &s.Volume }},
	{model.FValue, "tval", false, func(s *model.Snapshot) *int64 { return &s.Value }},
	{model.FIndBuyVol, "Buy_I_Volume", false, func(s *model.Snapshot) *int64 { return &s.IndBuyVol }},
	{model.FInstBuyVol, "Buy_N_Volume", false, func(s *model.Snapshot) *int64 { return &s.InstBuyVol }},
	{model.FIndSellVol, "Sell_I_Volume", false, func(s *model.Snapshot) *int64 { return &s.IndSellVol }},
	{model.FInstSellVol, "Sell_N_Volume", false, func(s *model.Snapshot) *int64 { return &s.InstSellVol }},
	{model.FIndBuyCount, "Buy_CountI", false, func(s *model.Snapshot) *int64 { return &s.IndBuyCount }},
	{model.FInstBuyCount, "Buy_CountN", false, func(s *model.Snapshot) *int64 { return &s.InstBuyCount }},
	{model.FIndSellCount, "Sell_CountI", false, func(s *model.Snapshot) *int64 { return &s.IndSellCount }},
	{model.FInstSellCount, "Sell_CountN", false, func(s *model.Snapshot) *int64 { return &s.InstSellCount }},
}

// ParseBrsApi converts one AllSymbols payload (a JSON array) into snapshots. Exported for
// contract tests. Rows without a numeric instrument id are skipped (they cannot be keyed).
func ParseBrsApi(body []byte, ingest time.Time) ([]model.Snapshot, error) {
	var rows []map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("payload is not a JSON array: %w", err)
	}
	out := make([]model.Snapshot, 0, len(rows))
	for _, row := range rows {
		// id is TSETMC's instrument code: a JSON number up to 7.2e16 (beyond float64 precision,
		// hence UseNumber) or, in the vendor's sample, a string.
		id := str(row, "id")
		if !digitsOnly.MatchString(id) {
			continue
		}
		sn := model.Snapshot{
			InsCode: id, Symbol: str(row, "l18"), Source: "brsapi",
			IngestTime: ingest, SourceTime: ingest, SourceTimeEstimated: true,
		}
		for _, f := range brsFlow {
			if v, ok := num(row, f.key); ok && (!f.price || v > 1) {
				*f.dst(&sn) = v
			} else {
				sn.Missing = append(sn.Missing, f.canon)
			}
		}
		// A placeholder last price makes the value (volume × placeholder) meaningless too.
		if !sn.Has(model.FPriceLast) && sn.Has(model.FValue) && sn.Volume > 0 {
			sn.Value = 0
			sn.Missing = append(sn.Missing, model.FValue)
		}
		// Price limits: a value <= 1 is the vendor's "no limit" sentinel (block/auction boards).
		lo, okLo := num(row, "tmin")
		hi, okHi := num(row, "tmax")
		if okLo && okHi && lo > 1 && hi >= lo {
			sn.PriceLimitMin, sn.PriceLimitMax = lo, hi
		} else {
			sn.Missing = append(sn.Missing, model.FPriceLimits)
		}
		if book, ok := brsBook(row); ok {
			sn.Book = book
		} else {
			sn.Missing = append(sn.Missing, model.FBook)
		}
		out = append(out, sn)
	}
	return out, nil
}

// brsBook reads the 5-level book (zd/qd/pd = bid count/volume/price, zo/qo/po = ask); all 30
// keys are required. Zeros are real (no order at that level).
func brsBook(row map[string]json.RawMessage) ([]model.Level, bool) {
	book := make([]model.Level, 5)
	for i := range book {
		n := fmt.Sprint(i + 1)
		for _, f := range []struct {
			key string
			dst *int64
		}{
			{"zd" + n, &book[i].BidCount}, {"qd" + n, &book[i].BidVol}, {"pd" + n, &book[i].BidPrice},
			{"zo" + n, &book[i].AskCount}, {"qo" + n, &book[i].AskVol}, {"po" + n, &book[i].AskPrice},
		} {
			v, ok := num(row, f.key)
			if !ok {
				return nil, false
			}
			*f.dst = v
		}
	}
	return book, true
}
