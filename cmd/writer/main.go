// writer (W-01) archives the bus in ClickHouse: md.snap.* → snapshots, flow.event.* → flow_events,
// flow.game.* → game_totals, flow.10m.* → flow_10m, quality.* → quality_issues
// (infra/clickhouse/001_schema.sql). One durable batch consumer per stream (writer-md,
// writer-flow, writer-quality); a batch is acked only after all its rows are inserted.
//
// Idempotent: every table is a ReplacingMergeTree keyed on the row's natural key, versioned by the
// message's stored time, so a redelivery, a crash between insert and ack, or an engine republish
// store each row once (read with FINAL). Synthetic data is never stored (rule 5): the writer refuses
// to run where ALLOW_SYNTHETIC_ON_BUS=1 and STOPS (batch unacked) on the first synthetic message
// on the bus (SYN*, source synthetic or rebase:), since engine outputs of a re-timed recording
// carry real instrument codes and no source; flow and quality batches wait until writer-md has
// checked every snapshot that could have produced them (mdGate). The stop is latched in the
// service_state KV (writer_synthetic_stop): a restarted writer refuses to run until an operator
// purges the streams and starts it once with WRITER_CLEAR_SYNTHETIC_STOP=1. A message that
// cannot be stored is recorded as an UNDECODABLE quality issue and acked; a ClickHouse failure is
// retried, then the writer exits non-zero with the batch unacked (redelivered to the next run).
//
//	NATS_URL=nats://127.0.0.1:4222
//	CLICKHOUSE_URL=http://127.0.0.1:8123  CLICKHOUSE_USER=dev  CLICKHOUSE_PASSWORD=…  CLICKHOUSE_DB=market
//	WRITER_BATCH=2000  WRITER_MAX_WAIT=1s  WRITER_ACK_WAIT=2m   # > worst-case insert incl. retries (~31s)
//	WRITER_CLEAR_SYNTHETIC_STOP=1        # operator: clear the synthetic-data stop latch (after purging)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bourse/internal/bus"
	"bourse/internal/config"
)

// Config of one writer process.
type Config struct {
	NATSURL                    string
	CHURL, CHDB, CHUser, CHKey string // CHKey: secret
	Batch                      int
	MaxWait                    time.Duration
	AckWait                    time.Duration // an unacked batch is redelivered after it
	Retry                      []time.Duration
	ClearSyntheticStop         bool // WRITER_CLEAR_SYNTHETIC_STOP=1: operator, after purging the streams
}

func loadConfig() Config {
	return Config{
		NATSURL: config.Str("NATS_URL", "nats://127.0.0.1:4222"),
		CHURL:   config.Str("CLICKHOUSE_URL", "http://127.0.0.1:8123"),
		CHDB:    config.Str("CLICKHOUSE_DB", "market"),
		CHUser:  config.Str("CLICKHOUSE_USER", "dev"),
		CHKey:   os.Getenv("CLICKHOUSE_PASSWORD"),
		Batch:   int(config.Int("WRITER_BATCH", 2000)),
		MaxWait: config.Dur("WRITER_MAX_WAIT", time.Second),
		AckWait: config.Dur("WRITER_ACK_WAIT", 2*time.Minute),
		Retry:   []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second},

		ClearSyntheticStop: os.Getenv("WRITER_CLEAR_SYNTHETIC_STOP") == "1",
	}
}

// consumers: one durable per stream, each covering the whole stream.
var consumers = []bus.BatchSpec{
	{Stream: bus.StreamMD, Durable: "writer-md", Filter: "md.snap.>"},
	{Stream: bus.StreamFlow, Durable: "writer-flow", Filter: "flow.>"},
	{Stream: bus.StreamQuality, Durable: "writer-quality", Filter: "quality.>"},
}

func main() {
	log.SetOutput(os.Stderr)
	if os.Getenv("ALLOW_SYNTHETIC_ON_BUS") == "1" {
		log.Fatal("writer: refusing to run with ALLOW_SYNTHETIC_ON_BUS=1: a synthetic stack must never reach ClickHouse (rule 5)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, loadConfig(), nil); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("writer: %v", err)
	}
}

// run consumes until ctx ends or a consumer fails. hook (tests) wraps each stream's writer.
func run(ctx context.Context, cfg Config, hook func(stream string, w *writer)) error {
	ch := newClickHouse(cfg.CHURL, cfg.CHDB, cfg.CHUser, cfg.CHKey)
	if err := ch.checkSchema(ctx); err != nil {
		return err
	}
	js, err := bus.DialJetStream(ctx, cfg.NATSURL, "writer", bus.DefaultStreams())
	if err != nil {
		return err
	}
	defer js.Close()
	if cfg.ClearSyntheticStop {
		if err := js.StateDelete(ctx, syntheticStopKey); err != nil {
			return err
		}
		log.Printf("writer: WARNING: synthetic-data stop cleared by the operator (%s)", syntheticStopKey)
	}
	if v, ok, err := js.StateGet(ctx, syntheticStopKey); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("%w: stopped earlier on %s. Purge the MD, FLOW and QUALITY streams of the synthetic run, "+
			"then start once with WRITER_CLEAR_SYNTHETIC_STOP=1", errSynthetic, v)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, len(consumers))
	gate := &mdGate{}
	for _, spec := range consumers {
		spec.MaxBatch, spec.MaxWait, spec.AckWait = cfg.Batch, cfg.MaxWait, cfg.AckWait
		w := &writer{sink: ch, retry: cfg.Retry}
		if spec.Stream == bus.StreamMD {
			spec.Settled = gate.settled
		} else {
			w.gate = gate // rule 5: never ahead of the synthetic check on MD (mdGate)
		}
		if hook != nil {
			hook(spec.Stream, w)
		}
		go func() {
			err := js.ConsumeBatch(ctx, spec, func(msgs []bus.Msg) error { return w.handle(ctx, msgs) })
			if err != nil && ctx.Err() == nil {
				err = fmt.Errorf("%s: %w", spec.Durable, err)
			}
			errc <- err
		}()
	}
	log.Printf("writer: consuming %d streams into ClickHouse %s", len(consumers), cfg.CHDB)
	err = <-errc
	cancel()
	for range consumers[1:] {
		<-errc
	}
	if errors.Is(err, errSynthetic) {
		// Latched: a restart must not resume while the synthetic batch awaits redelivery (the gate
		// would otherwise be the only guard) — an operator purges the streams and clears it.
		lctx, lcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer lcancel()
		if perr := js.StatePut(lctx, syntheticStopKey, []byte(time.Now().UTC().Format(time.RFC3339)+" "+err.Error())); perr != nil {
			return errors.Join(err, fmt.Errorf("latch %s: %w", syntheticStopKey, perr))
		}
	}
	return err
}

// syntheticStopKey (bus.StateBucket) latches a stop on synthetic data across restarts.
const syntheticStopKey = "writer_synthetic_stop"
