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
//     Other handler errors are redelivered with backoff, at most MaxDeliver times, then terminated.
//   - A handler error wrapped with Abort leaves the message unacked and stops the consumer;
//     the process must exit non-zero. It is redelivered after AckWait to the next process.
//   - Once the engine has run Process() on a snapshot it must never Nak it: a redelivery would be
//     rejected by the engine as OUT_OF_ORDER and the outputs not yet published would be lost.
//     Instead it retries publishing the SAME computed outputs with bounded backoff (Retry), then
//     aborts. Outputs carry deterministic message IDs (input stream + sequence + index), so a
//     republish after a restart is de-duplicated by JetStream within the duplicate window.
//   - On restart the engine rebuilds its in-memory state by replaying, without publishing, the
//     current trading day up to the durable's ack floor (AckFloor, Replay), then resumes.
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
