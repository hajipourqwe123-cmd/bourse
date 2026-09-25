// collector polls a data source and publishes canonical snapshots.
//
//	SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson collector > snaps.ndjson
//	SOURCE=sourcearena SOURCEARENA_TOKEN=… POLL_INTERVAL=5s collector
//	BUS=nats NATS_URL=nats://… collector     # publish to JetStream instead of stdout
//	                                         # (SYN* refused unless ALLOW_SYNTHETIC_ON_BUS=1)
//	COLLECT_WINDOW=08:30-13:00               # live sources poll only in this Tehran-time span ("always")
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
	"bourse/internal/tehran"
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
	_, isReplay := src.(*source.Replay)
	win, err := parseWindow(config.Str("COLLECT_WINDOW", "08:30-13:00"))
	if err != nil {
		log.Printf("collector: COLLECT_WINDOW: %v", err)
		return 1
	}
	if isReplay {
		win = window{always: true} // a recording is replayed whatever the wall clock says
	}
	outside := false

	for {
		if !win.contains(time.Now()) {
			if !outside {
				log.Printf("collector: outside the collection window %s (Tehran); not polling", win)
				outside = true
			}
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(interval):
			}
			continue
		}
		if outside {
			log.Printf("collector: collection window %s open; polling", win)
			outside = false
		}
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
	if model.IsSynthetic(s) {
		return fmt.Errorf("refusing to publish synthetic instrument %s to the bus (set ALLOW_SYNTHETIC_ON_BUS=1 only on a disposable local stack)", s.InsCode)
	}
	return nil
}

// window is the daily Tehran-time span in which the collector polls a live source
// (COLLECT_WINDOW="HH:MM-HH:MM", or "always"). It bounds the bus volume that the stream sizing
// in bus.DefaultStreams assumes (08:30-13:00 by default).
type window struct {
	from, to time.Duration // since Tehran midnight, [from, to)
	always   bool
}

func parseWindow(v string) (window, error) {
	if v == "always" {
		return window{always: true}, nil
	}
	a, b, ok := strings.Cut(v, "-")
	parse := func(x string) (time.Duration, error) {
		t, err := time.Parse("15:04", strings.TrimSpace(x))
		if err != nil {
			return 0, err
		}
		return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
	}
	from, err1 := parse(a)
	to, err2 := parse(b)
	if !ok || err1 != nil || err2 != nil || to <= from {
		return window{}, fmt.Errorf("%q: want HH:MM-HH:MM (Tehran, start before end) or always", v)
	}
	return window{from: from, to: to}, nil
}

func (w window) contains(t time.Time) bool {
	if w.always {
		return true
	}
	since := t.Sub(tehran.DayStart(t))
	return since >= w.from && since < w.to
}

func (w window) String() string {
	if w.always {
		return "always"
	}
	f := func(d time.Duration) string { return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60) }
	return f(w.from) + "-" + f(w.to)
}
