package source

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

// Contract test against 18 rows of ONE live AllSymbols type=1 response (2026-09-25, market
// closed, data of 2026-09-23), recorded without the key. Expected values are copied by hand
// from the recording.
func TestBrsApiLiveContract(t *testing.T) {
	body, err := os.ReadFile("testdata/brsapi_allsymbols_type1.json")
	if err != nil {
		t.Fatal(err)
	}
	ingest := time.Date(2026, 9, 25, 20, 18, 36, 0, time.UTC)
	got, err := ParseBrsApi(body, ingest)
	if err != nil || len(got) != 18 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	by := map[string]model.Snapshot{}
	for _, s := range got {
		by[s.Symbol] = s
		if s.Source != "brsapi" || !s.SourceTimeEstimated || !s.SourceTime.Equal(ingest) || !s.IngestTime.Equal(ingest) {
			t.Errorf("%s: source/time flags %+v", s.Symbol, s)
		}
		// Each side of the day's volume is split into individual + institutional (all rows).
		if s.Has(model.FVolume) && (s.IndBuyVol+s.InstBuyVol != s.Volume || s.IndSellVol+s.InstSellVol != s.Volume) {
			t.Errorf("%s: buy/sell sides do not sum to the volume", s.Symbol)
		}
	}

	f := by["فولاد"]
	want := model.Snapshot{InsCode: "46348559193224090", PriceLast: 3350, PriceClose: 3350, PriceFirst: 3350, PriceYesterday: 3260,
		PriceMin: 3280, PriceMax: 3350, PriceLimitMin: 3170, PriceLimitMax: 3350, TradeCount: 21174, Volume: 2210827100,
		Value: 7398241941410, IndBuyVol: 1345256558, InstBuyVol: 865570542, IndSellVol: 1703245171, InstSellVol: 507581929,
		IndBuyCount: 2232, InstBuyCount: 24, IndSellCount: 4728, InstSellCount: 27}
	if f.InsCode != want.InsCode || f.PriceLast != want.PriceLast || f.PriceClose != want.PriceClose || f.PriceFirst != want.PriceFirst ||
		f.PriceYesterday != want.PriceYesterday || f.PriceMin != want.PriceMin || f.PriceMax != want.PriceMax ||
		f.PriceLimitMin != want.PriceLimitMin || f.PriceLimitMax != want.PriceLimitMax || f.TradeCount != want.TradeCount ||
		f.Volume != want.Volume || f.Value != want.Value || f.IndBuyVol != want.IndBuyVol || f.InstBuyVol != want.InstBuyVol ||
		f.IndSellVol != want.IndSellVol || f.InstSellVol != want.InstSellVol || f.IndBuyCount != want.IndBuyCount ||
		f.InstBuyCount != want.InstBuyCount || f.IndSellCount != want.IndSellCount || f.InstSellCount != want.InstSellCount {
		t.Fatalf("فولاد mapping:\n got %+v\nwant %+v", f, want)
	}
	if !f.Complete() || !f.HasPriceLimits() {
		t.Errorf("فولاد: missing %v", f.Missing)
	}
	wantBook := []model.Level{
		{BidPrice: 3350, BidVol: 128495206, BidCount: 973, AskPrice: 3360, AskVol: 2200, AskCount: 1},
		{}, {}, {},
		{BidPrice: 3310, BidVol: 1756458, BidCount: 30, AskPrice: 3410, AskVol: 699987, AskCount: 27},
	}
	if len(f.Book) != 5 || f.Book[0] != wantBook[0] || f.Book[4] != wantBook[4] {
		t.Errorf("فولاد book: %+v", f.Book)
	}

	// An id beyond float64's exact range must survive as the exact decimal string.
	if by["پارتا"].InsCode != "72044846109864381" {
		t.Errorf("large id = %q", by["پارتا"].InsCode)
	}
	// An empty book side is real (no orders), not missing.
	if k := by["کمان"]; !k.Has(model.FBook) || k.Book[0] != (model.Level{}) {
		t.Errorf("empty book must be present as zeros: %+v %v", k.Book, k.Missing)
	}
	// ISIN …0002 fund row: price 1 is a placeholder → prices, value and limits missing; the
	// real previous price and the counts stay.
	k2 := by["کیان2"]
	for _, fld := range []string{model.FPriceLast, "price_close", "price_first", "price_min", "price_max", model.FValue, model.FPriceLimits} {
		if k2.Has(fld) {
			t.Errorf("کیان2: %s must be missing (placeholder), got %+v", fld, k2)
		}
	}
	if k2.PriceLast != 0 || k2.Value != 0 || k2.PriceYesterday != 111959 || k2.Volume != 90000000 || !k2.Has(model.FVolume) {
		t.Errorf("کیان2 values: %+v", k2)
	}
	// ISIN …0003 board: one fixed price, tmin == tmax, a real limit.
	if t3 := by["تابان3"]; !t3.HasPriceLimits() || t3.PriceLimitMin != 20570 || t3.PriceLimitMax != 20570 || t3.PriceLast != 20570 {
		t.Errorf("تابان3: %+v", t3)
	}
}

