package bus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
)

// startServer runs an in-process JetStream server in a temp dir and returns its client URL.
func startServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// max_file_store is an accounting limit (the default streams reserve 37 GiB of MaxBytes);
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

func connect(t *testing.T, streams []StreamSpec) *JetStream {
	t.Helper()
	j, err := ConnectJetStream(context.Background(), startServer(t), "test", streams)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// captureLog redirects the standard logger for the test.
func captureLog(t *testing.T) *syncBuf {
	b := &syncBuf{}
	prev := log.Writer()
	log.SetOutput(b)
	t.Cleanup(func() { log.SetOutput(prev) })
	return b
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func streamInfo(t *testing.T, j *JetStream, name string) *jetstream.StreamInfo {
	t.Helper()
	s, err := j.js.Stream(ctxT(t), name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestEnsureStreamsIdempotent(t *testing.T) {
	j := connect(t, DefaultStreams())
	if err := j.EnsureStreams(ctxT(t)); err != nil {
		t.Fatalf("second EnsureStreams: %v", err)
	}
	for _, spec := range DefaultStreams() {
		info := streamInfo(t, j, spec.Name)
		c := info.Config
		if strings.Join(c.Subjects, ",") != strings.Join(spec.Subjects, ",") || c.MaxBytes != spec.MaxBytes ||
			c.MaxAge != spec.MaxAge || c.Discard != jetstream.DiscardOld || c.Duplicates != DupWindow {
			t.Errorf("%s: config %+v does not match spec %+v", spec.Name, c, spec)
		}
	}
	// Every subject builder lands in some stream.
	for _, subj := range []string{SubjSnapshot("X"), SubjFlow("X"), SubjGame("X"), SubjWindow("X"), SubjQuality("X"), SubjAI("X")} {
		if err := j.Publish(subj, 1); err != nil {
			t.Errorf("%s: %v", subj, err)
		}
	}
}

func TestPublishPayloadEqualsNDJSONData(t *testing.T) {
	j := connect(t, DefaultStreams())
	v := struct {
		Name  string `json:"name"`
		Value int64  `json:"value"`
	}{"فولاد <a&b>", 9_223_372_036_854_775_807}
	if err := j.Publish(SubjSnapshot("IRO1"), v); err != nil {
		t.Fatal(err)
	}
	_, got, _, err := j.GetMsg(ctxT(t), StreamMD, 1)
	if err != nil {
		t.Fatal(err)
	}
	var line bytes.Buffer
	if err := NewNDJSON(&line).Publish(SubjSnapshot("IRO1"), v); err != nil {
		t.Fatal(err)
	}
	want := `{"subject":"md.snap.IRO1","data":` + string(got) + "}\n"
	if line.String() != want {
		t.Fatalf("NATS payload is not the NDJSON data:\nndjson: %s\nnats:   %s", line.String(), got)
	}
	if !bytes.Contains(got, []byte("<a&b>")) {
		t.Fatalf("HTML escaping applied: %s", got)
	}
}

func TestPublishUncoveredSubjectFails(t *testing.T) {
	j := connect(t, DefaultStreams())
	j.timeout = time.Second
	if err := j.Publish("nobody.listens", 1); err == nil {
		t.Fatal("publish to a subject without a stream must fail")
	}
}

func TestPublishIDDeduplicates(t *testing.T) {
	j := connect(t, DefaultStreams())
	for i := 0; i < 3; i++ {
		if err := j.PublishID(SubjFlow("A"), "eng:1:7:0", i); err != nil {
			t.Fatal(err)
		}
	}
	if n := streamInfo(t, j, StreamFlow).State.Msgs; n != 1 {
		t.Fatalf("stored %d messages, want 1", n)
	}
}

func publishN(t *testing.T, j *JetStream, payloads ...string) {
	t.Helper()
	for _, p := range payloads {
		if _, err := j.js.Publish(ctxT(t), SubjSnapshot("A"), []byte(p)); err != nil {
			t.Fatal(err)
		}
	}
}

// consumeUntil runs Consume until stop returns true (checked after each handled message).
func consumeUntil(t *testing.T, j *JetStream, spec ConsumerSpec, fn func(Msg) error, stop func() bool) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := j.Consume(ctx, spec, func(m Msg) error {
		err := fn(m)
		if stop() {
			defer cancel()
		}
		return err
	})
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("consume timed out")
	}
	return err
}

func mdSpec() ConsumerSpec {
	return ConsumerSpec{Stream: StreamMD, Durable: "engine", Filter: "md.snap.>",
		AckWait: 300 * time.Millisecond, MaxDeliver: 3, Backoff: []time.Duration{10 * time.Millisecond}}
}

func TestConsumeInOrderAndAcks(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "1", "2", "3", "4", "5")
	var got []string
	consumeUntil(t, j, mdSpec(), func(m Msg) error { got = append(got, string(m.Data)); return nil },
		func() bool { return len(got) == 5 })
	if strings.Join(got, "") != "12345" {
		t.Fatalf("order %v", got)
	}
	time.Sleep(50 * time.Millisecond) // let the last ack land
	if f, _ := j.AckFloor(ctxT(t), StreamMD, "engine"); f != 5 {
		t.Fatalf("ack floor %d, want 5", f)
	}
}

// A malformed message is terminated and must not block the next valid one (MaxAckPending=1).
func TestConsumePermanentErrorDoesNotBlock(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "ok1", "garbage", "ok2")
	var got []string
	calls := map[string]int{}
	consumeUntil(t, j, mdSpec(), func(m Msg) error {
		calls[string(m.Data)]++
		if string(m.Data) == "garbage" {
			return Permanent(errors.New("undecodable"))
		}
		got = append(got, string(m.Data))
		return nil
	}, func() bool { return len(got) == 2 })
	if strings.Join(got, ",") != "ok1,ok2" || calls["garbage"] != 1 {
		t.Fatalf("got %v, calls %v", got, calls)
	}
	time.Sleep(400 * time.Millisecond) // > AckWait: a Nak'd/unacked message would come back
	if f, _ := j.AckFloor(ctxT(t), StreamMD, "engine"); f != 3 {
		t.Fatalf("ack floor %d, want 3 (terminated counts as done)", f)
	}
}

