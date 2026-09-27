package bus

import (
	"reflect"
	"testing"
	"time"
)

func TestStreamsFromEnvProductionUnchanged(t *testing.T) {
	t.Setenv("STREAM_PROFILE", "")
	got, _, err := StreamsFromEnv()
	if err != nil || !reflect.DeepEqual(got, DefaultStreams()) {
		t.Fatalf("default profile must be DefaultStreams: %v %+v", err, got)
	}
}

func TestLocalProfileWithinCap(t *testing.T) {
	t.Setenv("STREAM_PROFILE", "local")
	got, desc, err := StreamsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for i, s := range got {
		total += s.MaxBytes
		if s.MaxAge != 48*time.Hour {
			t.Errorf("%s MaxAge %s, want 48h", s.Name, s.MaxAge)
		}
		if d := DefaultStreams()[i]; s.Name != d.Name || !reflect.DeepEqual(s.Subjects, d.Subjects) {
			t.Errorf("local stream %d differs in name/subjects from production", i)
		}
	}
	if total > LocalTotalCap {
		t.Fatalf("local total %d > cap", total)
	}
	if desc != "local (20 GiB total)" {
		t.Errorf("desc %q", desc)
	}
}

func TestLocalProfileOverrides(t *testing.T) {
	t.Setenv("STREAM_PROFILE", "local")
	t.Setenv("STREAM_MD_MAX_GIB", "8")
	t.Setenv("STREAM_MAX_AGE", "24h")
	got, _, err := StreamsFromEnv()
	if err != nil || got[0].MaxBytes != 8*gib || got[0].MaxAge != 24*time.Hour {
		t.Fatalf("override not applied: %v %+v", err, got[0])
	}
	t.Setenv("STREAM_MD_MAX_GIB", "30")
	if _, _, err := StreamsFromEnv(); err == nil {
		t.Fatal("local profile over 20 GiB must be refused")
	}
	t.Setenv("STREAM_PROFILE", "bogus")
	if _, _, err := StreamsFromEnv(); err == nil {
		t.Fatal("unknown profile must be refused")
	}
}
