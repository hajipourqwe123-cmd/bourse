package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"bourse/internal/anomaly"
	"bourse/internal/bus"
	"bourse/internal/calendar"
	"bourse/internal/market"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

const (
	apiKey      = "k-secret-api-key"
	tokenSecret = "t-secret-hmac-key"
)

func startServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "nats.conf")
	if err := os.WriteFile(conf, []byte(fmt.Sprintf("listen: \"127.0.0.1:-1\"\njetstream { store_dir: %q, max_file_store: 64G }\n", dir)), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	opts.NoLog, opts.NoSigs = true, true
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(s.Shutdown)
	return s.ClientURL()
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// fakeCentrifugo records every publication of the batch API.
type fakeCentrifugo struct {
	mu   sync.Mutex
	pubs []publication
	bad  int // requests without the right API key
}

func (f *fakeCentrifugo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/batch" || r.Header.Get("X-API-Key") != apiKey {
		f.mu.Lock()
		f.bad++
		f.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Commands []struct {
			Publish struct {
				Channel string          `json:"channel"`
				Data    json.RawMessage `json:"data"`
			} `json:"publish"`
		} `json:"commands"`
	}
	b, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(b, &body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	replies := make([]map[string]any, len(body.Commands))
	for i, c := range body.Commands {
		f.pubs = append(f.pubs, publication{Channel: c.Publish.Channel, Data: c.Publish.Data})
		replies[i] = map[string]any{"publish": map[string]any{}}
	}
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"replies": replies})
}

