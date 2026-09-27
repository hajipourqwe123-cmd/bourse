package source

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"bourse/internal/model"
)

// Contract test against the vendor's PUBLICLY DOCUMENTED sample (testdata). Only documented keys
// are present, so the unverified keys must surface as Missing and block flow metrics.
func TestParseDocumentedSample(t *testing.T) {
	body, err := os.ReadFile("testdata/sourcearena_documented_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 9, 30, 0, 0, time.UTC)
	got, err := Parse(body, now)
	if err != nil || len(got) != 1 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	s := got[0]
	if s.InsCode != "204092872752957" || s.Symbol != "شصدف" {
		t.Fatalf("keys: %+v", s)
	}
	if s.PriceLast != 33493 || s.PriceClose != 32023 || s.Volume != 123747 || s.Value != 4144658271 ||
		s.IndBuyVol != 123747 || s.InstSellVol != 16084 || s.IndBuyCount != 5 || s.TradeCount != 4726 {
		t.Fatalf("mapping: %+v", s)
	}
	// Documented sample is internally consistent: each side sums to total volume.
	if s.IndBuyVol+s.InstBuyVol != s.Volume || s.IndSellVol+s.InstSellVol != s.Volume {
		t.Fatalf("sample side sums do not match volume")
	}
	for _, f := range []string{model.FIndSellCount, model.FInstBuyCount, model.FInstSellCount, model.FBook} {
		if s.Has(f) {
			t.Fatalf("%s must be reported missing until verified", f)
		}
	}
	if !s.SourceTimeEstimated || !s.IngestTime.Equal(now) {
		t.Fatalf("time flags: %+v", s)
	}
}

func TestNumRejectsDecimalsAndGarbage(t *testing.T) {
	body := []byte(`[{"instance_code":"1","close_price":"12.5","trade_volume":"abc","trade_value":"1,000"}]`)
	got, err := Parse(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := got[0]
	if s.Has(model.FPriceLast) || s.Has(model.FVolume) {
		t.Fatalf("decimal/garbage must be missing, got %+v", s)
	}
	if !s.Has(model.FValue) || s.Value != 1000 {
		t.Fatalf("thousand separators should parse: %+v", s)
	}
}

func TestRowWithoutInsCodeSkipped(t *testing.T) {
	got, _ := Parse([]byte(`[{"name":"x"}]`), time.Now())
	if len(got) != 0 {
		t.Fatalf("rows without instance_code must be skipped")
	}
}

func TestRedact(t *testing.T) {
	if r := redact("GET https://x/?token=SECRET: timeout", "SECRET"); r != "GET https://x/?token=***: timeout" {
		t.Fatalf("redact: %s", r)
	}
}

// Contract test against 18 rows of ONE live all-symbols response (2026-09-26 07:00 Tehran,
// market closed, data of 2026-09-23), recorded without the token. The four keys that were
// unverified (real_sell_count, co_buy_count, co_sell_count, yesterday_price) are present on
// every row; numbers arrive as numeric strings. Expected values copied by hand; فولاد is the
// same instrument as in the BrsApi fixture and agrees with it field by field.
func TestParseLiveSample(t *testing.T) {
	body, err := os.ReadFile("testdata/sourcearena_live_all_type0.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 3, 30, 0, 0, time.UTC)
	got, err := Parse(body, now)
	if err != nil || len(got) != 18 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	by := map[string]model.Snapshot{}
	for _, s := range got {
		by[s.Symbol] = s
		for _, f := range model.FlowFields {
			if !s.Has(f) {
				t.Errorf("%s: flow field %s missing: %v", s.Symbol, f, s.Missing)
			}
		}
		if s.IndBuyVol+s.InstBuyVol != s.Volume || s.IndSellVol+s.InstSellVol != s.Volume {
			t.Errorf("%s: sides do not sum to the volume", s.Symbol)
		}
	}
	f := by["فولاد"]
	if f.InsCode != "46348559193224090" || f.PriceLast != 3350 || f.PriceClose != 3350 || f.PriceYesterday != 3260 ||
		f.PriceMin != 3280 || f.PriceMax != 3350 || f.TradeCount != 21174 || f.Volume != 2210827100 || f.Value != 7398241941410 ||
		f.IndBuyVol != 1345256558 || f.InstBuyVol != 865570542 || f.IndSellVol != 1703245171 || f.InstSellVol != 507581929 ||
		f.IndBuyCount != 2232 || f.InstBuyCount != 24 || f.IndSellCount != 4728 || f.InstSellCount != 27 {
		t.Fatalf("فولاد mapping: %+v", f)
	}
	// Price limits are mapped from the decimal strings; the book is not mapped yet: missing, never zero.
	if !f.HasPriceLimits() || f.PriceLimitMin != 3170 || f.PriceLimitMax != 3350 {
		t.Errorf("فولاد limits: %d..%d", f.PriceLimitMin, f.PriceLimitMax)
	}
	// Book: level 1 of the live sample (bid 973 orders of 128,495,206 at 3350; ask 1 of 2,200 at 3360).
	if !f.Has(model.FBook) || len(f.Book) != 5 || f.Book[0] != (model.Level{BidPrice: 3350, BidVol: 128495206, BidCount: 973,
		AskPrice: 3360, AskVol: 2200, AskCount: 1}) {
		t.Errorf("فولاد book: %+v", f.Book)
	}
	if !f.SourceTimeEstimated {
		t.Errorf("فولاد flags: %v", f.Missing)
	}
	// A suspended instrument and one with no trade date still parse (by instance_code).
	if s := by["حفارس"]; s.InsCode == "" {
		t.Error("suspended row dropped")
	}
	// Never traded (last_trade_date "-"): first/high/low are the vendor's 0 → missing, not a
	// price; last and close carry the reference price (TSETMC convention), volume 0 is real.
	p := by["پویا"]
	if p.InsCode != "48970598895465763" || p.Volume != 0 || !p.Has(model.FVolume) || p.PriceLast != 653121 {
		t.Errorf("no-trade row: %+v", p)
	}
	for _, fld := range []string{"price_first", "price_max", "price_min"} {
		if p.Has(fld) {
			t.Errorf("no-trade row: %s 0 must be missing", fld)
		}
	}
}

const saTestToken = "t0k+/=en" // escapes differently in a query string

// Redirects are not followed (they would carry the token); transport errors never show the
// token, raw or URL-encoded; a vendor error object with HTTP 200 is an error.
func TestSourceArenaRedirectAndErrors(t *testing.T) {
	var elsewhere int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { elsewhere++ }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/?"+r.URL.RawQuery, http.StatusFound)
	}))
	defer redir.Close()
	_, err := NewSourceArena(redir.URL+"/api/", saTestToken, time.Second).Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") || elsewhere != 0 {
		t.Fatalf("redirect followed (%d) or not reported: %v", elsewhere, err)
	}
	_, err = NewSourceArena("http://127.0.0.1:1/api/", saTestToken, time.Second).Fetch(context.Background())
	if err == nil {
		t.Fatal("unreachable host: no error")
	}
	for _, form := range []string{saTestToken, url.QueryEscape(saTestToken), url.PathEscape(saTestToken)} {
		if strings.Contains(err.Error(), form) {
			t.Fatalf("token leaked (%q): %v", form, err)
		}
	}
	vendorErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Error": "request timeout or empty response"}`)) // seen live, HTTP 200
	}))
	defer vendorErr.Close()
	if _, err := NewSourceArena(vendorErr.URL+"/api/", saTestToken, time.Second).Fetch(context.Background()); err == nil {
		t.Fatal("vendor error object accepted as data")
	}
}

