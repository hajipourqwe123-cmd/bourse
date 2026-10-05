package tkarchive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, s *Store, key, body string) error {
	t.Helper()
	_, err := s.Put(PutRequest{Dataset: DatasetScores, Key: key, Endpoint: "symbol-scores",
		Params: map[string]string{"score_date": key}, HTTPStatus: 200, RetrievedAt: "2026-10-05T00:00:00Z", Body: body})
	return err
}

func TestPutIsImmutableAndResumable(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := put(t, s, "2026-10-01", `[{"symbol":"A","score_date":"2026-10-01"}]`); err != nil {
		t.Fatal(err)
	}
	if err := put(t, s, "2026-10-01", `[]`); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite must be refused, got %v", err)
	}
	p, _ := s.Pending(DatasetScores, "2026-10-01", "2026-10-03")
	if len(p) != 2 || p[0] != "2026-10-02" {
		t.Fatalf("pending = %v", p)
	}
}

func TestCorruptFileMovedAsideAndRefetchable(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	put(t, s, "2026-10-01", `[{"symbol":"A"}]`)
	path := filepath.Join(s.Root, DatasetScores, "2026-10-01.json")
	os.Chmod(path, 0o644)
	os.WriteFile(path, []byte(`[{"symbol":"B"}]`), 0o644)
	p, _ := s.Pending(DatasetScores, "2026-10-01", "2026-10-01")
	if len(p) != 1 {
		t.Fatalf("corrupt item must be pending, got %v", p)
	}
	if m, _ := filepath.Glob(path + ".corrupt-*"); len(m) != 1 {
		t.Fatal("corrupt file must be renamed aside, not deleted")
	}
}

func TestRejectsSecretsAndNonJSON(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	_, err := s.Put(PutRequest{Dataset: DatasetScores, Key: "2026-10-01", Params: map[string]string{"Authorization": "x"}, Body: `[]`})
	if !errors.Is(err, ErrSecret) {
		t.Fatalf("got %v", err)
	}
	_, err = s.Put(PutRequest{Dataset: DatasetScores, Key: "2026-10-01", Params: map[string]string{"v": "Bearer abc"}, Body: `[]`})
	if !errors.Is(err, ErrSecret) {
		t.Fatalf("got %v", err)
	}
	if err := put(t, s, "2026-10-01", `<html>`); !errors.Is(err, ErrNotJSON) {
		t.Fatalf("got %v", err)
	}
	if err := put(t, s, "../x", `[]`); !errors.Is(err, ErrBadKey) {
		t.Fatalf("got %v", err)
	}
}

func TestAnalyzeCountsMatrixAndArray(t *testing.T) {
	n, d, _, syms, _ := Analyze([]byte(`{"date":"2026-10-04","data":[{"symbol":"A"},{"symbol":"B"}]}`))
	if n != 2 || d != "2026-10-04" || len(syms) != 2 {
		t.Fatal(n, d, syms)
	}
	n, _, _, _, _ = Analyze([]byte(`[{"symbol":"A"}]`))
	if n != 1 {
		t.Fatal(n)
	}
}
