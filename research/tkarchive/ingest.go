package tkarchive

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Downloaded files are named tk__<dataset>__<YYYY-MM-DD>.json.
var (
	dlNameRe  = regexp.MustCompile(`^tk__(hot_money_matrix|symbol_score_history)__(\d{4}-\d{2}-\d{2})\.json$`)
	jwtShape  = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}`)
	authLabel = regexp.MustCompile(`(?i)"?(access_token|refresh_token|authorization)"?\s*[:=]\s*"?Bearer`)
)

var endpoints = map[string]string{DatasetMatrix: "big-movers/matrix", DatasetScores: "symbol-scores"}

// IngestReport counts physical files separately from logical archive items.
// It never contains file contents or matched secret text.
type IngestReport struct {
	PhysicalFiles   int               `json:"physical_download_files"`
	NonConforming   []string          `json:"non_conforming_names"` // retries/duplicates such as "name (1).json"
	UniqueItems     int               `json:"unique_archive_items_in_downloads"`
	NewlyArchived   int               `json:"newly_archived"`
	SkippedComplete int               `json:"skipped_already_complete"`
	Quarantined     []QuarantineEntry `json:"quarantined"`
	FalsePositives  []string          `json:"secret_scan_false_positives_ingested"`
	Errors          []string          `json:"errors"`
}

type QuarantineEntry struct {
	File           string `json:"file"`
	Pattern        string `json:"pattern"` // jwt_shape | auth_label
	Classification string `json:"classification"`
}

// classifyHit decides whether a secret-pattern match is a real credential.
// A JWT-shaped string counts as real only if its header decodes to JSON with
// an "alg" field; otherwise it is a market-data false positive.
func classifyHit(b []byte) (pattern string, real bool, hit bool) {
	if authLabel.Match(b) {
		return "auth_label", true, true
	}
	for _, m := range jwtShape.FindAll(b, -1) {
		hdr := strings.SplitN(string(m), ".", 2)[0]
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(hdr, "="))
		var h map[string]any
		if err == nil && json.Unmarshal(raw, &h) == nil {
			if _, ok := h["alg"]; ok {
				return "jwt_shape", true, true
			}
		}
		pattern = "jwt_shape"
		hit = true
	}
	return pattern, false, hit
}

// Ingest moves browser-downloaded files into the immutable archive. Files
// already archived with a valid checksum are skipped. Files with a real
// credential-like match are copied to <root>/quarantine and not archived;
// they are never deleted and match text is never printed.
func Ingest(s *Store, dir string) (*IngestReport, error) {
	rep := &IngestReport{}
	files, _ := filepath.Glob(filepath.Join(dir, "tk__*"))
	sort.Strings(files)
	for _, p := range files {
		rep.PhysicalFiles++
		name := filepath.Base(p)
		m := dlNameRe.FindStringSubmatch(name)
		if m == nil {
			rep.NonConforming = append(rep.NonConforming, name)
			continue
		}
		rep.UniqueItems++
		ds, key := m[1], m[2]
		man, err := s.Manifest(ds)
		if err != nil {
			return rep, err
		}
		if e := man[key]; e != nil && s.Verify(e) == nil {
			rep.SkippedComplete++
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return rep, err
		}
		if pat, real, hit := classifyHit(b); hit {
			if real {
				qd := filepath.Join(s.Root, "quarantine")
				if err := os.MkdirAll(qd, 0o755); err != nil {
					return rep, err
				}
				if err := os.WriteFile(filepath.Join(qd, name), b, 0o444); err != nil && !os.IsExist(err) {
					rep.Errors = append(rep.Errors, name+": quarantine copy failed")
				}
				rep.Quarantined = append(rep.Quarantined, QuarantineEntry{name, pat, "suspected_real_secret"})
				continue
			}
			rep.FalsePositives = append(rep.FalsePositives, name)
		}
		params := map[string]string{"date": key}
		if ds == DatasetScores {
			params = map[string]string{"score_date": key, "end_date": key}
		}
		st, _ := os.Stat(p)
		if _, err := s.Pending(ds, key, key); err != nil { // moves a corrupt raw file aside
			return rep, err
		}
		if _, err = s.Put(PutRequest{Dataset: ds, Key: key, Endpoint: endpoints[ds], Params: params,
			HTTPStatus: 200, RetrievedAt: st.ModTime().UTC().Format(time.RFC3339), Body: string(b)}); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		rep.NewlyArchived++
	}
	return rep, nil
}
