// Package config reads settings from the environment. Secrets are only ever read here.
package config

import (
	"os"
	"strconv"
	"time"
)

// Str returns env k or def.
func Str(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

// Dur returns env k parsed as a duration, or def.
func Dur(k string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

// Int returns env k parsed as int64, or def.
func Int(k string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(k), 10, 64); err == nil {
		return v
	}
	return def
}