func TestBrsApiBondsContract(t *testing.T) {
	body, err := os.ReadFile("testdata/brsapi_allsymbols_type4.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseBrsApi(body, time.Now())
	if err != nil || len(got) != 2 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	for _, s := range got {
		if !digitsOnly.MatchString(s.InsCode) || !s.Has(model.FPriceLast) || !s.HasPriceLimits() || len(s.Book) != 5 {
			t.Errorf("bond %s: %+v", s.Symbol, s)
		}
	}
}

// The vendor's public sample gives id as a string on most rows; live gives numbers. Rows
// whose id is not an integer cannot be keyed and are skipped. Absent or non-integer fields go
// to Missing (never zero-filled).
func TestBrsApiIdFormsAndMissing(t *testing.T) {
	body := []byte(`[
	 {"id":"34144395039913458","l18":"عیار","pl":315399,"tvol":10,"tval":3153990,"tmin":285706,"tmax":349196},
	 {"id":6233172939132588,"l18":"پتوسعه","pl":"12.5","tvol":"1,000"},
	 {"id":"12.5","l18":"bad"},
	 {"id":null,"l18":"none"},
	 {"l18":"noid"}
	]`)
	got, err := ParseBrsApi(body, time.Now())
	if err != nil || len(got) != 2 {
		t.Fatalf("parse: %v, n=%d", err, len(got))
	}
	a, b := got[0], got[1]
	if a.InsCode != "34144395039913458" || b.InsCode != "6233172939132588" {
		t.Fatalf("ids %q %q", a.InsCode, b.InsCode)
	}
	if !a.Has(model.FPriceLast) || a.PriceLast != 315399 || !a.HasPriceLimits() {
		t.Errorf("string-id row: %+v", a)
	}
	for _, fld := range []string{model.FIndSellCount, model.FInstBuyVol, model.FBook, model.FTradeCount} {
		if a.Has(fld) {
			t.Errorf("absent %s not reported missing", fld)
		}
	}
	if b.Has(model.FPriceLast) || b.Has(model.FPriceLimits) || !b.Has(model.FVolume) || b.Volume != 1000 {
		t.Errorf("decimal price must be missing, separators parsed: %+v", b)
	}
	if _, err := ParseBrsApi([]byte(`{"successful":false}`), time.Now()); err == nil {
		t.Error("an object payload must be an error")
	}
}

const testKey = "k3y+/=secret" // escapes differently in a query string

func brsServer(t *testing.T, hits *int32, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrsApiFetchSendsKeyTypeAndUserAgent(t *testing.T) {
	type1, _ := os.ReadFile("testdata/brsapi_allsymbols_type1.json")
	type4, _ := os.ReadFile("testdata/brsapi_allsymbols_type4.json")
	var hits int32
	srv := brsServer(t, &hits, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != testKey || r.Header.Get("User-Agent") != BrsUserAgent {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Query().Get("type") {
		case "1":
			_, _ = w.Write(type1)
		case "4":
			_, _ = w.Write(type4)
		}
	})
	b := NewBrsApi(BrsApiConfig{URL: srv.URL + "/Tsetmc/AllSymbols.php", Key: testKey, Types: []string{"1", "4"}, Timeout: 5 * time.Second, DailyLimit: 10})
	got, err := b.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || hits != 2 {
		t.Fatalf("got %d snapshots in %d requests, want 20 in 2", len(got), hits)
	}
}

// The key must not appear, raw or URL-escaped, in any error: vendor errors that echo the
// request, and transport errors (*url.Error carries the full URL).
func TestBrsApiErrorsRedactKey(t *testing.T) {
	var hits int32
	srv := brsServer(t, &hits, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code_http":400,"successful":false,"status":"invalid_key","message_error":"key ` + testKey +
			` is not valid","account":{"type":"رایگان","usage_today":4,"usage_today_limit":100,"usage_5min":6,"usage_5min_limit":300,"request_block":0}}`))
	})
	b := NewBrsApi(BrsApiConfig{URL: srv.URL, Key: testKey, Types: []string{"1"}, Timeout: time.Second, DailyLimit: 10})
	_, err := b.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_key") || !strings.Contains(err.Error(), "today 4/100") {
		t.Fatalf("vendor error not reported: %v", err)
	}
	assertNoKey(t, err)

	dead := NewBrsApi(BrsApiConfig{URL: "http://127.0.0.1:1/Tsetmc/AllSymbols.php", Key: testKey, Types: []string{"1"}, Timeout: time.Second, DailyLimit: 10})
	_, err = dead.Fetch(context.Background())
	if err == nil {
		t.Fatal("unreachable host: no error")
	}
	assertNoKey(t, err)

	html := brsServer(t, &hits, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html>404</html>"))
	})
	b.cfg.URL = html.URL
	if _, err := b.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("404 page: %v", err)
	}
}

