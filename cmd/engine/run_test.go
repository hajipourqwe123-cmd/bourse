package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
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
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true,
		StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
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
	spec := bus.ConsumerSpec{Stream: bus.StreamMD, Durable: engineDurable, Filter: snapFilter,
		AckWait: 300 * time.Millisecond, MaxDeliver: 3, Backoff: []time.Duration{10 * time.Millisecond}}
	err := p.runNATS(ctx, js, spec)
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
	for _, cut := range []uint64{121, 400, 580} { // prev day; today mid-morning; after the malformed msg
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

// failingPub fails every publish once armed and the countdown of successful publishes is spent.
type failingPub struct {
	*bus.JetStream
	okLeft atomic.Int64
}

func (f *failingPub) PublishID(subject, id string, v any) error {
	if f.okLeft.Add(-1) < 0 {
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

// A malformed snapshot is terminated with one UNDECODABLE quality issue and does not block
// the next valid snapshot.
func TestMalformedSnapshotReportedAndSkipped(t *testing.T) {
	url := startServer(t)
	js := connectJS(t, url)
	snaps := day("2026-09-23", 1, 3, 3)
	feed(t, url, js, []any{snaps[0], rawMsg{"SYNTEST0000", `not json`}, snaps[1], snaps[2]})
	if err := runUntil(t, js, js, 4); err != nil {
		t.Fatal(err)
	}
	nc, _ := nats.Connect(url)
	defer nc.Close()
	jsc, _ := nc.JetStream()
	m, err := jsc.GetLastMsg(bus.StreamQuality, "quality.SYNTEST0000")
	if err != nil {
		t.Fatal(err)
	}
	var iss model.QualityIssue
	json.Unmarshal(m.Data, &iss)
	if iss.Code != quality.Undecodable || !strings.Contains(iss.Detail, "stream seq 2") || iss.At.IsZero() {
		t.Fatalf("issue %+v", iss)
	}
	g, err := jsc.GetLastMsg(bus.StreamFlow, "flow.game.SYNTEST0000")
	if err != nil {
		t.Fatalf("no game totals after the malformed message: %v", err)
	}
	if g.Sequence == 0 {
		t.Fatal("game totals missing")
	}
}
