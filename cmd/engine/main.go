// engine consumes snapshot envelopes (NDJSON on stdin) and publishes flow metrics (NDJSON on stdout).
//
//	collector | engine
package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"strings"

	"bourse/internal/anomaly"
	"bourse/internal/bus"
	"bourse/internal/config"
	"bourse/internal/flow"
	"bourse/internal/model"
)

func main() {
	log.SetOutput(os.Stderr)
	cfg := flow.DefaultConfig()
	cfg.HotThreshold = config.Int("HOT_THRESHOLD_RIAL", cfg.HotThreshold)
	cfg.PlusThreshold = config.Int("PLUS_THRESHOLD_RIAL", cfg.PlusThreshold)
	cfg.StaleAfter = config.Dur("STALE_AFTER", cfg.StaleAfter)
	eng := flow.New(cfg)
	radar := anomaly.New(anomaly.DefaultConfig())
	pub := bus.NewNDJSON(os.Stdout)

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	var n, bad int
	for sc.Scan() {
		var env struct {
			Subject string          `json:"subject"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &env); err != nil || !strings.HasPrefix(env.Subject, "md.snap.") {
			bad++
			continue
		}
		var s model.Snapshot
		if err := json.Unmarshal(env.Data, &s); err != nil {
			bad++
			continue
		}
		n++
		r := eng.Process(s)
		for _, i := range r.Issues {
			pub.Publish(bus.SubjQuality(s.InsCode), i)
		}
		for _, e := range r.Events {
			pub.Publish(bus.SubjFlow(s.InsCode), e)
		}
		if r.Game != nil {
			pub.Publish(bus.SubjGame(s.InsCode), r.Game)
		}
		if r.Window != nil {
			pub.Publish(bus.SubjWindow(s.InsCode), r.Window)
			if ev := radar.Divergence(r.Window); ev != nil {
				pub.Publish(bus.SubjAI(s.InsCode), ev)
			}
		}
		for _, ev := range radar.Observe(r.Interval) {
			pub.Publish(bus.SubjAI(s.InsCode), ev)
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("engine: read: %v", err)
	}
	log.Printf("engine: processed %d snapshots, %d unreadable lines", n, bad)
}
