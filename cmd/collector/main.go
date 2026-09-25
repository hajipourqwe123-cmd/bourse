// collector polls a data source and publishes canonical snapshots.
//
//	SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson collector > snaps.ndjson
//	SOURCE=sourcearena SOURCEARENA_TOKEN=… POLL_INTERVAL=5s collector
//	BUS=nats NATS_URL=nats://… collector     # publish to JetStream instead of stdout
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bourse/internal/bus"
	"bourse/internal/config"
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

	var pub bus.Publisher
	var retry []time.Duration // NDJSON: a failed stdout write is not retried (it could tear a line)
	switch kind := config.Str("BUS", "ndjson"); kind {
	case "ndjson":
		pub = bus.NewNDJSON(os.Stdout)
	case "nats":
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
			subj := bus.SubjSnapshot(batch[i].InsCode)
			if err := bus.Retry(retry, nil, func() error { return pub.Publish(subj, batch[i]) }); err != nil {
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
