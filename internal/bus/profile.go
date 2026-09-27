package bus

import (
	"fmt"
	"strings"
	"time"

	"bourse/internal/config"
)

// LocalTotalCap is the most the LOCAL stream profile may reserve in total (a developer PC).
const LocalTotalCap = 20 * gib

// LocalStreams is the LOCAL profile: same streams and subjects as DefaultStreams, MaxAge 48h,
// MaxBytes summing to 20 GiB. MD keeps ~1.2 days of the SourceArena feed at 5 s (≈1100 rows,
// realistic ≤ 9 GB/day); at longer poll intervals it keeps the full 48 h. When a cap is hit the
// OLDEST messages go (DiscardOld): the engine's same-day replay needs only today's MD.
func LocalStreams() []StreamSpec {
	return []StreamSpec{
		{Name: StreamMD, Subjects: []string{"md.snap.>"}, MaxAge: 48 * time.Hour, MaxBytes: 11 * gib},
		{Name: StreamFlow, Subjects: []string{"flow.>"}, MaxAge: 48 * time.Hour, MaxBytes: 5 * gib},
		{Name: StreamAI, Subjects: []string{"ai.signal.>"}, MaxAge: 48 * time.Hour, MaxBytes: 1 * gib},
		{Name: StreamQuality, Subjects: []string{"quality.>"}, MaxAge: 48 * time.Hour, MaxBytes: 3 * gib},
	}
}

// StreamsFromEnv returns the stream layout every service must use (collector, engine and
// gateway must agree, or each start rewrites the others' limits):
//
//	STREAM_PROFILE=production (default) | local
//	STREAM_MAX_AGE=48h                  optional, all streams
//	STREAM_<NAME>_MAX_GIB=n             optional per stream (NAME = MD, FLOW, AI, QUALITY)
//
// Production defaults are DefaultStreams, unchanged. The local profile refuses overrides whose
// total exceeds LocalTotalCap, so a typo cannot fill a developer's disk.
func StreamsFromEnv() ([]StreamSpec, string, error) {
	profile := strings.ToLower(config.Str("STREAM_PROFILE", "production"))
	var specs []StreamSpec
	switch profile {
	case "production":
		specs = DefaultStreams()
	case "local":
		specs = LocalStreams()
	default:
		return nil, "", fmt.Errorf("STREAM_PROFILE=%q: want production or local", profile)
	}
	var total int64
	for i := range specs {
		if age := config.Dur("STREAM_MAX_AGE", 0); age > 0 {
			specs[i].MaxAge = age
		}
		if n := config.Int("STREAM_"+specs[i].Name+"_MAX_GIB", 0); n > 0 {
			specs[i].MaxBytes = n * gib
		}
		total += specs[i].MaxBytes
	}
	if profile == "local" && total > LocalTotalCap {
		return nil, "", fmt.Errorf("STREAM_PROFILE=local: streams reserve %d GiB, more than the %d GiB local cap", total/gib, LocalTotalCap/gib)
	}
	return specs, fmt.Sprintf("%s (%d GiB total)", profile, total/gib), nil
}
