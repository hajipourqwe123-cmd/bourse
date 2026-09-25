package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"bourse/internal/model"
)

// ErrDone signals that a finite source (replay) is exhausted.
var ErrDone = errors.New("source: done")

// Replay reads canonical snapshots from an NDJSON file (collector envelopes or bare snapshots),
// one batch per distinct source_time. Recording live collector output gives a replayable day.
type Replay struct {
	path string
	sc   *bufio.Scanner
	f    *os.File
	next *model.Snapshot
}

// NewReplay opens path.
func NewReplay(path string) (*Replay, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	return &Replay{path: path, sc: sc, f: f}, nil
}

func (r *Replay) Name() string { return "replay:" + r.path }

func (r *Replay) read() (*model.Snapshot, error) {
	if r.next != nil {
		s := r.next
		r.next = nil
		return s, nil
	}
	for r.sc.Scan() {
		line := r.sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Accept both collector envelopes {"subject","data"} (recordings) and bare snapshots.
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return nil, fmt.Errorf("replay: bad line: %w", err)
		}
		payload := line
		if len(env.Data) > 0 {
			payload = env.Data
		}
		var s model.Snapshot
		if err := json.Unmarshal(payload, &s); err != nil {
			return nil, fmt.Errorf("replay: bad snapshot: %w", err)
		}
		return &s, nil
	}
	if err := r.sc.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// Fetch returns all consecutive snapshots sharing the first snapshot's source_time.
func (r *Replay) Fetch(ctx context.Context) ([]model.Snapshot, error) {
	first, err := r.read()
	if errors.Is(err, io.EOF) {
		r.f.Close()
		return nil, ErrDone
	} else if err != nil {
		return nil, err
	}
	batch := []model.Snapshot{*first}
	for {
		s, err := r.read()
		if errors.Is(err, io.EOF) {
			return batch, nil
		} else if err != nil {
			return nil, err
		}
		if !s.SourceTime.Equal(first.SourceTime) {
			r.next = s
			return batch, nil
		}
		batch = append(batch, *s)
	}
}
