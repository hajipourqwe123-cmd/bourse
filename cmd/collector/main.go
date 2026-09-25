// collector polls a data source and publishes canonical snapshots.
//
//	SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson collector > snaps.ndjson
//	SOURCE=sourcearena SOURCEARENA_TOKEN=… POLL_INTERVAL=5s collector
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"time"

	"bourse/internal/bus"
	"bourse/internal/config"
	"bourse/internal/source"
)

func main() {
	log.SetOutput(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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

	pub := bus.NewNDJSON(os.Stdout)
	interval := config.Dur("POLL_INTERVAL", 5*time.Second)
	replayDelay := config.Dur("REPLAY_DELAY", 0)
	backoff := time.Second
	log.Printf("collector: source=%s interval=%s", src.Name(), interval)

	for {
		batch, err := src.Fetch(ctx)
		if errors.Is(err, source.ErrDone) {
			log.Printf("collector: source exhausted")
			return
		}
		if err != nil {
			log.Printf("collector: fetch error: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for i := range batch {
			if err := pub.Publish(bus.SubjSnapshot(batch[i].InsCode), batch[i]); err != nil {
				log.Fatalf("publish: %v", err)
			}
		}
		wait := interval
		if _, ok := src.(*source.Replay); ok {
			wait = replayDelay
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		} else if ctx.Err() != nil {
			return
		}
	}
}
