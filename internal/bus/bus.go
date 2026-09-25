// Package bus defines how services publish. Subjects follow contracts/subjects.md.
//
// Two transports satisfy Publisher, selected by BUS=ndjson|nats in cmd/*:
//   - NDJSON: one {"subject","data"} envelope per line (pipes, files, tests; the default).
//   - JetStream: the NATS subject is the subject and the payload is exactly the envelope's
//     "data" JSON (same encoder settings), stored in the streams of DefaultStreams.
//
// JetStream delivery semantics (engine input, see JetStream.Consume):
//   - At-least-once, strictly in stream order: the durable consumer allows one unacked message
//     (MaxAckPending=1) because the flow engine is order-sensitive per instrument.
//   - A handler error wrapped with Permanent (e.g. an undecodable payload) terminates the message
//     (Term): it is logged and reported, never retried and never blocks the next message.
//     Other handler errors are redelivered with backoff, at most MaxDeliver times (default 5),
//     then terminated. Redeliveries after a crash or Abort count toward MaxDeliver too.
//   - A handler error wrapped with Abort leaves the message unacked and stops the consumer;
//     the process must exit non-zero. It is redelivered after AckWait to the next process.
//
// Engine specifics (cmd/engine):
//   - Its durable has unlimited MaxDeliver: it has no transient errors (only Permanent or
//     Abort), so a limit would only count crash redeliveries and silently drop a snapshot.
//     Instead, on the 5th delivery of one message the engine publishes POISON_SUSPECT and exits
//     non-zero without processing it; it never skips it on its own.
//   - Once the engine has run Process() on a snapshot it must never Nak it: a redelivery would be
//     rejected by the engine as OUT_OF_ORDER and the outputs not yet published would be lost.
//     Instead it retries publishing the SAME computed outputs with bounded backoff (Retry), then
//     aborts. Outputs carry deterministic message IDs (MD stream creation + sequence + index), so
//     a republish after a restart is de-duplicated by JetStream within DupWindow (30 min).
//   - It remembers the last MD sequence applied and published: a redelivered, already-applied
//     sequence is acked without recomputing; sequences the consumer skipped are fetched and
//     applied first, so state sees every stored snapshot exactly once, in order.
//   - On restart it rebuilds its in-memory state by replaying, without publishing, the trading
//     day of the snapshot at the durable's ack floor, up to that floor (AckFloor, Replay); if part
//     of that day was already discarded, the first retained snapshot is a late day baseline and
//     the flow engine's late-start rule marks the day partial (DAY_START_MISSED, re-emitted once
//     after the restart if it was produced during the replay).
//   - Exactly one engine runs per durable, enforced by a JetStream KV lease (lease.go, P-02):
//     a second engine refuses to start, and an engine that loses its lease stops. Two
//     instances would each see only part of the snapshots.
package bus

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

// Publisher sends one message on one subject.
type Publisher interface {
	Publish(subject string, v any) error
}

// Envelope is the wire format for NDJSON output.
type Envelope struct {
	Subject string `json:"subject"`
	Data    any    `json:"data"`
}

// marshal encodes v exactly as NDJSON encodes an envelope's data (no HTML escaping, no newline).
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// NDJSON writes one envelope per line.
type NDJSON struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewNDJSON returns a publisher writing to w.
func NewNDJSON(w io.Writer) *NDJSON {
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	return &NDJSON{enc: e}
}

func (n *NDJSON) Publish(subject string, v any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.enc.Encode(Envelope{Subject: subject, Data: v})
}

// Subject builders (single source of truth for names).
func SubjSnapshot(ins string) string { return "md.snap." + ins }
func SubjFlow(ins string) string     { return "flow.event." + ins }
func SubjGame(ins string) string     { return "flow.game." + ins }
func SubjWindow(ins string) string   { return "flow.10m." + ins }
func SubjQuality(ins string) string  { return "quality." + ins }
func SubjAI(ins string) string       { return "ai.signal." + ins }
