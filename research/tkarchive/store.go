// Package tkarchive stores immutable raw tablokhani archive responses outside
// the repository, with a per-dataset manifest, so that extraction is resumable.
package tkarchive

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DatasetMatrix = "hot_money_matrix"
	DatasetScores = "symbol_score_history"
)

var (
	ErrExists     = errors.New("raw response already exists (immutable)")
	ErrBadDataset = errors.New("unknown dataset")
	ErrBadKey     = errors.New("key must be YYYY-MM-DD or YYYY-MM-DD@suffix")
	ErrSecret     = errors.New("request parameters look like they contain a secret")
	ErrNotJSON    = errors.New("body is not valid JSON")
	keyRe         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(@[a-z0-9-]+)?$`)
	secretKeyRe   = regexp.MustCompile(`(?i)token|auth|cookie|secret|passw|bearer|session|api[_-]?key`)
	secretValueRe = regexp.MustCompile(`(?i)^\s*(bearer\s|eyJ[A-Za-z0-9_-]{10,})`)
	validDatasets = map[string]bool{DatasetMatrix: true, DatasetScores: true}
)

// PutRequest is the minimum payload the browser sends; it never carries credentials.
type PutRequest struct {
	Dataset     string            `json:"dataset"`
	Key         string            `json:"key"`
	Endpoint    string            `json:"endpoint"`
	Params      map[string]string `json:"params"`
	HTTPStatus  int               `json:"http_status"`
	RetrievedAt string            `json:"retrieved_at"`
	Body        string            `json:"body"`
}

// Entry is one manifest line.
type Entry struct {
	Dataset       string            `json:"dataset"`
	RetrievedAt   string            `json:"retrieved_at"`
	MarketDate    string            `json:"market_date"`
	Symbol        string            `json:"symbol_if_applicable"`
	Endpoint      string            `json:"endpoint_identifier"`
	Params        map[string]string `json:"request_parameters"`
	HTTPStatus    int               `json:"http_status"`
	RecordCount   int               `json:"record_count"`
	ContentLength int               `json:"content_length"`
	SHA256        string            `json:"sha256"`
	SchemaVersion string            `json:"schema_version"`
	File          string            `json:"file"`
	PayloadDate   string            `json:"payload_date,omitempty"`
	// ObservationKind is empty for ordinary observations; "same_day_capture" marks a
	// capture taken on the market day itself (an earlier label was "intraday_snapshot").
	ObservationKind string `json:"observation_kind,omitempty"`
	// Completeness is an explicit session-completeness state (session.go); empty
	// means it is derived from retrieved_at with the session calendar.
	Completeness string `json:"completeness,omitempty"`
}

type Store struct {
	Root string
	mu   sync.Mutex
}

func NewStore(root string) (*Store, error) {
	for _, d := range []string{DatasetMatrix, DatasetScores, "manifests", "logs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{Root: root}, nil
}

func (s *Store) rawPath(dataset, key string) string {
	return filepath.Join(s.Root, dataset, key+".json")
}

func (s *Store) manifestPath(dataset string) string {
	return filepath.Join(s.Root, "manifests", dataset+".jsonl")
}

func checkParams(p map[string]string) error {
	for k, v := range p {
		if secretKeyRe.MatchString(k) || secretValueRe.MatchString(v) {
			return ErrSecret
		}
	}
	return nil
}

// Analyze extracts record count, payload date, and a schema fingerprint.
func Analyze(body []byte) (count int, payloadDate, schema string, symbols []string, err error) {
	var raw any
	if err = json.Unmarshal(body, &raw); err != nil {
		return 0, "", "", nil, ErrNotJSON
	}
	var rows []any
	var top []string
	switch v := raw.(type) {
	case []any:
		rows = v
	case map[string]any:
		for k := range v {
			top = append(top, k)
		}
		if d, ok := v["data"].([]any); ok {
			rows = d
		}
		if d, ok := v["date"].(string); ok {
			payloadDate = d
		}
	}
	var keys []string
	if len(rows) > 0 {
		if m, ok := rows[0].(map[string]any); ok {
			for k := range m {
				keys = append(keys, k)
			}
		}
	}
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			if sy, ok := m["symbol"].(string); ok {
				symbols = append(symbols, sy)
			}
		}
	}
	sort.Strings(top)
	sort.Strings(keys)
	h := sha256.Sum256([]byte(strings.Join(top, ",") + "|" + strings.Join(keys, ",")))
	return len(rows), payloadDate, "s" + hex.EncodeToString(h[:])[:12], symbols, nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Put writes a raw response exactly once. It never overwrites.
func (s *Store) Put(r PutRequest) (*Entry, error) {
	if !validDatasets[r.Dataset] {
		return nil, ErrBadDataset
	}
	if !keyRe.MatchString(r.Key) {
		return nil, ErrBadKey
	}
	if err := checkParams(r.Params); err != nil {
		return nil, err
	}
	body := []byte(r.Body)
	count, pd, schema, _, err := Analyze(body)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.rawPath(r.Dataset, r.Key)
	if _, err := os.Stat(path); err == nil {
		return nil, ErrExists
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrExists
		}
		return nil, err
	}
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	e := &Entry{
		Dataset: r.Dataset, RetrievedAt: r.RetrievedAt, MarketDate: r.Key,
		Endpoint: r.Endpoint, Params: r.Params, HTTPStatus: r.HTTPStatus,
		RecordCount: count, ContentLength: len(body), SHA256: sum(body),
		SchemaVersion: schema, File: filepath.ToSlash(filepath.Join(r.Dataset, r.Key+".json")),
		PayloadDate: pd,
	}
	if err := s.appendManifest(e); err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Store) appendManifest(e *Entry) error {
	line, _ := json.Marshal(e)
	f, err := os.OpenFile(s.manifestPath(e.Dataset), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Annotate appends a manifest line labelling an existing item's observation kind
// and session completeness. The raw file and its checksum are untouched; earlier
// manifest lines stay as history.
func (s *Store) Annotate(dataset, key, kind, completeness string) error {
	m, err := s.Manifest(dataset)
	if err != nil {
		return err
	}
	e := m[key]
	if e == nil {
		return ErrBadKey
	}
	if err := s.Verify(e); err != nil {
		return err
	}
	c := *e
	c.ObservationKind = kind
	c.Completeness = completeness
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendManifest(&c)
}

// Manifest returns the latest entry per key.
func (s *Store) Manifest(dataset string) (map[string]*Entry, error) {
	out := map[string]*Entry{}
	f, err := os.Open(s.manifestPath(dataset))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.MarketDate != "" {
			ee := e
			out[e.MarketDate] = &ee
		}
	}
	return out, sc.Err()
}

// Verify re-hashes the raw file against its manifest entry.
func (s *Store) Verify(e *Entry) error {
	b, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(e.File)))
	if err != nil {
		return err
	}
	if len(b) != e.ContentLength || sum(b) != e.SHA256 {
		return fmt.Errorf("checksum mismatch for %s", e.File)
	}
	return nil
}

// Pending lists keys in [from,to] that are missing or corrupt. Corrupt raw
// files are renamed aside (never deleted) so the key can be re-fetched.
func (s *Store) Pending(dataset, from, to string) ([]string, error) {
	if !validDatasets[dataset] {
		return nil, ErrBadDataset
	}
	a, err1 := time.Parse("2006-01-02", from)
	b, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil {
		return nil, ErrBadKey
	}
	m, err := s.Manifest(dataset)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		e := m[k]
		if e != nil && s.Verify(e) == nil {
			continue
		}
		p := s.rawPath(dataset, k)
		if _, err := os.Stat(p); err == nil {
			os.Rename(p, p+".corrupt-"+time.Now().UTC().Format("20060102T150405"))
		}
		out = append(out, k)
	}
	return out, nil
}