func TestConsumeTransientRetriedThenTerminated(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "flaky", "stuck", "ok")
	calls := map[string]int{}
	var got []string
	consumeUntil(t, j, mdSpec(), func(m Msg) error {
		d := string(m.Data)
		calls[d]++
		if d == "flaky" && calls[d] < 2 || d == "stuck" {
			return errors.New("transient")
		}
		got = append(got, d)
		return nil
	}, func() bool { return len(got) == 2 })
	if strings.Join(got, ",") != "flaky,ok" || calls["flaky"] != 2 || calls["stuck"] != 3 {
		t.Fatalf("got %v calls %v (want flaky retried once, stuck MaxDeliver=3 times)", got, calls)
	}
}

func TestConsumeAbortLeavesMessageForNextProcess(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "1", "2", "3")
	boom := errors.New("publish failed")
	err := consumeUntil(t, j, mdSpec(), func(m Msg) error {
		if string(m.Data) == "2" {
			return Abort(boom)
		}
		return nil
	}, func() bool { return false })
	if !errors.Is(err, boom) {
		t.Fatalf("err %v, want %v", err, boom)
	}
	time.Sleep(50 * time.Millisecond)
	if f, _ := j.AckFloor(ctxT(t), StreamMD, "engine"); f != 1 {
		t.Fatalf("ack floor %d, want 1", f)
	}
	var got []string // "next process": message 2 is redelivered after AckWait, then 3
	consumeUntil(t, j, mdSpec(), func(m Msg) error { got = append(got, string(m.Data)); return nil },
		func() bool { return len(got) == 2 })
	if strings.Join(got, "") != "23" {
		t.Fatalf("after restart got %v, want [2 3]", got)
	}
}

