// Package bus defines how services publish. Subjects follow contracts/subjects.md.
// Sprint 0 ships an NDJSON writer (pipes, files, local dev). The NATS JetStream publisher is
// Sprint 1 task P-01 and must satisfy the same interface.
package bus

import (
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
