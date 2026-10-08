package tkarchive

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeDL(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const matrixBody = `{"success":true,"date":"2026-03-01","intervals":["09:00"],"data":[{"symbol":"AAA"}],"count":1}`

func TestIngestDuplicateAndResume(t *testing.T) {
	root, dl := t.TempDir(), t.TempDir()
	s, _ := NewStore(root)
	writeDL(t, dl, "tk__hot_money_matrix__2026-03-01.json", matrixBody)
	writeDL(t, dl, "tk__hot_money_matrix__2026-03-01 (1).json", matrixBody) // browser retry copy

	r1, err := Ingest(s, dl)
	if err != nil || r1.NewlyArchived != 1 || r1.PhysicalFiles != 2 || len(r1.NonConforming) != 1 {
		t.Fatalf("first ingest: %+v %v", r1, err)
	}
	r2, _ := Ingest(s, dl) // resume: nothing new, nothing rewritten
	if r2.NewlyArchived != 0 || r2.SkippedComplete != 1 {
		t.Fatalf("second ingest must skip complete items: %+v", r2)
	}
}

func TestIngestQuarantinesRealSecretAndKeepsFalsePositive(t *testing.T) {
	root, dl := t.TempDir(), t.TempDir()
	s, _ := NewStore(root)
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	jwt := hdr + "." + "eyJzdWIiOiJ4eHh4eHgifQ" + "." + "c2lnbmF0dXJl"
	real := `{"success":true,"date":"2026-03-02","data":[],"x":"` + "eyJ" + jwt[3:] + `"}`
	// not a JWT: eyJ-shaped data whose header has no alg
	fpHdr := base64.RawURLEncoding.EncodeToString([]byte(`{"name":"abc"}`))
	fp := `{"success":true,"date":"2026-03-03","data":[],"x":"` + fpHdr + ".AAAAAAAAAAAA.BBBBBB" + `"}`
	writeDL(t, dl, "tk__hot_money_matrix__2026-03-02.json", real)
	writeDL(t, dl, "tk__hot_money_matrix__2026-03-03.json", fp)

	r, err := Ingest(s, dl)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Quarantined) != 1 || r.Quarantined[0].Classification != "suspected_real_secret" {
		t.Fatalf("real secret must be quarantined: %+v", r.Quarantined)
	}
	if _, err := os.Stat(filepath.Join(root, "quarantine", "tk__hot_money_matrix__2026-03-02.json")); err != nil {
		t.Fatal("quarantine copy missing")
	}
	if _, err := os.Stat(filepath.Join(dl, "tk__hot_money_matrix__2026-03-02.json")); err != nil {
		t.Fatal("source file must not be deleted")
	}
	if len(r.FalsePositives) != 1 || r.NewlyArchived != 1 {
		t.Fatalf("false positive must be ingested and recorded: %+v", r)
	}
}

func TestAnnotateCaptureAndRecaptureKeyAreDistinct(t *testing.T) {
	root, dl := t.TempDir(), t.TempDir()
	s, _ := NewStore(root)
	writeDL(t, dl, "tk__hot_money_matrix__2026-03-01.json", matrixBody)
	if _, err := Ingest(s, dl); err != nil {
		t.Fatal(err)
	}
	if err := s.Annotate(DatasetMatrix, "2026-03-01", "same_day_capture", SessionUnknown); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Manifest(DatasetMatrix)
	if m["2026-03-01"].ObservationKind != "same_day_capture" || m["2026-03-01"].Completeness != SessionUnknown || s.Verify(m["2026-03-01"]) != nil {
		t.Fatal("annotation must label the entry and keep the raw checksum valid")
	}
	writeDL(t, dl, "tk__hot_money_matrix__2026-03-01__recapture.json", matrixBody+" ")
	r, err := Ingest(s, dl)
	if err != nil || r.NewlyArchived != 1 {
		t.Fatalf("recapture must be a separate immutable item: %+v %v", r, err)
	}
	m, _ = s.Manifest(DatasetMatrix)
	if m["2026-03-01@recapture"] == nil || m["2026-03-01@recapture"].SHA256 == m["2026-03-01"].SHA256 {
		t.Fatal("recapture and original capture must not be conflated")
	}
}
