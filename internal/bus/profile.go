package bus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"bourse/internal/config"
)

// LocalTotalCap is the most the LOCAL stream profile may reserve in total (a developer PC).
const LocalTotalCap = 20 * gib

// LocalStreams is the LOCAL profile: same streams and subjects as DefaultStreams, MaxAge 48h,
// MaxBytes summing to 20 GiB. It is sized for SLOW polling (>= LocalMinInterval): at 5 s the
// worst-case day (MD 14.5, FLOW 8.3, QUALITY 4.7 GB) exceeds these caps and the oldest messages
// of the day would be discarded, which the gateway's same-day rebuild cannot detect. The
// collector warns below LocalMinInterval.
func LocalStreams() []StreamSpec {
	return []StreamSpec{
		{Name: StreamMD, Subjects: []string{"md.snap.>", "md.index.>"}, MaxAge: 48 * time.Hour, MaxBytes: 11 * gib},
		{Name: StreamFlow, Subjects: []string{"flow.>"}, MaxAge: 48 * time.Hour, MaxBytes: 5 * gib},
		{Name: StreamAI, Subjects: []string{"ai.signal.>"}, MaxAge: 48 * time.Hour, MaxBytes: 1 * gib},
		{Name: StreamQuality, Subjects: []string{"quality.>"}, MaxAge: 48 * time.Hour, MaxBytes: 3 * gib},
	}
}

// LocalMinInterval is the shortest poll interval the local caps hold a worst-case day for
// (MD 11 GiB / 14.5 GB-at-5s ≈ 0.8 day → 5 s / 0.8 ≈ 6.3 s; rounded up with margin).
const LocalMinInterval = 15 * time.Second

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
	age, err := envDur("STREAM_MAX_AGE")
	if err != nil {
		return nil, "", err
	}
	if age != 0 && age < 24*time.Hour {
		return nil, "", fmt.Errorf("STREAM_MAX_AGE=%s: at least 24h (the engine and gateway replay the whole trading day)", age)
	}
	for i := range specs {
		if age > 0 {
			specs[i].MaxAge = age
		}
		k := "STREAM_" + specs[i].Name + "_MAX_GIB"
		n, err := envInt(k)
		if err != nil {
			return nil, "", err
		}
		if n != 0 {
			if n < 1 || n > 1024 {
				return nil, "", fmt.Errorf("%s=%d: want 1..1024", k, n)
			}
			specs[i].MaxBytes = n * gib
		}
		total += specs[i].MaxBytes
	}
	if profile == "local" && total > LocalTotalCap {
		return nil, "", fmt.Errorf("STREAM_PROFILE=local: streams reserve %d GiB, more than the %d GiB local cap", total/gib, LocalTotalCap/gib)
	}
	return specs, fmt.Sprintf("%s (%d GiB total)", profile, total/gib), nil
}

// envInt / envDur: unset or empty = 0; set but unparseable = error (a typo must not silently
// fall back to a default).
func envInt(k string) (int64, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: not an integer", k, v)
	}
	return n, nil
}

func envDur(k string) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: not a duration", k, v)
	}
	return d, nil
}