func TestReplayUpToAndFromStart(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "1", "2", "3")
	time.Sleep(20 * time.Millisecond)
	mid := time.Now()
	publishN(t, j, "4", "5", "6")
	collect := func(start time.Time, upTo uint64) string {
		var s []string
		last, err := j.Replay(ctxT(t), StreamMD, "md.snap.>", start, upTo, func(m Msg) error {
			s = append(s, string(m.Data))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s@%d", strings.Join(s, ""), last)
	}
	if got := collect(time.Time{}, 4); got != "1234@4" {
		t.Errorf("whole stream to 4: %q", got)
	}
	if got := collect(mid, 5); got != "45@5" {
		t.Errorf("from mid to 5: %q", got)
	}
	if got := collect(time.Time{}, 0); got != "@0" {
		t.Errorf("upTo 0: %q", got)
	}
	if got := collect(time.Time{}, 99); got != "123456@6" {
		t.Errorf("upTo beyond end: %q", got)
	}
	if got := collect(time.Now().Add(time.Hour), 6); got != "@0" {
		t.Errorf("start after every message: %q", got)
	}
}

func TestStreamLimitsDiscardOldAndAreReported(t *testing.T) {
	logs := captureLog(t)
	spec := []StreamSpec{{Name: StreamMD, Subjects: []string{"md.snap.>"}, MaxAge: time.Hour, MaxBytes: 4096}}
	j := connect(t, spec)
	payload := strings.Repeat("x", 20)
	publishN(t, j, payload) // seq 1: consumed and acked below
	var got []uint64
	consumeUntil(t, j, mdSpec(), func(m Msg) error { got = append(got, m.StreamSeq); return nil },
		func() bool { return true })
	for i := 0; i < 200; i++ { // ~15 KiB into a 4 KiB stream: publishes still succeed (DiscardOld)
		publishN(t, j, payload)
	}
	info := streamInfo(t, j, StreamMD)
	if info.State.Bytes > 4096 || info.State.FirstSeq <= 2 {
		t.Fatalf("limits not enforced: %+v", info.State)
	}
	full := map[string]bool{}
	j.checkLimits(full)
	j.checkLimits(full) // logged once per transition, not on every pass
	if n := strings.Count(logs.String(), "stream MD at"); n != 1 {
		t.Errorf("limit logged %d times, want 1: %s", n, logs.String())
	}
	defer func() {
		st, _ := j.js.Stream(ctxT(t), StreamMD)
		if err := st.Purge(ctxT(t)); err != nil {
			t.Fatal(err)
		}
		j.checkLimits(full)
		if !strings.Contains(logs.String(), "stream MD back below MaxBytes") {
			t.Errorf("recovery below the limit not logged: %s", logs.String())
		}
	}()
	consumeUntil(t, j, mdSpec(), func(m Msg) error { got = append(got, m.StreamSeq); return nil },
		func() bool { return true })
	if want := fmt.Sprintf("stream sequences 2..%d were not delivered", info.State.FirstSeq-1); !strings.Contains(logs.String(), want) {
		t.Errorf("gap not logged (want %q): %s", want, logs.String())
	}
}

func TestConnectErrorHidesURL(t *testing.T) {
	u := "nats://alice:s3cret@127.0.0.1:1"
	_, err := ConnectJetStream(context.Background(), u, "test", nil)
	if err == nil {
		t.Fatal("expected connect error")
	}
	for _, leak := range []string{"s3cret", "alice:", u} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
	if got := redactURL("dial "+u+" failed for alice:s3cret@x", u); strings.Contains(got, "s3cret") || strings.Contains(got, "alice") {
		t.Fatalf("redactURL: %s", got)
	}
	list := "nats://a:p1@h1:4222, nats://b:p2@h2:4222"
	if got := redactURL("dial nats://b:p2@h2:4222: refused (pass p2, also p1)", list); strings.Contains(got, "p1") ||
		strings.Contains(got, "p2") || strings.Contains(got, "@h2") || !strings.Contains(got, "dial <NATS_URL>: refused") {
		t.Fatalf("redactURL server list: %s", got)
	}
}

func TestRetry(t *testing.T) {
	n, before := 0, 0
	ctx := context.Background()
	err := Retry(ctx, []time.Duration{0, 0, 0}, func() { before++ }, func() error {
		n++
		if n < 3 {
			return errors.New("x")
		}
		return nil
	})
	if err != nil || n != 3 || before != 2 {
		t.Fatalf("err=%v n=%d before=%d", err, n, before)
	}
	n = 0
	if err := Retry(ctx, []time.Duration{0, 0}, nil, func() error { n++; return errors.New("x") }); err == nil || n != 3 {
		t.Fatalf("exhausted: err=%v n=%d", err, n)
	}
	n = 0
	if err := Retry(ctx, []time.Duration{time.Hour}, nil, func() error { n++; return Permanent(errors.New("x")) }); err == nil || n != 1 {
		t.Fatalf("permanent error retried: err=%v n=%d", err, n)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	n = 0
	start := time.Now()
	err = Retry(cctx, []time.Duration{time.Hour}, nil, func() error { n++; return errors.New("x") })
	if !errors.Is(err, context.Canceled) || n != 1 || time.Since(start) > time.Second {
		t.Fatalf("cancelled: err=%v n=%d", err, n)
	}
}

// MaxDeliver < 0: a message keeps being redelivered until it succeeds (never terminated).
func TestConsumeUnlimitedMaxDeliver(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "x", "y")
	spec := mdSpec()
	spec.MaxDeliver = -1
	calls := 0
	var got []string
	consumeUntil(t, j, spec, func(m Msg) error {
		if string(m.Data) == "x" {
			if calls++; calls < 7 {
				return errors.New("transient")
			}
		}
		got = append(got, string(m.Data))
		return nil
	}, func() bool { return len(got) == 2 })
	if strings.Join(got, "") != "xy" || calls != 7 {
		t.Fatalf("got %v after %d calls", got, calls)
	}
}

func TestConsumerDefaults(t *testing.T) {
	var c ConsumerSpec
	c.defaults()
	if c.AckWait != 10*time.Second || c.MaxDeliver != 5 || fmt.Sprint(c.Backoff) != "[1s 2s 4s 8s]" {
		t.Fatalf("defaults %+v", c)
	}
	c = ConsumerSpec{MaxDeliver: -1}
	c.defaults()
	if c.MaxDeliver != -1 {
		t.Fatalf("unlimited MaxDeliver overwritten: %d", c.MaxDeliver)
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	j := connect(t, DefaultStreams())
	if _, ok, err := j.LoadCheckpoint(ctxT(t), "engine"); ok || err != nil {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	for _, cp := range []Checkpoint{{Seq: 7, Day: "2026-09-23"}, {Seq: 90, Day: "2026-09-24"}} {
		if err := j.SaveCheckpoint(ctxT(t), "engine", cp); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := j.LoadCheckpoint(ctxT(t), "engine"); !ok || err != nil || got != cp {
			t.Fatalf("got %+v ok=%v err=%v, want %+v", got, ok, err, cp)
		}
	}
}

// BeforeAck failing stops the consumer without acking: the message comes back to the next one.
func TestConsumeBeforeAckBlocksAck(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, "1", "2")
	spec := mdSpec()
	stop := errors.New("lease lost")
	n := 0
	spec.BeforeAck = func() error {
		if n++; n == 2 {
			return stop
		}
		return nil
	}
	err := consumeUntil(t, j, spec, func(Msg) error { return nil }, func() bool { return false })
	if !errors.Is(err, stop) {
		t.Fatalf("err %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if f, _ := j.AckFloor(ctxT(t), StreamMD, "engine"); f != 1 {
		t.Fatalf("ack floor %d, want 1", f)
	}
}

func TestDialDoesNotTouchStreams(t *testing.T) {
	url := startServer(t)
	j, err := DialJetStream(context.Background(), url, "test", DefaultStreams())
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err := j.js.Stream(ctxT(t), StreamMD); err == nil {
		t.Fatal("DialJetStream created streams")
	}
}

func TestTailCatchesUpThenFollows(t *testing.T) {
	j := connect(t, DefaultStreams())
	publishN(t, j, `{"n":1}`, `{"n":2}`)
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	var mu sync.Mutex
	var got []string
	caught := make(chan int, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- j.Tail(ctx, StreamMD, "md.snap.>", time.Time{}, func(m Msg) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, string(m.Data))
			return nil
		}, func() {
			mu.Lock()
			defer mu.Unlock()
			caught <- len(got)
		})
	}()
	select {
	case n := <-caught:
		if n != 2 {
			t.Fatalf("caught up after %d messages, want 2", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("never caught up")
	}
	publishN(t, j, `{"n":3}`)
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live message not followed: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("Tail = %v, want context.Canceled", err)
	}
	if strings.Join(got, ",") != `{"n":1},{"n":2},{"n":3}` {
		t.Errorf("order = %v", got)
	}
}

func TestTailEmptyStreamIsCaughtUp(t *testing.T) {
	j := connect(t, DefaultStreams())
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	caught := make(chan struct{})
	go func() {
		_ = j.Tail(ctx, StreamAI, "ai.signal.>", time.Now(), func(Msg) error { return nil }, func() { close(caught) })
	}()
	select {
	case <-caught:
	case <-time.After(10 * time.Second):
		t.Fatal("empty stream never reported caught up")
	}
}
