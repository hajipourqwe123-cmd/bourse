package bus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Stream names. Subjects per stream are in DefaultStreams and contracts/subjects.md.
const (
	StreamMD      = "MD"
	StreamFlow    = "FLOW"
	StreamAI      = "AI"
	StreamQuality = "QUALITY"
)

// StreamSpec describes one JetStream stream. Retention is by limits: whichever of MaxAge or
// MaxBytes is hit first discards the OLDEST messages (DiscardOld). Long-term storage is
// ClickHouse (W-01) and the daily recordings (R-01), not the bus.
type StreamSpec struct {
	Name     string
	Subjects []string
	MaxAge   time.Duration
	MaxBytes int64
}

const gib = int64(1) << 30

// SizedSessionSpan is the longest daily collection span (union of all session classes) the
// stream sizing assumes; services warn at startup when the loaded calendar exceeds it.
const SizedSessionSpan = 9*time.Hour + 35*time.Minute // 08:25–18:00

// DefaultStreams is the single source of truth for stream layout.
//
// Sizing (contracts/subjects.md, TestStreamSizingAssumption): full market ~1500 instruments every
// 5s = 300 snapshots/s. Worst case: every instrument published across the whole union of the
// session calendar (08:25–18:00 = 34,500 s) = 10.35M snapshots/day; a worst-case stored snapshot
// (5-level book, message-ID header) measures 1270 B (assumed <= 1400 B) → <= 14.5 GB/day, so MD
// (20 GiB) holds >= 1.2 full days (the engine replays the current day on restart). Realistic
// (each class only in its own session; unchanged snapshots outside it skipped): <= 9 GB/day.
// FLOW: <= 2 outputs per snapshot (measured ≈ 2.0), <= 400 B stored each (measured 317) → <= 8.3
// GB/day; QUALITY: <= 1 issue per snapshot, <= 450 B (measured 383) → <= 4.7 GB/day
// (TestFlowAndQualitySizing). Sum of MaxBytes: 37 GiB (reserved against max_file_store).
func DefaultStreams() []StreamSpec {
	return []StreamSpec{
		{Name: StreamMD, Subjects: []string{"md.snap.>"}, MaxAge: 48 * time.Hour, MaxBytes: 20 * gib},
		{Name: StreamFlow, Subjects: []string{"flow.>"}, MaxAge: 48 * time.Hour, MaxBytes: 10 * gib},
		{Name: StreamAI, Subjects: []string{"ai.signal.>"}, MaxAge: 48 * time.Hour, MaxBytes: 1 * gib},
		{Name: StreamQuality, Subjects: []string{"quality.>"}, MaxAge: 48 * time.Hour, MaxBytes: 6 * gib},
	}
}

// DupWindow is how long JetStream remembers message IDs for de-duplication. It must exceed the
// worst case between a failed engine publish and the republish by the next process: publish
// retries (~8s per output) + restart + state replay of one trading day + AckWait (minutes).
const DupWindow = 30 * time.Minute

// JetStream publishes to and consumes from NATS JetStream. Safe for concurrent Publish.
type JetStream struct {
	nc      *nats.Conn
	js      jetstream.JetStream
	streams []StreamSpec
	timeout time.Duration // per publish / API call
}

// ConnectJetStream is DialJetStream followed by EnsureStreams.
func ConnectJetStream(ctx context.Context, rawURL, name string, streams []StreamSpec) (*JetStream, error) {
	j, err := DialJetStream(ctx, rawURL, name, streams)
	if err != nil {
		return nil, err
	}
	if err := j.EnsureStreams(ctx); err != nil {
		j.nc.Close()
		return nil, err
	}
	return j, nil
}