// find returns the payloads published on channel.
func (f *fakeCentrifugo) find(channel string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []json.RawMessage
	for _, p := range f.pubs {
		if p.Channel == channel {
			out = append(out, p.Data.(json.RawMessage))
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func testConfig(natsURL, centURL string) Config {
	m := market.DefaultConfig()
	m.Sessions = calendar.Default().WithInstruments(map[string]string{"S1": market.Stock, "S2": market.Stock})
	return Config{Addr: "127.0.0.1:0", NATSURL: natsURL, CentrifugoAPI: centURL + "/api", CentrifugoKey: apiKey,
		TokenSecret: tokenSecret, CentrifugoWS: "ws://127.0.0.1:8000/connection/websocket", LeaseTTL: 3 * time.Second,
		WebDir: "/nonexistent", StaleAfter: 30 * time.Second, Market: m, Tick: 50 * time.Millisecond}
}

func snapAt(ins string, t time.Time, last, value int64) model.Snapshot {
	return model.Snapshot{InsCode: ins, Symbol: "sym-" + ins, Source: "test", SourceTime: t, IngestTime: t.Add(time.Second),
		PriceLast: last, PriceYesterday: 1000, TradeCount: 1, Volume: 10, Value: value}
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestGatewayEndToEnd(t *testing.T) {
	logs := &syncBuf{}
	prev := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	natsURL := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	js, err := bus.ConnectJetStream(ctx, natsURL, "test-producer", bus.DefaultStreams())
	if err != nil {
		t.Fatal(err)
	}
	defer js.Close()
	now := time.Now()
	for _, s := range []model.Snapshot{snapAt("S1", now.Add(-2*time.Minute), 1010, 5_000), snapAt("S2", now.Add(-2*time.Minute), 1000, 7_000),
		snapAt("SYNTHETIC0001", now.Add(-2*time.Minute), 1, 1)} {
		if err := js.Publish(bus.SubjSnapshot(s.InsCode), s); err != nil {
			t.Fatal(err)
		}
	}
	if err := js.Publish(bus.SubjGame("S1"), model.GameTotals{InsCode: "S1", Class: market.Stock, Day: tehran.TradingDay(now.Add(-2 * time.Minute)),
		AsOf: now.Add(-2 * time.Minute), NetHot: -42, Partial: true}); err != nil {
		t.Fatal(err)
	}
	if err := js.Publish(bus.SubjQuality("S2"), model.QualityIssue{InsCode: "S2", Code: quality.Stale, At: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}

	cent := &fakeCentrifugo{}
	cs := httptest.NewServer(cent)
	defer cs.Close()
	cfg := testConfig(natsURL, cs.URL)
	ready := make(chan string, 1)
	errc := make(chan error, 1)
	gctx, gcancel := context.WithCancel(ctx)
	go func() { errc <- run(gctx, cfg, ready) }()
	var addr string
	select {
	case addr = <-ready:
	case err := <-errc:
		t.Fatalf("gateway: %v", err)
	}
	base := "http://" + addr

	var st stateMsg
	waitFor(t, "state", func() bool { return getJSON(t, base+"/api/v1/state", &st) == http.StatusOK })
	if len(st.Rows) != 2 || st.Rows[0].Ins != "S1" || st.Rows[1].Ins != "S2" {
		t.Fatalf("rows = %+v, want S1 and S2 (synthetic ignored)", st.Rows)
	}
	if st.Rows[0].NetHot == nil || *st.Rows[0].NetHot != -42 || !st.Rows[0].Partial || st.Rows[1].Issues != 1 {
		t.Errorf("rows = %+v", st.Rows)
	}
	if st.Summary.Syn || st.Summary.Instruments != 2 || st.HotThreshold != 2_000_000_000 || st.StaleAfterMs != 30_000 ||
		len(st.Sessions) != len(market.Classes) || !st.CalendarUnverified {
		t.Errorf("state = %+v", st)
	}
	var kpi *market.KPI
	for i := range st.Summary.KPIs {
		if st.Summary.KPIs[i].ID == "value_stock" {
			kpi = &st.Summary.KPIs[i]
		}
	}
	if kpi == nil || kpi.Value == nil || *kpi.Value != 12_000 {
		t.Errorf("stock value KPI = %+v", kpi)
	}

	// First tick after the rebuild: every row as a delta with seq 1, and the summary.
	waitFor(t, "mkt:symbols", func() bool { return len(cent.find(chSymbols)) > 0 })
	waitFor(t, "mkt:summary", func() bool { return len(cent.find(chSummary)) > 0 })
	var sm symbolsMsg
	if err := json.Unmarshal(cent.find(chSymbols)[0], &sm); err != nil {
		t.Fatal(err)
	}
	if sm.Seq != 1 || len(sm.Rows) != 2 || sm.GW.IsZero() {
		t.Errorf("first symbols delta = seq %d rows %d gw %s", sm.Seq, len(sm.Rows), sm.GW)
	}
	if len(cent.find(chSym+"S1")) == 0 || len(cent.find(chSym+"SYNTHETIC0001")) != 0 {
		t.Error("sym:<ins> must carry real instruments only")
	}

	// Live: a new snapshot, a radar signal, a hot flow event.
	if err := js.Publish(bus.SubjSnapshot("S1"), snapAt("S1", now, 1020, 6_000)); err != nil {
		t.Fatal(err)
	}
	if err := js.Publish(bus.SubjAI("S1"), anomaly.Event{InsCode: "S1", Kind: "anomaly", Z: 5.8, At: now, Reason: "دلیل"}); err != nil {
		t.Fatal(err)
	}
	if err := js.Publish(bus.SubjFlow("S1"), model.FlowEvent{InsCode: "S1", Class: market.Stock, Side: model.Buy, Band: model.BandHot,
		IntervalFrom: now.Add(-5 * time.Second), IntervalTo: now, Value: 3_000_000_000}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second symbols delta", func() bool { return len(cent.find(chSymbols)) >= 2 })
	var sm2 symbolsMsg
	_ = json.Unmarshal(cent.find(chSymbols)[1], &sm2)
	if sm2.Seq != 2 || len(sm2.Rows) != 1 || sm2.Rows[0].Ins != "S1" || *sm2.Rows[0].Last != 1020 {
		t.Errorf("second delta = %+v", sm2)
	}
	waitFor(t, "radar:signals", func() bool { return len(cent.find(chRadar)) == 1 })
	var sig signalMsg
	_ = json.Unmarshal(cent.find(chRadar)[0], &sig)
	if sig.Signal.Sym != "sym-S1" || sig.Signal.Class != market.Stock || sig.Signal.Reason != "دلیل" {
		t.Errorf("signal = %+v", sig.Signal)
	}
	waitFor(t, "flow:hot", func() bool { return len(cent.find(chHot)) == 1 })

	if code := getJSON(t, base+"/api/v1/token", nil); code != http.StatusNotFound {
		t.Errorf("token endpoint without GATEWAY_DEV_TOKEN = %d, want 404", code)
	}
	var tm map[string]int64
	if getJSON(t, base+"/api/v1/time", &tm) != http.StatusOK || tm["now"] < now.UnixMilli() {
		t.Errorf("time = %v", tm)
	}

	// A second gateway refuses to start while the first holds the lease.
	cfg2 := cfg
	err = run(ctx, cfg2, nil)
	if !errors.Is(err, bus.ErrLeaseHeld) {
		t.Errorf("second gateway: %v, want ErrLeaseHeld", err)
	}

	gcancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("gateway exit: %v", err)
	}
	if cent.bad != 0 {
		t.Errorf("%d Centrifugo requests without the API key", cent.bad)
	}
	for _, secret := range []string{apiKey, tokenSecret} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("a secret appears in the logs")
		}
	}
	if !strings.Contains(logs.String(), "synthetic instrument SYNTHETIC0001") {
		t.Errorf("ignored synthetic data must be logged:\n%s", logs.String())
	}
}

func TestCheckLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:8080": true, "[::1]:8080": true, "localhost:8080": true, "127.0.0.2:80": true,
		":8080": false, "0.0.0.0:8080": false, "[::]:8080": false, "192.168.1.10:8080": false,
		"example.com:80": false, "8080": false,
	} {
		if err := checkLoopback(addr); (err == nil) != ok {
			t.Errorf("checkLoopback(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

func TestRunRefusesNonLoopbackBeforeAnything(t *testing.T) {
	cfg := testConfig("nats://127.0.0.1:1", "http://127.0.0.1:1")
	cfg.Addr = "0.0.0.0:8080"
	if err := run(context.Background(), cfg, nil); !errors.Is(err, errNotLoopback) {
		t.Fatalf("run = %v, want errNotLoopback", err)
	}
}

func TestDevToken(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	tok := devToken(tokenSecret, now)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", tok)
	}
	mac := hmac.New(sha256.New, []byte(tokenSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("bad HS256 signature")
	}
	var hdr, claims map[string]any
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(h, &hdr)
	_ = json.Unmarshal(c, &claims)
	if hdr["alg"] != "HS256" {
		t.Errorf("header = %v", hdr)
	}
	if sub, _ := claims["sub"].(string); !strings.HasPrefix(sub, "anon-") || len(sub) != len("anon-")+16 {
		t.Errorf("sub = %v", claims["sub"])
	}
	if claims["exp"].(float64) != float64(now.Add(15*time.Minute).Unix()) {
		t.Errorf("exp = %v", claims["exp"])
	}
	if devToken(tokenSecret, now) == tok {
		t.Error("every token must carry its own anonymous subject")
	}
}

func TestTokenEndpointEnabled(t *testing.T) {
	cfg := testConfig("", "")
	cfg.DevToken = true
	h := newHub(cfg, nil, func() error { return nil }, time.Now)
	srv, addr, err := serve(cfg, h)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var out struct {
		Token string `json:"token"`
		Exp   int64  `json:"exp"`
	}
	if code := getJSON(t, "http://"+addr+"/api/v1/token", &out); code != http.StatusOK || strings.Count(out.Token, ".") != 2 {
		t.Fatalf("token = %d %+v", code, out)
	}
	if code := getJSON(t, "http://"+addr+"/api/v1/state", nil); code != http.StatusServiceUnavailable {
		t.Errorf("state before the rebuild = %d, want 503", code)
	}
	cfg.TokenSecret = ""
	srv2, addr2, _ := serve(cfg, h)
	defer srv2.Close()
	if code := getJSON(t, "http://"+addr2+"/api/v1/token", nil); code != http.StatusServiceUnavailable {
		t.Errorf("token without secret = %d, want 503", code)
	}
}