func assertNoKey(t *testing.T, err error) {
	t.Helper()
	for _, form := range []string{testKey, url.QueryEscape(testKey), url.PathEscape(testKey)} {
		if strings.Contains(err.Error(), form) {
			t.Fatalf("key leaked (%q) in %q", form, err)
		}
	}
}

// The plan's daily quota is enforced before any request: over budget nothing is sent; the
// budget renews on the next Tehran day.
func TestBrsApiDailyBudget(t *testing.T) {
	var hits int32
	srv := brsServer(t, &hits, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	b := NewBrsApi(BrsApiConfig{URL: srv.URL, Key: testKey, Types: []string{"1", "4"}, Timeout: time.Second, DailyLimit: 3})
	now := time.Date(2026, 9, 26, 8, 30, 0, 0, tehran.Loc)
	b.now = func() time.Time { return now }
	if _, err := b.Fetch(context.Background()); err != nil { // 2 requests
		t.Fatal(err)
	}
	if _, err := b.Fetch(context.Background()); !errors.Is(err, ErrBudget) { // 3rd sent, 4th refused
		t.Fatalf("over budget: %v", err)
	}
	if hits != 3 {
		t.Fatalf("%d requests sent, want 3 (budget)", hits)
	}
	now = now.Add(24 * time.Hour)
	if _, err := b.Fetch(context.Background()); err != nil || hits != 5 {
		t.Fatalf("next day: %v, hits %d", err, hits)
	}
	zero := NewBrsApi(BrsApiConfig{URL: srv.URL, Key: testKey, Types: []string{"1"}})
	if _, err := zero.Fetch(context.Background()); !errors.Is(err, ErrBudget) {
		t.Fatalf("no budget configured must send nothing: %v", err)
	}
	if _, err := NewBrsApi(BrsApiConfig{URL: srv.URL, Types: []string{"1"}, DailyLimit: 1}).Fetch(context.Background()); err == nil {
		t.Fatal("missing key accepted")
	}
}
