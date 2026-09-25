// gateway turns the engine's bus outputs into the market dashboard (G-01): it rebuilds today's
// market state from JetStream, publishes it to Centrifugo channels and serves the REST API and
// the static web build.
//
//	NATS_URL=nats://127.0.0.1:4222
//	GATEWAY_ADDR=127.0.0.1:8080          # MUST be a loopback address until real auth (Sprint 7)
//	CENTRIFUGO_API_URL=http://127.0.0.1:8000/api
//	CENTRIFUGO_API_KEY=…                 # secret: env only, never logged
//	CENTRIFUGO_SECRET=…                  # HMAC key of connection tokens (dev token only)
//	CENTRIFUGO_WS_URL=ws://127.0.0.1:8000/connection/websocket   # told to the browser
//	GATEWAY_DEV_TOKEN=1                  # DEV ONLY: /api/v1/token issues anonymous tokens
//	GATEWAY_LEASE_TTL=15s                # single-gateway lease; a second gateway refuses to start
//	WEB_DIR=web/out                      # static web build served at /
//	ALLOW_SYNTHETIC_ON_BUS=1             # show SYN* data; disposable local stacks only (rule 5)
//	HOT_THRESHOLD_RIAL, PLUS_THRESHOLD_RIAL, STALE_AFTER, SESSIONS_FILE: same values as the engine
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bourse/internal/bus"
	"bourse/internal/calendar"
	"bourse/internal/config"
	"bourse/internal/market"
)

// Config is everything the gateway reads from the environment.
type Config struct {
	Addr           string
	NATSURL        string
	CentrifugoAPI  string
	CentrifugoKey  string // secret
	TokenSecret    string // secret
	CentrifugoWS   string
	DevToken       bool
	LeaseTTL       time.Duration
	WebDir         string
	AllowSynthetic bool
	StaleAfter     time.Duration
	Market         market.Config
	Tick           time.Duration // publish period
}

func loadConfig() (Config, error) {
	cal, err := calendar.Load(config.Str("SESSIONS_FILE", ""))
	if err != nil {
		return Config{}, err
	}
	m := market.DefaultConfig()
	m.Sessions = cal
	m.HotThreshold = config.Int("HOT_THRESHOLD_RIAL", m.HotThreshold)
	m.PlusThreshold = config.Int("PLUS_THRESHOLD_RIAL", m.PlusThreshold)
	return Config{
		Addr:           config.Str("GATEWAY_ADDR", "127.0.0.1:8080"),
		NATSURL:        config.Str("NATS_URL", "nats://127.0.0.1:4222"),
		CentrifugoAPI:  config.Str("CENTRIFUGO_API_URL", "http://127.0.0.1:8000/api"),
		CentrifugoKey:  os.Getenv("CENTRIFUGO_API_KEY"),
		TokenSecret:    os.Getenv("CENTRIFUGO_SECRET"),
		CentrifugoWS:   config.Str("CENTRIFUGO_WS_URL", "ws://127.0.0.1:8000/connection/websocket"),
		DevToken:       config.Str("GATEWAY_DEV_TOKEN", "") == "1",
		LeaseTTL:       config.Dur("GATEWAY_LEASE_TTL", 15*time.Second),
		WebDir:         config.Str("WEB_DIR", "web/out"),
		AllowSynthetic: config.Str("ALLOW_SYNTHETIC_ON_BUS", "") == "1",
		StaleAfter:     config.Dur("STALE_AFTER", 30*time.Second),
		Market:         m,
		Tick:           time.Second,
	}, nil
}

// errNotLoopback: until real authentication (Sprint 7) the gateway serves only this machine.
var errNotLoopback = errors.New("GATEWAY_ADDR must be a loopback address (127.0.0.1, ::1 or localhost) until real authentication (Sprint 7)")

// checkLoopback accepts only an explicit loopback host; an empty host (":8080") binds every
// interface and is refused.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("GATEWAY_ADDR %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%w; got %q", errNotLoopback, addr)
}

func main() {
	log.SetOutput(os.Stderr)
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, nil); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("gateway: %v", err)
	}
}

// run starts the gateway; ready (may be nil) receives the HTTP listen address once serving.
func run(ctx context.Context, cfg Config, ready chan<- string) error {
	if err := checkLoopback(cfg.Addr); err != nil {
		return err
	}
	if cfg.LeaseTTL < 3*time.Second {
		return fmt.Errorf("GATEWAY_LEASE_TTL %s is below 3s", cfg.LeaseTTL)
	}
	if cfg.DevToken {
		log.Printf("gateway: WARNING: GATEWAY_DEV_TOKEN=1: /api/v1/token issues ANONYMOUS connection tokens; " +
			"development only, allowed because the gateway is bound to loopback (real auth: Sprint 7)")
	}
	if cfg.AllowSynthetic {
		log.Printf("gateway: WARNING: ALLOW_SYNTHETIC_ON_BUS=1: synthetic (SYN*) data is shown (labelled); disposable local stacks only (rule 5)")
	}
	if n := cfg.Market.Sessions.Unverified(); n > 0 {
		log.Printf("gateway: WARNING: session calendar has %d rules/holidays not verified against an official source (docs/sessions.md)", n)
	}
	// Dial only: the gateway never creates or changes streams.
	js, err := bus.DialJetStream(ctx, cfg.NATSURL, "gateway", bus.DefaultStreams())
	if err != nil {
		return err
	}
	defer js.Close()
	lease, err := js.AcquireLease(ctx, "gateway_lease", "gateway", holder(), cfg.LeaseTTL)
	if err != nil {
		if errors.Is(err, bus.ErrLeaseHeld) {
			return fmt.Errorf("refusing to start: %w", err)
		}
		return err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lease.Release(rctx)
	}()

	h := newHub(cfg, newCentrifugo(cfg.CentrifugoAPI, cfg.CentrifugoKey), lease.Valid, time.Now)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 6)
	for _, t := range h.tails() {
		go func() { errc <- js.Tail(ctx, t.stream, t.filter, t.start, t.fn, t.caughtUp) }()
	}
	go func() { errc <- h.loop(ctx) }()
	srv, addr, err := serve(cfg, h)
	if err != nil {
		return err
	}
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer scancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("gateway: serving on http://%s", addr)
	if ready != nil {
		ready <- addr
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lease.Lost():
		return fmt.Errorf("gateway lease lost: %w", lease.Valid())
	case err := <-errc:
		return err
	}
}

func holder() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s/pid-%d/%s", host, os.Getpid(), hex.EncodeToString(b))
}
