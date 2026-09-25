// engine consumes snapshots and publishes flow metrics, quality issues and AI signals.
//
//	collector | engine                      # BUS=ndjson (default): NDJSON stdin → stdout
//	BUS=nats NATS_URL=nats://… engine       # durable JetStream consumer on md.snap.> → JetStream
//	ENGINE_EXIT_WHEN_IDLE=1                 # (nats) exit 0 once every stored snapshot is acknowledged
//	ENGINE_LEASE_TTL=15s                    # (nats) single-engine lease (>= 3s); a second engine refuses to start
//	ENGINE_RETRY_POISON_SEQ=<seq>           # (nats) operator release of one POISON_SUSPECT message
//	ALLOW_SYNTHETIC_ON_BUS=1                # (nats) accept SYN* snapshots; disposable local stacks only
//	SESSIONS_FILE=path.json                 # trading-session calendar (default: embedded internal/calendar/sessions.json)
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bourse/internal/bus"
	"bourse/internal/calendar"
	"bourse/internal/config"
	"bourse/internal/flow"
)

func main() {
	log.SetOutput(os.Stderr)
	cfg := flow.DefaultConfig()
	cfg.HotThreshold = config.Int("HOT_THRESHOLD_RIAL", cfg.HotThreshold)
	cfg.PlusThreshold = config.Int("PLUS_THRESHOLD_RIAL", cfg.PlusThreshold)
	cfg.StaleAfter = config.Dur("STALE_AFTER", cfg.StaleAfter)
	cal, err := calendar.Load(config.Str("SESSIONS_FILE", ""))
	if err != nil {
		log.Fatalf("engine: %v", err)
	}
	if n := cal.Unverified(); n > 0 {
		log.Printf("engine: WARNING: session calendar has %d rules/holidays not verified against an official source (docs/sessions.md)", n)
	}
	cfg.Sessions = cal

	switch kind := config.Str("BUS", "ndjson"); kind {
	case "ndjson":
		p := newProcessor(cfg, bus.NewNDJSON(os.Stdout))
		p.retry = nil // a failed stdout write is not retried (it could tear a line)
		n, bad, err := p.runNDJSON(os.Stdin)
		if err != nil {
			log.Fatalf("engine: %v", err)
		}
		log.Printf("engine: processed %d snapshots, %d unreadable lines", n, bad)
	case "nats":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ttl := config.Dur("ENGINE_LEASE_TTL", 15*time.Second)
		if ttl < 3*time.Second {
			log.Fatalf("engine: ENGINE_LEASE_TTL %s is below 3s", ttl)
		}
		// Dial only: streams are ensured after the lease is taken (a refused engine changes nothing).
		js, err := bus.DialJetStream(ctx, config.Str("NATS_URL", "nats://127.0.0.1:4222"), "engine", bus.DefaultStreams())
		if err != nil {
			log.Fatalf("engine: %v", err)
		}
		log.Printf("engine: bus=nats consumer=%s", engineDurable)
		p := newProcessor(cfg, js)
		if p.allowSynthetic = config.Str("ALLOW_SYNTHETIC_ON_BUS", "") == "1"; p.allowSynthetic {
			log.Printf("engine: WARNING: ALLOW_SYNTHETIC_ON_BUS=1: synthetic (SYN*) snapshots are processed and published; " +
				"only acceptable on a disposable local stack (rule 5)")
		}
		if seq := config.Int("ENGINE_RETRY_POISON_SEQ", 0); seq > 0 {
			p.retryPoisonSeq = uint64(seq)
			log.Printf("engine: WARNING: ENGINE_RETRY_POISON_SEQ=%d: that MD message will be processed once more despite POISON_SUSPECT", seq)
		}
		err = runLeased(ctx, js, p, engineConsumer(), config.Str("ENGINE_EXIT_WHEN_IDLE", "") == "1", leaseHolder(), ttl)
		js.Close()
		switch {
		case errors.Is(err, bus.ErrLeaseHeld):
			log.Fatalf("engine: refusing to start: %v", err)
		case err != nil:
			log.Fatalf("engine: %v (unacked snapshot is redelivered to the next start)", err)
		}
	default:
		log.Fatalf("engine: unknown BUS %q (want ndjson or nats)", kind)
	}
}

// leaseHolder identifies this process in the lease: host, pid and a random suffix (in a
// container every engine is pid 1).
func leaseHolder() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s/pid-%d/%s", host, os.Getpid(), hex.EncodeToString(b))
}
