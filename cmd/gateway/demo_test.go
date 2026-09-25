package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"bourse/internal/democlock"
)

func demoConfig(t *testing.T) Config {
	t.Helper()
	cfg := testConfig("nats://127.0.0.1:4224", "http://127.0.0.1:8000")
	cfg.AllowSynthetic = true
	d, err := democlock.New(tehranAt("11:40"), time.Now(), 5)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Demo = d
	return cfg
}

func TestCheckDemo(t *testing.T) {
	if err := checkDemo(testConfig("nats://10.0.0.1:4222", "http://10.0.0.1:8000")); err != nil {
		t.Fatalf("no DEMO_CLOCK: %v (production must be unaffected)", err)
	}
	if err := checkDemo(demoConfig(t)); err != nil {
		t.Fatal(err)
	}
	cfg := demoConfig(t)
	cfg.AllowSynthetic = false
	if err := checkDemo(cfg); err == nil || !strings.Contains(err.Error(), "ALLOW_SYNTHETIC_ON_BUS") {
		t.Errorf("demo without synthetic data allowed: %v", err)
	}
	for _, mut := range []func(*Config){
		func(c *Config) { c.NATSURL = "nats://10.0.0.1:4222" },
		func(c *Config) { c.CentrifugoAPI = "http://centrifugo.example:8000/api" },
		func(c *Config) { c.CentrifugoWS = "ws://192.168.1.2:8000/connection/websocket" },
	} {
		cfg := demoConfig(t)
		mut(&cfg)
		if err := checkDemo(cfg); err == nil {
			t.Errorf("non-loopback endpoint accepted: %+v", cfg)
		}
	}
}

// Under DEMO_CLOCK the hub shows synthetic instruments only and says so in the state.
func TestDemoShowsSyntheticOnly(t *testing.T) {
	cfg := demoConfig(t)
	now := cfg.Demo.Now()
	h := newHub(cfg, &recPub{}, func() error { return nil }, cfg.Demo.Now)
	ready(h)
	h.onSnapshot(msg("md.snap.S1", snapAt("S1", now, 1010, 5000))) // real
	syn := snapAt("SYN1", now, 1010, 5000)
	syn.Source = "synthetic"
	h.onSnapshot(msg("md.snap.SYN1", syn))
	st, ok := h.state(now)
	if !ok || len(st.Rows) != 1 || st.Rows[0].Ins != "SYN1" {
		t.Fatalf("rows %+v: want only SYN1", st.Rows)
	}
	if st.Day != "2026-09-23" || st.DemoClock == nil || st.DemoClock.Rate != 5 || !st.DemoClock.Start.Equal(tehranAt("11:40")) {
		t.Errorf("day %s demo %+v", st.Day, st.DemoClock)
	}
	h2, _, _ := hubAt(t, now, true)
	ready(h2)
	if st, _ := h2.state(now); st.DemoClock != nil {
		t.Error("demo_clock set without DEMO_CLOCK")
	}
}

func TestTimeEndpointFollowsDemoClock(t *testing.T) {
	cfg := demoConfig(t)
	h := newHub(cfg, nil, func() error { return nil }, cfg.Demo.Now)
	srv, addr, err := serve(cfg, h)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var out struct {
		Now  int64   `json:"now"`
		Rate float64 `json:"rate"`
	}
	if code := getJSON(t, "http://"+addr+"/api/v1/time", &out); code != http.StatusOK {
		t.Fatalf("time = %d", code)
	}
	if d := time.UnixMilli(out.Now).Sub(tehranAt("11:40")); d < 0 || d > 30*time.Second || out.Rate != 5 {
		t.Errorf("time = %s (rate %v), want just after the demo start 11:40, rate 5", time.UnixMilli(out.Now), out.Rate)
	}
	// Without DEMO_CLOCK: the wall clock and no rate field (unchanged response).
	cfg2 := testConfig("", "")
	h2 := newHub(cfg2, nil, func() error { return nil }, time.Now)
	srv2, addr2, err := serve(cfg2, h2)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Close()
	var raw map[string]any
	getJSON(t, "http://"+addr2+"/api/v1/time", &raw)
	if _, has := raw["rate"]; has || len(raw) != 1 {
		t.Errorf("production /api/v1/time changed: %v", raw)
	}
	if d := time.Since(time.UnixMilli(int64(raw["now"].(float64)))); d < 0 || d > 5*time.Second {
		t.Errorf("wall clock off by %s", d)
	}
}