// DialJetStream connects to url without creating or changing streams (a process that must
// first take a lease uses it, so a refused process cannot alter the running one's streams).
// name identifies the client in NATS monitoring. Errors never contain the URL (it may carry
// credentials).
func DialJetStream(ctx context.Context, rawURL, name string, streams []StreamSpec) (*JetStream, error) {
	nc, err := nats.Connect(rawURL,
		nats.Name(name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		// No reconnect buffer: a publish while disconnected fails now (and is retried by the
		// caller) instead of being delivered later, possibly after this process gave up on it
		// or lost its lease.
		nats.ReconnectBufSize(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Printf("nats: disconnected: %s", redactURL(err.Error(), rawURL))
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) { log.Printf("nats: reconnected") }),
	)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %s", redactURL(err.Error(), rawURL))
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream: %w", err)
	}
	return &JetStream{nc: nc, js: js, streams: streams, timeout: 5 * time.Second}, nil
}

// redactURL removes the URL (or comma-separated server list) from msg, then any credentials
// of each server URL (userinfo and bare password) that still appear.
func redactURL(msg, rawURL string) string {
	if rawURL == "" {
		return msg
	}
	servers := strings.Split(rawURL, ",")
	msg = strings.ReplaceAll(msg, rawURL, "<NATS_URL>")
	for _, one := range servers {
		if one = strings.TrimSpace(one); one != "" {
			msg = strings.ReplaceAll(msg, one, "<NATS_URL>")
		}
	}
	for _, one := range servers {
		u, err := url.Parse(strings.TrimSpace(one))
		if err != nil || u.User == nil {
			continue
		}
		msg = strings.ReplaceAll(msg, u.User.String()+"@", "***@")
		if p, ok := u.User.Password(); ok && p != "" {
			msg = strings.ReplaceAll(msg, p, "***")
		}
	}
	return msg
}

// EnsureStreams creates the configured streams or updates them to the configured limits.
// Idempotent.
func (j *JetStream) EnsureStreams(ctx context.Context) error {
	for _, s := range j.streams {
		_, err := j.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:       s.Name,
			Subjects:   s.Subjects,
			Retention:  jetstream.LimitsPolicy,
			Storage:    jetstream.FileStorage,
			MaxAge:     s.MaxAge,
			MaxBytes:   s.MaxBytes,
			Discard:    jetstream.DiscardOld,
			Duplicates: DupWindow,
		})
		if err != nil {
			return fmt.Errorf("jetstream: ensure stream %s: %w", s.Name, err)
		}
	}
	return nil
}

// Close flushes buffered protocol (e.g. the last ack) to the server, then closes the connection.
func (j *JetStream) Close() error {
	err := j.nc.FlushTimeout(2 * time.Second)
	j.nc.Close()
	return err
}

// Publish stores v (JSON, as NDJSON's data) on subject and waits for the stream's ack.
// A subject no stream covers is an error.
func (j *JetStream) Publish(subject string, v any) error { return j.PublishID(subject, "", v) }