func TestSourceArenaPriceLimits(t *testing.T) {
	body := []byte(`[
		{"instance_code":"1","name":"a","daily_price_low":"3170.00","daily_price_high":"3350.00","trade_volume":"1"},
		{"instance_code":"2","name":"energy","daily_price_low":"1.00","daily_price_high":"999999999.00","trade_volume":"1"},
		{"instance_code":"3","name":"frac","daily_price_low":"3170.50","daily_price_high":"3350.00","trade_volume":"1"},
		{"instance_code":"4","name":"none","trade_volume":"1"}]`)
	snaps, err := Parse(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s := snaps[0]; !s.HasPriceLimits() || s.PriceLimitMin != 3170 || s.PriceLimitMax != 3350 {
		t.Fatalf("limits not mapped: %+v", s)
	}
	for _, s := range snaps[1:] {
		if s.HasPriceLimits() || !slices.Contains(s.Missing, model.FPriceLimits) {
			t.Errorf("%s: placeholder, fraction or absent range must be missing: %+v", s.Symbol, s)
		}
	}
}

func TestSourceArenaDailyLimitAndSaveLatest(t *testing.T) {
	body, err := os.ReadFile("testdata/sourcearena_live_all_type0.json")
	if err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(body)
	}))
	defer srv.Close()
	s := NewSourceArena(srv.URL+"/", "tok", time.Second)
	s.DailyLimit = 1
	s.SaveLatest = t.TempDir() + "/latest.json"
	if _, err := s.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(s.SaveLatest); err != nil || len(saved) != len(body) {
		t.Fatalf("latest payload not saved: %v", err)
	}
	if _, err := s.Fetch(context.Background()); !errors.Is(err, ErrBudget) || hits != 1 {
		t.Fatalf("second request must be refused without calling the vendor: err=%v hits=%d", err, hits)
	}
}

func TestSourceArenaVendorLimitIsBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Error":"daily request limit reached"}`)) // live body, 2026-09-27, HTTP 200
	}))
	defer srv.Close()
	s := NewSourceArena(srv.URL+"/", "tok", time.Second)
	if _, err := s.Fetch(context.Background()); !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "daily request limit") {
		t.Fatalf("want ErrBudget with the vendor message, got %v", err)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Error":"request timeout or empty response"}`))
	}))
	defer other.Close()
	s = NewSourceArena(other.URL+"/", "tok", time.Second)
	if _, err := s.Fetch(context.Background()); err == nil || errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "vendor error") {
		t.Fatalf("want a non-budget vendor error, got %v", err)
	}
}

func TestSourceArenaBudgetFilePersists(t *testing.T) {
	body, _ := os.ReadFile("testdata/sourcearena_live_all_type0.json")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.Write(body) }))
	defer srv.Close()
	file := t.TempDir() + "/budget.json"
	mk := func() *SourceArena {
		s := NewSourceArena(srv.URL+"/", "tok", time.Second)
		s.DailyLimit, s.BudgetFile = 2, file
		return s
	}
	a := mk()
	a.Fetch(context.Background())
	b := mk() // a restart
	b.Fetch(context.Background())
	if _, err := mk().Fetch(context.Background()); !errors.Is(err, ErrBudget) || hits != 2 {
		t.Fatalf("restart must not reset the day's count: err=%v hits=%d", err, hits)
	}
	os.WriteFile(file, []byte("{corrupt"), 0o600)
	if _, err := mk().Fetch(context.Background()); !errors.Is(err, ErrBudget) || hits != 2 {
		t.Fatalf("corrupt budget file must fail closed: err=%v hits=%d", err, hits)
	}
}

func TestSourceArenaQuotaReplies(t *testing.T) {
	for msg, daily := range map[string]bool{"daily request limit reached": true, "Day limit exceeded": true,
		"rate limit exceeded": false, "per-minute limit": false, "request timeout or empty response": false} {
		if dailyQuota(msg) != daily {
			t.Errorf("dailyQuota(%q) = %v", msg, !daily)
		}
	}
	// A vendor refusal marks the day spent: a restart must not send another request.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"Error":"daily request limit reached"}`))
	}))
	defer srv.Close()
	file := t.TempDir() + "/budget.json"
	mk := func() *SourceArena {
		s := NewSourceArena(srv.URL+"/", "tok", time.Second)
		s.DailyLimit, s.BudgetFile = 40, file
		return s
	}
	mk().Fetch(context.Background())
	if _, err := mk().Fetch(context.Background()); !errors.Is(err, ErrBudget) || hits != 1 {
		t.Fatalf("after the vendor refusal a restart must not call it again: err=%v hits=%d", err, hits)
	}
	// A corrupt file blocks only its day: it is rewritten for today, and another day starts afresh.
	os.WriteFile(file, []byte("{bad"), 0o600)
	s := mk()
	if _, err := s.Fetch(context.Background()); !errors.Is(err, ErrBudget) {
		t.Fatalf("corrupt file must fail closed today: %v", err)
	}
	if got := s.loadUsed("2099-01-01"); got != 0 {
		t.Fatalf("another day after a corrupt file: used = %d, want 0", got)
	}
}
