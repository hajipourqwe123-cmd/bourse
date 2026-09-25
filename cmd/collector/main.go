// collector polls a data source and publishes canonical snapshots.
//
//	SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson collector > snaps.ndjson
//	SOURCE=sourcearena SOURCEARENA_TOKEN=… POLL_INTERVAL=5s collector
//	BUS=nats NATS_URL=nats://… collector     # publish to JetStream instead of stdout
//	                                         # (SYN* refused unless ALLOW_SYNTHETIC_ON_BUS=1)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bourse/internal/bus"
	"bourse/internal/config"
	"bourse/internal/model"
	"bourse/internal/source"
)

func main() { os.Exit(run()) }

// run returns the exit code; deferred cleanup (bus drain) runs before the process exits.
func run() int {
	log.SetOutput(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var src source.Source
	switch kind := config.Str("SOURCE", "replay"); kind {
	case "replay":
		r, err := source.NewReplay(config.Str("REPLAY_FILE", "testdata/synthetic_day.ndjson"))
		if err != nil {
			log.Fatalf("replay: %v", err)
		}
		src = r
	case "sourcearena":
		src = source.NewSourceArena(
			config.Str("SOURCEARENA_URL", "https://apis.sourcearena.ir/api/"),
			os.Getenv("SOURCEARENA_TOKEN"),
			config.Dur("HTTP_TIMEOUT", 10*time.Second))
	default:
		log.Fatalf("unknown SOURCE %q", kind)
	}

	allowSynthetic := config.Str("ALLOW_SYNTHETIC_ON_BUS", "") == "1"
	if allowSynthetic {
		log.Printf("collector: WARNING: ALLOW_SYNTHETIC_ON_BUS=1: synthetic (SYN*) data may be published to the bus; " +
			"only acceptable on a disposable local stack, never where the gateway or ClickHouse writer serve users (rule 5)")
	}
	var pub bus.Publisher
	var retry []time.Duration // NDJSON: a failed stdout write is not retried (it could tear a line)
	var guard func(*model.Snapshot) error
	switch kind := config.Str("BUS", "ndjson"); kind {
	case "ndjson":
		pub = bus.NewNDJSON(os.Stdout)
	case "nats":
		guard = func(s *model.Snapshot) error { return busGuard(s, allowSynthetic) }
		js, err := bus.ConnectJetStream(ctx, config.Str("NATS_URL", "nats://127.0.0.1:4222"), "collector", bus.DefaultStreams())
		if err != nil {
			log.Fatalf("collector: %v", err)
		}
		defer js.Close()
		go js.WatchLimits(ctx, 30*time.Second)
		pub = js
		retry = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}
	default:
		log.Fatalf("collector: unknown BUS %q (want ndjson or nats)", kind)
	}
	interval := config.Dur("POLL_INTERVAL", 5*time.Second)
	replayDelay := config.Dur("REPLAY_DELAY", 0)
	backoff := time.Second
	log.Printf("collector: source=%s interval=%s bus=%s", src.Name(), interval, config.Str("BUS", "ndjson"))

	for {
		batch, err := src.Fetch(ctx)
		if errors.Is(err, source.ErrDone) {
			log.Printf("collector: source exhausted")
			return 0
		}
		if err != nil {
			log.Printf("collector: fetch error: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for i := range batch {
			if guard != nil {
				if err := guard(&batch[i]); err != nil {
					log.Printf("collector: %v", err)
					return 1
				}
			}
			subj := bus.SubjSnapshot(batch[i].InsCode)
			if err := bus.Retry(ctx, retry, nil, func() error { return publishSnapshot(pub, subj, &batch[i]) }); err != nil {
				log.Printf("collector: publish: %v", err)
				return 1
			}
		}
		wait := interval
		if _, ok := src.(*source.Replay); ok {
			wait = replayDelay
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(wait):
			}
		} else if ctx.Err() != nil {
			return 0
		}
	}
}

// publishSnapshot publishes s; on JetStream with a message ID built from the instrument, source
// time and ingest time, so a retried publish that was in fact stored is not stored twice, while
// a re-poll of an unchanged snapshot (new ingest time) still reaches the engine.
func publishSnapshot(pub bus.Publisher, subj string, s *model.Snapshot) error {
	if idp, ok := pub.(interface {
		PublishID(subject, id string, v any) error
	}); ok {
		return idp.PublishID(subj, fmt.Sprintf("snap:%s:%d:%d", s.InsCode, s.SourceTime.UnixNano(), s.IngestTime.UnixNano()), s)
	}
	return pub.Publish(subj, s)
}

// busGuard keeps SYNTHETIC data off the shared bus (rule 5: SYN* never reaches users, and the
// bus feeds the gateway and the ClickHouse writer). Local experiments opt in explicitly with
// ALLOW_SYNTHETIC_ON_BUS=1, on a disposable local stack only.
func busGuard(s *model.Snapshot, allowSynthetic bool) error {
	if allowSynthetic {
		return nil
	}
	if s.Source == "synthetic" || strings.HasPrefix(s.InsCode, "SYN") || strings.HasPrefix(s.Symbol, "SYN") {
		return fmt.Errorf("refusing to publish synthetic instrument %s to the bus (set ALLOW_SYNTHETIC_ON_BUS=1 only on a disposable local stack)", s.InsCode)
	}
	return nil
}
