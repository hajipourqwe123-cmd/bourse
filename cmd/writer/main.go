// writer (W-01) archives the bus in ClickHouse: md.snap.* → snapshots, flow.event.* → flow_events,
// flow.game.* → game_totals, flow.10m.* → flow_10m, quality.* → quality_issues
// (infra/clickhouse/001_schema.sql). One durable batch consumer per stream (writer-md,
// writer-flow, writer-quality); a batch is acked only after all its rows are inserted.
//
// Idempotent: every table is a ReplacingMergeTree keyed on the row's natural key with the bus
// sequence as version, so a redelivery, a crash between insert and ack, or an engine republish
// store each row once (read with FINAL). Synthetic data (SYN*, source synthetic or rebase:) is never
// stored (rule 5), and the writer refuses to run where ALLOW_SYNTHETIC_ON_BUS=1. A message that
// cannot be stored is recorded as an UNDECODABLE quality issue and acked; a ClickHouse failure is
// retried, then the writer exits non-zero with the batch unacked (redelivered to the next run).
//
//	NATS_URL=nats://127.0.0.1:4222
//	CLICKHOUSE_URL=http://127.0.0.1:8123  CLICKHOUSE_USER=dev  CLICKHOUSE_PASSWORD=…  CLICKHOUSE_DB=market
//	WRITER_BATCH=2000  WRITER_MAX_WAIT=1s  WRITER_ACK_WAIT=2m   # > worst-case insert incl. retries (~31s)
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, len(consumers))
	for _, spec := range consumers {
		spec.MaxBatch, spec.MaxWait, spec.AckWait = cfg.Batch, cfg.MaxWait, cfg.AckWait
		w := &writer{sink: ch, retry: cfg.Retry}
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
	return err
}
