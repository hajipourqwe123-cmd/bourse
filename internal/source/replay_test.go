package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplayBatchesByTimeAndAcceptsEnvelopes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.ndjson")
	data := `{"subject":"md.snap.A","data":{"ins_code":"A","source_time":"2026-09-23T09:00:00+03:30"}}
{"ins_code":"B","source_time":"2026-09-23T09:00:00+03:30"}
{"subject":"md.snap.A","data":{"ins_code":"A","source_time":"2026-09-23T09:00:05+03:30"}}
`
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewReplay(p)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := r.Fetch(context.Background())
	if err != nil || len(b1) != 2 || b1[0].InsCode != "A" || b1[1].InsCode != "B" {
		t.Fatalf("batch1: %v %+v", err, b1)
	}
	b2, err := r.Fetch(context.Background())
	if err != nil || len(b2) != 1 {
		t.Fatalf("batch2: %v %+v", err, b2)
	}
	if _, err := r.Fetch(context.Background()); !errors.Is(err, ErrDone) {
		t.Fatalf("want ErrDone, got %v", err)
	}
}
