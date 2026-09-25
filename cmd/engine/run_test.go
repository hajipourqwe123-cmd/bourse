package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"bourse/internal/bus"
	"bourse/internal/flow"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

func startServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// max_file_store is an accounting limit (the default streams reserve 26 GiB of MaxBytes);
	// it is only honoured from a config file, like infra/nats/nats.conf.
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
		t.Fatal("nats server not ready")
	}
	t.Cleanup(s.Shutdown)
	return s.ClientURL()
}

func connectJS(t *testing.T, url string) *bus.JetStream {
	t.Helper()
	j, err := bus.ConnectJetStream(context.Background(), url, "test", bus.DefaultStreams())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

// day builds a deterministic stream of consistent snapshots: nSym instruments, every 5s from
// 09:00 Tehran on the given day, with occasional large new real-person buyers/sellers so hot
// money, game totals, 10-minute windows and radar events are all produced.
func day(date string, nSym, steps int, seed int64) []model.Snapshot {
	d, _ := time.ParseInLocation("2006-01-02", date, tehran.Loc)
	rng := rand.New(rand.NewSource(seed))
	cur := make([]model.Snapshot, nSym)
	for i := range cur {
		cur[i] = model.Snapshot{InsCode: fmt.Sprintf("SYNTEST%04d", i), Symbol: fmt.Sprintf("SYN-T%d", i),
			Source: "synthetic", PriceYesterday: 10_000, PriceFirst: 10_000, PriceLast: 10_000}
	}
	var out []model.Snapshot
	for k := 0; k < steps; k++ {
		t := d.Add(9*time.Hour + time.Duration(k)*5*time.Second)
		for i := range cur {
			s := &cur[i]
			s.PriceLast += int64(rng.Intn(21) - 10)
			vol := int64(rng.Intn(3000) + 100)
			indBuy, indSell := vol*int64(50+rng.Intn(40))/100, vol*int64(50+rng.Intn(40))/100
			nb, ns := int64(rng.Intn(3)), int64(rng.Intn(3))
			if rng.Intn(15) == 0 { // one large new buyer (~300M toman)
				big := int64(3e9) / s.PriceLast
				vol, indBuy, nb = vol+big, indBuy+big, nb+1
			}
			s.Volume += vol
			s.Value += vol * s.PriceLast
			s.TradeCount += int64(rng.Intn(9) + 1)
			s.IndBuyVol += indBuy
			s.InstBuyVol += vol - indBuy
			s.IndSellVol += indSell
			s.InstSellVol += vol - indSell
			s.IndBuyCount += nb
			s.IndSellCount += ns
			s.SourceTime = t
			s.IngestTime = t.Add(700 * time.Millisecond)
			out = append(out, *s)
		}
	}
	return out
}

// feed publishes snapshots, or raw (malformed) payloads, to md.snap.<ins>.
func feed(t *testing.T, url string, js *bus.JetStream, items []any) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	jsc, _ := nc.JetStream()
	for _, it := range items {
		switch v := it.(type) {
		case model.Snapshot:
			err = js.Publish(bus.SubjSnapshot(v.InsCode), v)
		case rawMsg:
			_, err = jsc.Publish(bus.SubjSnapshot(v.ins), []byte(v.data))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

type rawMsg struct{ ins, data string }

// runUntil runs a fresh processor over NATS until the durable's ack floor reaches floor.
// It returns runNATS's error.
func runUntil(t *testing.T, js *bus.JetStream, pub bus.Publisher, floor uint64) error {
	t.Helper()
	p := newProcessor(flow.DefaultConfig(), pub)
	p.retry = []time.Duration{time.Millisecond, time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if f, _ := js.AckFloor(ctx, bus.StreamMD, engineDurable); f >= floor {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	spec := engineConsumer()
	spec.AckWait, spec.Backoff = 300*time.Millisecond, []time.Duration{10 * time.Millisecond}
	err := p.runNATS(ctx, js, spec, false)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("processor did not reach the ack floor in time")
	}
	return err
}

// outputs returns every stored engine output as sorted "subject payload-sha256" lines, plus
// the last flow.game payload per instrument.
func outputs(t *testing.T, url string) (lines []string, game map[string]model.GameTotals) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	game = map[string]model.GameTotals{}
	for _, subj := range []string{"flow.>", "ai.signal.>", "quality.>"} {
		jsc, _ := nc.JetStream()
		s, err := jsc.SubscribeSync(subj, nats.OrderedConsumer())
		if err != nil {
			t.Fatal(err)
		}
		for {
			m, err := s.NextMsg(300 * time.Millisecond)
			if errors.Is(err, nats.ErrTimeout) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data := m.Data
			if strings.HasPrefix(m.Subject, "quality.") {
				// UNDECODABLE carries the stream store time, which differs between two
				// independently fed servers; everything else must match byte for byte.
				var iss model.QualityIssue
				if json.Unmarshal(data, &iss) == nil && iss.Code == quality.Undecodable {
					iss.At = time.Time{}
					data, _ = json.Marshal(iss)
				}
			}
			h := sha256.Sum256(data)
			lines = append(lines, m.Subject+" "+hex.EncodeToString(h[:]))
			if strings.HasPrefix(m.Subject, "flow.game.") {
				var g model.GameTotals
				if err := json.Unmarshal(m.Data, &g); err != nil {
					t.Fatal(err)
				}
				game[g.InsCode] = g
			}
		}
		s.Unsubscribe()
	}
	sort.Strings(lines)
	return lines, game
}

// scenario feeds the same input into a fresh server and runs fn, returning the outputs.
func scenario(t *testing.T, input []any, fn func(js *bus.JetStream, n uint64)) ([]string, map[string]model.GameTotals) {
	url := startServer(t)
	js := connectJS(t, url)
	feed(t, url, js, input)
	fn(js, uint64(len(input)))
	return outputs(t, url)
}

func testInput() []any {
	var in []any
	for _, s := range day("2026-09-22", 3, 40, 1) { // previous day: must not leak into today's totals
		in = append(in, s)
	}
	for i, s := range day("2026-09-23", 3, 200, 2) {
		in = append(in, s)
		if i == 250 {
			in = append(in, rawMsg{"SYNTEST0001", `{"ins_code": 12`})
		}
	}
	return in
}

// Restart mid-day: the second process rebuilds state from the stream and the combined output
// equals an uninterrupted run exactly (game totals, flow events, windows, radar, quality).
func TestRestartMidDayEqualsUninterruptedRun(t *testing.T) {
	in := testInput()
	wantLines, wantGame := scenario(t, in, func(js *bus.JetStream, n uint64) {
		if err := runUntil(t, js, js, n); err != nil {
			t.Fatal(err)
		}
	})
	if len(wantGame) != 3 {
		t.Fatalf("baseline produced game totals for %d instruments, want 3", len(wantGame))
	}
	for _, g := range wantGame {
		if g.Day != "2026-09-23" || g.NetHot == 0 {
			t.Fatalf("baseline game totals look wrong: %+v", g)
		}
	}
	// Seqs 1..120 are the previous day, 121.. today; the malformed message is seq 372.
	// Cuts are approximate (the first process may run a few messages past them).
	for _, cut := range []uint64{60, 300, 580} {
		gotLines, gotGame := scenario(t, in, func(js *bus.JetStream, n uint64) {
			if err := runUntil(t, js, js, cut); err != nil {
				t.Fatal(err)
			}
			if err := runUntil(t, js, js, n); err != nil { // restart: fresh in-memory state
				t.Fatal(err)
			}
		})
		for ins, g := range wantGame {
			if gotGame[ins] != g {
				t.Errorf("cut %d: %s game totals\n got %+v\nwant %+v", cut, ins, gotGame[ins], g)
			}
		}
		if strings.Join(gotLines, "\n") != strings.Join(wantLines, "\n") {
			t.Errorf("cut %d: outputs differ from uninterrupted run (%d vs %d messages)", cut, len(gotLines), len(wantLines))
		}
	}
}

// failingPub starts failing, permanently, at the first output with index > 0 (so part of that
// snapshot's outputs are already stored) once okLeft publishes have succeeded.
type failingPub struct {
	*bus.JetStream
	okLeft   atomic.Int64
	failedID atomic.Value
}

func (f *failingPub) PublishID(subject, id string, v any) error {
	if f.failedID.Load() != nil || f.okLeft.Add(-1) < 0 && !strings.HasSuffix(id, ":0") {
		f.failedID.CompareAndSwap(nil, id)
		return errors.New("nats down")
	}
	return f.JetStream.PublishID(subject, id, v)
}

// A publish failure after Process(): retries, then aborts without acking; the next process
// recovers state up to the ack floor, gets the snapshot redelivered, recomputes the same outputs
// and republishes them under the same message IDs, so partial output is not duplicated.
func TestPublishFailureAbortsAndNextProcessCompletes(t *testing.T) {
	in := testInput()
	wantLines, wantGame := scenario(t, in, func(js *bus.JetStream, n uint64) {
		if err := runUntil(t, js, js, n); err != nil {
			t.Fatal(err)
		}
	})
	gotLines, gotGame := scenario(t, in, func(js *bus.JetStream, n uint64) {
		fp := &failingPub{JetStream: js}
		fp.okLeft.Store(700) // dies part-way through some snapshot's outputs
		err := runUntil(t, js, fp, n)
		if err == nil || !strings.Contains(err.Error(), "nats down") {
			t.Fatalf("want abort with publish error, got %v", err)
		}
		if id, _ := fp.failedID.Load().(string); id == "" || strings.HasSuffix(id, ":0") {
			t.Fatalf("failure must hit a later output of a snapshot, failed at %q", id)
		}
		floor, _ := js.AckFloor(context.Background(), bus.StreamMD, engineDurable)
		if floor == 0 || floor >= n {
			t.Fatalf("ack floor %d: the failed snapshot must stay unacked", floor)
		}
		if err := runUntil(t, js, js, n); err != nil {
			t.Fatal(err)
		}
	})
	for ins, g := range wantGame {
		if gotGame[ins] != g {
			t.Errorf("%s game totals\n got %+v\nwant %+v", ins, gotGame[ins], g)
		}
	}
	if strings.Join(gotLines, "\n") != strings.Join(wantLines, "\n") {
		t.Errorf("outputs differ from uninterrupted run (%d vs %d messages)", len(gotLines), len(wantLines))
	}
}

// Malformed snapshots are terminated with exactly one UNDECODABLE quality issue each and do
// not block, or leak zero-filled values into, the next valid snapshot.
func TestMalformedSnapshotsReportedAndSkipped(t *testing.T) {
	url := startServer(t)
	js := connectJS(t, url)
	snaps := day("2026-09-23", 1, 3, 3)
	other, _ := json.Marshal(day("2026-09-23", 2, 1, 3)[1]) // SYNTEST0001 published on SYNTEST0000's subject
	bad := []string{`not json`, `{}`, `null`, `{"ins_code":"SYNTEST0000"}`, string(other)}
	in := []any{snaps[0]}
	for _, b := range bad {
		in = append(in, rawMsg{"SYNTEST0000", b})
	}
	in = append(in, snaps[1], snaps[2])
	feed(t, url, js, in)
	if err := runUntil(t, js, js, uint64(len(in))); err != nil {
		t.Fatal(err)
	}
	nc, _ := nats.Connect(url)
	defer nc.Close()
	jsc, _ := nc.JetStream()
	sub, err := jsc.SubscribeSync("quality.>", nats.OrderedConsumer())
	if err != nil {
		t.Fatal(err)
	}
	var undecodable []string
	for {
		m, err := sub.NextMsg(300 * time.Millisecond)
		if err != nil {
			break
		}
		var iss model.QualityIssue
		json.Unmarshal(m.Data, &iss)
		if iss.Code == quality.Undecodable {
			if m.Subject != "quality.SYNTEST0000" || iss.At.IsZero() {
				t.Errorf("bad issue on %s: %+v", m.Subject, iss)
			}
			undecodable = append(undecodable, iss.Detail)
		} else if iss.Code != quality.Stale {
			t.Errorf("unexpected issue (zero-filled snapshot reached the engine?): %+v", iss)
		}
	}
	if len(undecodable) != len(bad) {
		t.Fatalf("%d UNDECODABLE issues, want %d: %q", len(undecodable), len(bad), undecodable)
	}
	for i, d := range undecodable {
		if !strings.Contains(d, fmt.Sprintf("stream seq %d:", i+2)) {
			t.Errorf("issue %d detail %q", i, d)
		}
	}
	g, err := jsc.GetLastMsg(bus.StreamFlow, "flow.game.SYNTEST0000")
	if err != nil {
		t.Fatalf("no game totals after the malformed messages: %v", err)
	}
	var gt model.GameTotals
	json.Unmarshal(g.Data, &gt)
	if gt.Day != "2026-09-23" {
		t.Fatalf("game totals %+v", gt)
	}
}

type downPub struct{}

func (downPub) Publish(string, any) error           { return errors.New("nats down") }
func (downPub) PublishID(string, string, any) error { return errors.New("nats down") }

// Deliveries count across processes: after 4 aborted runs the 5th process reports
// POISON_SUSPECT once and stops; later processes stop too. The message is never dropped.
func TestPoisonSuspectAcrossRestarts(t *testing.T) {
	url := startServer(t)
	js := connectJS(t, url)
	snaps := day("2026-09-23", 1, 2, 4)
	feed(t, url, js, []any{snaps[0], snaps[1]})
	for run := 1; run <= 4; run++ {
		if err := runUntil(t, js, downPub{}, 2); err == nil || !strings.Contains(err.Error(), "nats down") {
			t.Fatalf("run %d: want publish abort, got %v", run, err)
		}
	}
	for run := 5; run <= 6; run++ {
		err := runUntil(t, js, js, 2)
		if err == nil || !strings.Contains(err.Error(), "POISON_SUSPECT: MD seq 2") { // seq 1 is a baseline: no outputs, acked
			t.Fatalf("run %d: want POISON_SUSPECT abort, got %v", run, err)
		}
	}
	if f, _ := js.AckFloor(context.Background(), bus.StreamMD, engineDurable); f != 1 {
		t.Fatalf("poisoned message acked/dropped: ack floor %d", f)
	}
	nc, _ := nats.Connect(url)
	defer nc.Close()
	jsc, _ := nc.JetStream()
	info, err := jsc.StreamInfo(bus.StreamQuality)
	if err != nil {
		t.Fatal(err)
	}
	m, err := jsc.GetLastMsg(bus.StreamQuality, "quality.SYNTEST0000")
	if err != nil || info.State.Msgs != 1 || !strings.Contains(string(m.Data), quality.PoisonSuspect) {
		t.Fatalf("want exactly one POISON_SUSPECT issue (deduplicated across runs 5 and 6): msgs=%d err=%v", info.State.Msgs, err)
	}
}
