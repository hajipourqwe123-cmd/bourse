// engine consumes snapshots and publishes flow metrics, quality issues and AI signals.
//
//	collector | engine                      # BUS=ndjson (default): NDJSON stdin → stdout
//	BUS=nats NATS_URL=nats://… engine       # durable JetStream consumer on md.snap.> → JetStream
//	ENGINE_EXIT_WHEN_IDLE=1                 # (nats) exit 0 once every stored snapshot is acknowledged
//	ENGINE_LEASE_TTL=15s                    # (nats) single-engine lease; a second engine refuses to start
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
	"bourse/internal/config"
	"bourse/internal/flow"
)

func main() {
	log.SetOutput(os.Stderr)
	cfg := flow.DefaultConfig()
	cfg.HotThreshold = config.Int("HOT_THRESHOLD_RIAL", cfg.HotThreshold)
	cfg.PlusThreshold = config.Int("PLUS_THRESHOLD_RIAL", cfg.PlusThreshold)
	cfg.StaleAfter = config.Dur("STALE_AFTER", cfg.StaleAfter)

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
		js, err := bus.ConnectJetStream(ctx, config.Str("NATS_URL", "nats://127.0.0.1:4222"), "engine", bus.DefaultStreams())
		if err != nil {
			log.Fatalf("engine: %v", err)
		}
		go js.WatchLimits(ctx, 30*time.Second)
		log.Printf("engine: bus=nats consumer=%s", engineDurable)
		p := newProcessor(cfg, js)
		err = runLeased(ctx, js, p, engineConsumer(), config.Str("ENGINE_EXIT_WHEN_IDLE", "") == "1",
			leaseHolder(), config.Dur("ENGINE_LEASE_TTL", 15*time.Second))
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
