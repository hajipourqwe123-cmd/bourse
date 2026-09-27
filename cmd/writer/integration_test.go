package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"bourse/internal/bus"
	"bourse/internal/market"
	"bourse/internal/model"
	"bourse/internal/quality"
)

// The restart + redelivery test needs a real ClickHouse (make up): it creates and drops its own
// database, never touching `market`.
//
//	WRITER_TEST_CLICKHOUSE_URL=http://127.0.0.1:8123 WRITER_TEST_CLICKHOUSE_PASSWORD=… go test ./cmd/writer
func testClickHouse(t *testing.T) *clickhouse {
	t.Helper()
	u := os.Getenv("WRITER_TEST_CLICKHOUSE_URL")
	if u == "" {
		t.Skip("WRITER_TEST_CLICKHOUSE_URL not set (needs `make up`)")
	}
	db := fmt.Sprintf("w01_test_%d", time.Now().UnixNano())
	ch := newClickHouse(u, db, envOr("WRITER_TEST_CLICKHOUSE_USER", "dev"), os.Getenv("WRITER_TEST_CLICKHOUSE_PASSWORD"))
	ctx := context.Background()
	if _, err := ch.do(ctx, "CREATE DATABASE "+db, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = ch.do(context.Background(), "DROP DATABASE IF EXISTS "+db, nil) })
	ddl, err := os.ReadFile(filepath.Join("..", "..", "infra", "clickhouse", "001_schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var code []string // comments stripped first: they contain semicolons
	for _, l := range strings.Split(string(ddl), "\n") {
		l, _, _ = strings.Cut(l, "--")
		code = append(code, l)
	}
	for _, stmt := range strings.Split(strings.Join(code, "\n"), ";") {
		q := strings.TrimSpace(stmt)
		if q == "" {
			continue
		}
		if _, err := ch.do(ctx, strings.ReplaceAll(q, "market.", db+"."), nil); err != nil {
			t.Fatalf("DDL: %v", err)
		}
	}
	return ch
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func startNATS(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "nats.conf")
	if err := os.WriteFile(conf, []byte(fmt.Sprintf("listen: \"127.0.0.1:-1\"\njetstream { store_dir: %q, max_file_store: 64G }\n", dir)), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	opts.NoLog, opts.NoSigs = true, true
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(s.Shutdown)
	return s.ClientURL()
}

func (c *clickhouse) count(t *testing.T, q string) int {
	t.Helper()
	out, err := c.do(context.Background(), strings.ReplaceAll(q, "{db}", c.db)+" FORMAT TSV", nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	fmt.Sscan(strings.TrimSpace(string(out)), &n)
	return n
}

func (c *clickhouse) value(t *testing.T, q string) string {
	t.Helper()
	out, err := c.do(context.Background(), strings.ReplaceAll(q, "{db}", c.db)+" FORMAT TSV", nil)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// waitAcked waits until every writer durable has acked its whole stream.
func waitAcked(t *testing.T, js *bus.JetStream) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(20 * time.Second)
	for _, c := range consumers {
		for {
			st, err := js.StreamState(ctx, c.Stream)
			if err != nil {
				t.Fatal(err)
			}
			floor, err := js.AckFloor(ctx, c.Stream, c.Durable)
			if err != nil {
				t.Fatal(err)
			}
			if floor >= st.LastSeq {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: ack floor %d, stream last %d", c.Durable, floor, st.LastSeq)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestRestartAndRedelivery: a writer crashes after inserting a batch but before acking it; the
// restarted writer receives the batch again and inserts it again; then the engine republishes
// outputs (after the dedup window: no message ID) and re-emits DAY_START_MISSED. Every key is
// stored exactly once (FINAL, and after a forced merge), with the latest values, partial and class.
func TestRestartAndRedelivery(t *testing.T) {
	ch := testClickHouse(t)
	natsURL := startNATS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	js, err := bus.ConnectJetStream(ctx, natsURL, "test-producer", bus.DefaultStreams())
	if err != nil {
		t.Fatal(err)
	}
	defer js.Close()
	at := tehranAt("09:00")
	pub := func(subj string, v any) {
		t.Helper()
		if err := js.Publish(subj, v); err != nil {
			t.Fatal(err)
		}
	}
	engineOutputs := func(netHot int64) {
		pub(bus.SubjFlow("S1"), model.FlowEvent{InsCode: "S1", Symbol: "ش۱", Class: market.Stock, Side: model.Buy, Band: model.BandHot,
			Attribution: model.Attributed, IntervalFrom: at, IntervalTo: at.Add(5 * time.Second), Value: 3e9, Participants: 1})
		pub(bus.SubjGame("S1"), model.GameTotals{InsCode: "S1", Class: market.Stock, Day: "2026-09-23", AsOf: at, NetHot: netHot, Partial: true})
		pub(bus.SubjWindow("S1"), model.TenMinute{InsCode: "S1", Class: market.Stock, WindowStart: at, NetHot: netHot, Partial: true})
		pub(bus.SubjQuality("S1"), model.QualityIssue{InsCode: "S1", Code: quality.DayStartMissed,
			Detail: fmt.Sprintf("late baseline (emission %d)", netHot), At: at.Add(time.Duration(netHot) * time.Minute)})
	}
	for i := 0; i < 3; i++ {
		pub(bus.SubjSnapshot("S1"), model.Snapshot{InsCode: "S1", Symbol: "ش۱", Source: "sourcearena",
			SourceTime: at.Add(time.Duration(i) * 5 * time.Second), IngestTime: at.Add(time.Duration(i)*5*time.Second + time.Second), Volume: int64(i)})
	}
	pub(bus.SubjSnapshot("SYN1"), model.Snapshot{InsCode: "SYN1", Source: "synthetic", SourceTime: at})
	engineOutputs(1)
	pub(bus.SubjQuality("S1"), model.QualityIssue{InsCode: "S1", Code: quality.Stale, Detail: "40s", At: at})

	cfg := Config{NATSURL: natsURL, CHURL: ch.base, CHDB: ch.db, CHUser: ch.user, CHKey: ch.pass,
		Batch: 100, MaxWait: 200 * time.Millisecond, AckWait: 2 * time.Second, Retry: []time.Duration{10 * time.Millisecond}}

	// Run 1: every stream's first batch is inserted, then the process "crashes" before the ack.
	// A barrier makes all three streams insert before any crashes (the first crash stops the others).
	var crashes atomic.Int32
	all := make(chan struct{})
	crash := errors.New("crash between insert and ack")
	err = run(ctx, cfg, func(_ string, w *writer) {
		w.afterInsert = func() error {
			if crashes.Add(1) == int32(len(consumers)) {
				close(all)
			}
			select {
			case <-all:
			case <-time.After(10 * time.Second):
			}
			return bus.Abort(crash)
		}
	})
	if !errors.Is(err, crash) {
		t.Fatalf("run 1 = %v, want the simulated crash", err)
	}
	if n := ch.count(t, "SELECT count() FROM {db}.snapshots"); n != 3 {
		t.Fatalf("run 1 inserted %d snapshots before crashing, want 3", n)
	}

	// Run 2 (restart): the unacked batches are redelivered and inserted again.
	cfg2 := cfg
	rctx, rcancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- run(rctx, cfg2, nil) }()
	// The engine republishes after the dedup window (new bus sequences, same keys), with newer
	// totals, and re-emits DAY_START_MISSED at a later time (engine recovery).
	time.Sleep(300 * time.Millisecond)
	engineOutputs(2)
	waitAcked(t, js)
	rcancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("run 2: %v", err)
	}

	raw := ch.count(t, "SELECT count() FROM {db}.snapshots")
	if raw <= 3 {
		t.Errorf("snapshots stored %d times: the redelivery was not exercised", raw)
	}
	for q, n := range map[string]int{
		"snapshots":   3, // SYN1 never stored
		"flow_events": 1,
		"game_totals": 1,
		"flow_10m":    1,
		"quality_issues FINAL WHERE code = 'DAY_START_MISSED'": 1,
		"quality_issues FINAL WHERE code = 'STALE'":            1,
	} {
		if !strings.Contains(q, "FINAL") {
			q += " FINAL"
		}
		if got := ch.count(t, "SELECT count() FROM {db}."+q); got != n {
			t.Errorf("%s: %d rows, want %d", q, got, n)
		}
	}
	if v := ch.value(t, "SELECT net_hot, partial, class FROM {db}.game_totals FINAL"); v != "2\ttrue\tstock" {
		t.Errorf("game_totals = %q, want the latest publication (net_hot 2), partial, class stock", v)
	}
	if v := ch.value(t, "SELECT net_hot, partial, class FROM {db}.flow_10m FINAL"); v != "2\ttrue\tstock" {
		t.Errorf("flow_10m = %q", v)
	}
	if v := ch.value(t, "SELECT class FROM {db}.flow_events FINAL"); v != "stock" {
		t.Errorf("flow_events class = %q", v)
	}
	for _, table := range []string{"snapshots", "flow_events", "game_totals", "flow_10m", "quality_issues"} {
		if _, err := ch.do(context.Background(), "OPTIMIZE TABLE "+ch.db+"."+table+" FINAL", nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := ch.count(t, "SELECT count() FROM {db}.snapshots"); n != 3 {
		t.Errorf("after merge: %d snapshots, want 3", n)
	}
	if n := ch.count(t, "SELECT count() FROM {db}.quality_issues WHERE code = 'DAY_START_MISSED'"); n != 1 {
		t.Errorf("after merge: %d DAY_START_MISSED rows, want 1", n)
	}
	if crashes.Load() != int32(len(consumers)) {
		t.Errorf("crash hook ran %d times, want once per stream", crashes.Load())
	}
}