// PublishID is Publish with a JetStream message ID: a second publish with the same id within
// DupWindow is acknowledged but not stored again. Empty id disables de-duplication.
func (j *JetStream) PublishID(subject, id string, v any) error {
	data, err := marshal(v)
	if err != nil {
		return fmt.Errorf("jetstream: encode %s: %w", subject, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	defer cancel()
	var opts []jetstream.PublishOpt
	if id != "" {
		opts = append(opts, jetstream.WithMsgID(id))
	}
	if _, err := j.js.Publish(ctx, subject, data, opts...); err != nil {
		return fmt.Errorf("jetstream: publish %s: %w", subject, err)
	}
	return nil
}

// BatchItem is one message of PublishBatch.
type BatchItem struct {
	Subject, ID string // ID: JetStream message ID ("" = no de-duplication)
	V           any
}

// PublishBatch sends items without waiting in between (in order, on one connection) and then
// waits for every stream ack; it returns one error per item (nil = stored). It is one publish
// attempt for independent messages: the caller retries the failed ones.
func (j *JetStream) PublishBatch(items []BatchItem) []error {
	errs := make([]error, len(items))
	futs := make([]jetstream.PubAckFuture, len(items))
	for i, it := range items {
		data, err := marshal(it.V)
		if err != nil {
			errs[i] = fmt.Errorf("jetstream: encode %s: %w", it.Subject, err)
			continue
		}
		var opts []jetstream.PublishOpt
		if it.ID != "" {
			opts = append(opts, jetstream.WithMsgID(it.ID))
		}
		if futs[i], err = j.js.PublishAsync(it.Subject, data, opts...); err != nil {
			errs[i] = fmt.Errorf("jetstream: publish %s: %w", it.Subject, err)
		}
	}
	deadline := time.NewTimer(j.timeout)
	defer deadline.Stop()
	for i, f := range futs {
		if f == nil {
			continue
		}
		select {
		case <-f.Ok():
		case err := <-f.Err():
			errs[i] = fmt.Errorf("jetstream: publish %s: %w", items[i].Subject, err)
		case <-deadline.C:
			errs[i] = fmt.Errorf("jetstream: publish %s: no ack within %s", items[i].Subject, j.timeout)
			deadline.Reset(0) // the rest are late too
		}
	}
	return errs
}

// StreamInfo is the part of a stream's state the services need.
type StreamInfo struct {
	FirstSeq    uint64    // first sequence still stored (lower ones were discarded or deleted)
	FirstStored time.Time // when FirstSeq was stored
	LastSeq     uint64    // last sequence stored
}

// StreamState returns the stream's current first/last sequences.
func (j *JetStream) StreamState(ctx context.Context, stream string) (StreamInfo, error) {
	s, err := j.js.Stream(ctx, stream)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("jetstream: stream %s: %w", stream, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return StreamInfo{}, fmt.Errorf("jetstream: stream %s info: %w", stream, err)
	}
	return StreamInfo{FirstSeq: info.State.FirstSeq, FirstStored: info.State.FirstTime, LastSeq: info.State.LastSeq}, nil
}

// StreamCreated returns the creation time of a stream (distinguishes re-created streams whose
// sequences restart at 1).
func (j *JetStream) StreamCreated(ctx context.Context, stream string) (time.Time, error) {
	s, err := j.js.Stream(ctx, stream)
	if err != nil {
		return time.Time{}, fmt.Errorf("jetstream: stream %s: %w", stream, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("jetstream: stream %s info: %w", stream, err)
	}
	return info.Created, nil
}

// WatchLimits logs when a stream reaches its MaxBytes (from then on the oldest messages are
// being discarded) and when it drops back below. It polls every interval until ctx is done.
func (j *JetStream) WatchLimits(ctx context.Context, interval time.Duration) {
	full := map[string]bool{}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		j.checkLimits(full)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// checkLimits is one WatchLimits pass; full carries the per-stream state between passes.
func (j *JetStream) checkLimits(full map[string]bool) {
	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	defer cancel()
	for _, spec := range j.streams {
		if spec.MaxBytes <= 0 {
			continue
		}
		s, err := j.js.Stream(ctx, spec.Name)
		if err != nil {
			continue
		}
		info, err := s.Info(ctx)
		if err != nil {
			continue
		}
		used := float64(info.State.Bytes) / float64(spec.MaxBytes)
		switch {
		case used >= 0.95 && !full[spec.Name]:
			full[spec.Name] = true
			log.Printf("jetstream: stream %s at %.0f%% of MaxBytes (%d): oldest messages are being discarded",
				spec.Name, used*100, spec.MaxBytes)
		case used < 0.90 && full[spec.Name]:
			full[spec.Name] = false
			log.Printf("jetstream: stream %s back below MaxBytes (%.0f%%)", spec.Name, used*100)
		}
	}
}

// Msg is one consumed message.
type Msg struct {
	Subject      string
	Data         []byte
	StreamSeq    uint64
	NumDelivered uint64
	Stored       time.Time // when the stream stored it (deterministic across replays)
	inProgress   func() error
}

// InProgress resets the ack deadline; call it while retrying slow work on this message.
func (m Msg) InProgress() {
	if m.inProgress != nil {
		_ = m.inProgress()
	}
}

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Permanent marks a handler error as not retryable: the message is terminated.
func Permanent(err error) error { return permanentError{err} }

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

type abortError struct{ error }

func (e abortError) Unwrap() error { return e.error }

// Abort makes Consume return err immediately, leaving the message unacked for redelivery to
// the next process (after AckWait). Use when the process must exit.
func Abort(err error) error { return abortError{err} }

// ConsumerSpec configures a durable, strictly ordered pull consumer.
type ConsumerSpec struct {
	Stream, Durable, Filter string
	AckWait                 time.Duration   // default 10s; must exceed the handler's worst case (or use InProgress)
	MaxDeliver              int             // default 5; < 0 = unlimited (crash redeliveries also count)
	Backoff                 []time.Duration // redelivery delay after the n-th failed attempt; default 1s,2s,4s,8s
	// BeforeAck, if set, is checked before every ack; an error stops Consume WITHOUT acking
	// (e.g. the process's single-instance lease is no longer valid).
	BeforeAck func() error
}

func (c *ConsumerSpec) defaults() {
	if c.AckWait <= 0 {
		c.AckWait = 10 * time.Second
	}
	if c.MaxDeliver == 0 {
		c.MaxDeliver = 5
	}
	if len(c.Backoff) == 0 {
		c.Backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	}
}

// AckFloor returns the highest stream sequence up to which the durable has acknowledged (or
// terminated) every message; 0 if the consumer does not exist yet.
func (j *JetStream) AckFloor(ctx context.Context, stream, durable string) (uint64, error) {
	c, err := j.js.Consumer(ctx, stream, durable)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("jetstream: consumer %s/%s: %w", stream, durable, err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("jetstream: consumer %s/%s info: %w", stream, durable, err)
	}
	return info.AckFloor.Stream, nil
}

// GetMsg returns the payload of the message at seq.
func (j *JetStream) GetMsg(ctx context.Context, stream string, seq uint64) (subject string, data []byte, stored time.Time, err error) {
	s, err := j.js.Stream(ctx, stream)
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("jetstream: stream %s: %w", stream, err)
	}
	m, err := s.GetMsg(ctx, seq)
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("jetstream: get %s/%d: %w", stream, seq, err)
	}
	return m.Subject, m.Data, m.Time, nil
}

// Replay calls fn, in stream order, for every message matching filter that was stored at or
// after start (zero start = the whole stream) and whose sequence is <= upTo. Nothing is acked;
// no durable consumer is touched. It returns the last sequence passed to fn (0 if none); a fn
// error stops the replay and is returned. The end of the stream is detected from the server's
// pending count, not from a quiet fetch.
func (j *JetStream) Replay(ctx context.Context, stream, filter string, start time.Time, upTo uint64, fn func(Msg) error) (last uint64, err error) {
	if upTo == 0 {
		return 0, nil
	}
	cfg := jetstream.OrderedConsumerConfig{FilterSubjects: []string{filter}, DeliverPolicy: jetstream.DeliverAllPolicy}
	if !start.IsZero() {
		cfg.DeliverPolicy = jetstream.DeliverByStartTimePolicy
		cfg.OptStartTime = &start
	}
	c, err := j.js.OrderedConsumer(ctx, stream, cfg)
	if err != nil {
		return 0, fmt.Errorf("jetstream: replay %s: %w", stream, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		batch, err := c.Fetch(512, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			return last, fmt.Errorf("jetstream: replay %s: fetch: %w", stream, err)
		}
		got := 0
		for m := range batch.Messages() {
			got++
			meta, err := m.Metadata()
			if err != nil {
				return last, fmt.Errorf("jetstream: replay %s: metadata: %w", stream, err)
			}
			if meta.Sequence.Stream > upTo {
				return last, nil
			}
			if err := fn(Msg{Subject: m.Subject(), Data: m.Data(), StreamSeq: meta.Sequence.Stream,
				NumDelivered: meta.NumDelivered, Stored: meta.Timestamp}); err != nil {
				return last, err
			}
			last = meta.Sequence.Stream
			if last == upTo || meta.NumPending == 0 {
				return last, nil
			}
		}
		if err := batch.Error(); err != nil && !errors.Is(err, nats.ErrTimeout) {
			return last, fmt.Errorf("jetstream: replay %s: %w", stream, err)
		}
		if got == 0 {
			info, err := c.Info(ctx)
			if err != nil {
				return last, fmt.Errorf("jetstream: replay %s: info: %w", stream, err)
			}
			if info.NumPending == 0 {
				return last, nil // nothing (left) at or after start
			}
		}
	}
}

// Consume runs a durable pull consumer (created or updated from spec) and calls fn for each
// message, one at a time, in stream order (MaxAckPending=1). See the package comment for the
// ack/term/redelivery rules. It returns ctx.Err() on cancellation, the wrapped error of an
// Abort, or a setup error.
func (j *JetStream) Consume(ctx context.Context, spec ConsumerSpec, fn func(Msg) error) error {
	spec.defaults()
	c, err := j.js.CreateOrUpdateConsumer(ctx, spec.Stream, jetstream.ConsumerConfig{
		Durable:       spec.Durable,
		FilterSubject: spec.Filter,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckWait:       spec.AckWait,
		MaxDeliver:    spec.MaxDeliver,
		MaxAckPending: 1,
	})
	if err != nil {
		return fmt.Errorf("jetstream: consumer %s/%s: %w", spec.Stream, spec.Durable, err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Errorf("jetstream: consumer %s/%s info: %w", spec.Stream, spec.Durable, err)
	}
	// Sequence gaps mean messages were discarded (stream limits) before this consumer got them.
	// Only meaningful when the filter covers the whole stream.
	detectGaps := j.filterIsWholeStream(spec.Stream, spec.Filter)
	last := info.AckFloor.Stream

	it, err := c.Messages()
	if err != nil {
		return fmt.Errorf("jetstream: consumer %s/%s: %w", spec.Stream, spec.Durable, err)
	}
	var once sync.Once
	stop := func() { once.Do(it.Stop) }
	defer stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()

	for {
		m, err := it.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) || errors.Is(err, jetstream.ErrConsumerDeleted) ||
				errors.Is(err, jetstream.ErrConsumerNotFound) {
				return fmt.Errorf("jetstream: consumer %s/%s: %w", spec.Stream, spec.Durable, err)
			}
			log.Printf("jetstream: consumer %s/%s: %v", spec.Stream, spec.Durable, err)
			continue
		}
		meta, err := m.Metadata()
		if err != nil {
			log.Printf("jetstream: %s: no metadata (%v); terminating message", m.Subject(), err)
			_ = m.Term()
			continue
		}
		seq := meta.Sequence.Stream
		if detectGaps && last != 0 && seq > last+1 && meta.NumDelivered == 1 {
			log.Printf("jetstream: %s/%s: stream sequences %d..%d were not delivered to this consumer (discarded by stream limits, terminated after MaxDeliver, or taken by another instance)",
				spec.Stream, spec.Durable, last+1, seq-1)
		}
		if seq > last {
			last = seq
		}
		herr := fn(Msg{Subject: m.Subject(), Data: m.Data(), StreamSeq: seq, NumDelivered: meta.NumDelivered,
			Stored: meta.Timestamp, inProgress: m.InProgress})
		var perm permanentError
		var abort abortError
		switch {
		case herr == nil:
			if spec.BeforeAck != nil {
				if err := spec.BeforeAck(); err != nil {
					return err // unacked: redelivered after AckWait
				}
			}
			if err := m.Ack(); err != nil {
				log.Printf("jetstream: ack %s seq %d: %v", m.Subject(), seq, err)
			}
		case errors.As(herr, &abort):
			return abort.error
		case ctx.Err() != nil:
			return ctx.Err() // unacked: redelivered after AckWait
		case errors.As(herr, &perm):
			if spec.BeforeAck != nil {
				if err := spec.BeforeAck(); err != nil {
					return err // not ours to terminate any more
				}
			}
			log.Printf("jetstream: %s seq %d: permanent error, terminated: %v", m.Subject(), seq, herr)
			_ = m.Term()
		case spec.MaxDeliver > 0 && meta.NumDelivered >= uint64(spec.MaxDeliver):
			log.Printf("jetstream: %s seq %d: failed %d deliveries, terminated: %v", m.Subject(), seq, meta.NumDelivered, herr)
			_ = m.Term()
		default:
			d := spec.Backoff[min(int(meta.NumDelivered), len(spec.Backoff))-1]
			log.Printf("jetstream: %s seq %d: attempt %d failed, redelivery in %s: %v", m.Subject(), seq, meta.NumDelivered, d, herr)
			_ = m.NakWithDelay(d)
		}
	}
}

// Tail follows a stream without any durable state (an ordered, ephemeral consumer): it calls fn
// for every message matching filter stored at or after start (zero = the whole stream), in
// stream order, then keeps following new messages until ctx ends. caughtUp (may be nil) is
// called once, when every message stored at the time Tail started has been passed to fn. Nothing
// is acked. A fn error stops Tail and is returned; otherwise it returns ctx.Err().
func (j *JetStream) Tail(ctx context.Context, stream, filter string, start time.Time, fn func(Msg) error, caughtUp func()) error {
	cfg := jetstream.OrderedConsumerConfig{FilterSubjects: []string{filter}, DeliverPolicy: jetstream.DeliverAllPolicy}
	if !start.IsZero() {
		cfg.DeliverPolicy = jetstream.DeliverByStartTimePolicy
		cfg.OptStartTime = &start
	}
	c, err := j.js.OrderedConsumer(ctx, stream, cfg)
	if err != nil {
		return fmt.Errorf("jetstream: tail %s: %w", stream, err)
	}
	var once sync.Once
	signal := func() {
		if caughtUp != nil {
			once.Do(caughtUp)
		}
	}
	info, err := c.Info(ctx)
	if err != nil {
		return fmt.Errorf("jetstream: tail %s: info: %w", stream, err)
	}
	if info.NumPending == 0 {
		signal()
	}
	it, err := c.Messages()
	if err != nil {
		return fmt.Errorf("jetstream: tail %s: %w", stream, err)
	}
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(it.Stop) }
	defer stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()
	for {
		m, err := it.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return fmt.Errorf("jetstream: tail %s: %w", stream, err)
			}
			log.Printf("jetstream: tail %s: %v", stream, err)
			continue
		}
		meta, err := m.Metadata()
		if err != nil {
			log.Printf("jetstream: tail %s: %s: no metadata (%v); skipped", stream, m.Subject(), err)
			continue
		}
		if err := fn(Msg{Subject: m.Subject(), Data: m.Data(), StreamSeq: meta.Sequence.Stream,
			NumDelivered: meta.NumDelivered, Stored: meta.Timestamp}); err != nil {
			return err
		}
		if meta.NumPending == 0 {
			signal()
		}
	}
}

func (j *JetStream) filterIsWholeStream(stream, filter string) bool {
	for _, s := range j.streams {
		if s.Name == stream {
			return len(s.Subjects) == 1 && s.Subjects[0] == filter
		}
	}
	return false
}

// Retry calls f until it succeeds, sleeping backoff[i] between attempts (len(backoff)+1 attempts
// in total) and calling beforeSleep (may be nil) before each sleep. An error marked Permanent
// stops it at once. It returns f's last error, or, if ctx ends during a sleep, that error joined
// with ctx's.
func Retry(ctx context.Context, backoff []time.Duration, beforeSleep func(), f func() error) error {
	err := f()
	for _, d := range backoff {
		if err == nil || IsPermanent(err) {
			return err
		}
		if beforeSleep != nil {
			beforeSleep()
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Join(err, ctx.Err())
		case <-t.C:
		}
		err = f()
	}
	return err
}
