package anomaly

import (
	"math/rand"
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/flow"
	"bourse/internal/model"
	"bourse/internal/tehran"
)

var day = time.Date(2026, 9, 23, 0, 0, 0, 0, tehran.Loc)

func iv(at time.Time, value, net int64) *flow.Interval {
	return &flow.Interval{InsCode: "X", From: at.Add(-5 * time.Second), To: at, Value: value, NetIndValue: net}
}

// feed returns the radar after n normal intervals starting at 09:30.
func feed(r *Radar, n int) time.Time {
	rng := rand.New(rand.NewSource(7))
	t := day.Add(9*time.Hour + 30*time.Minute)
	for i := 0; i < n; i++ {
		v := int64(100_000_000 + rng.Intn(40_000_000)) // 100–140M rial per 5s
		net := int64(rng.Intn(20_000_000) - 10_000_000)
		if evs := r.Observe(iv(t, v, net)); len(evs) != 0 {
			panic("normal data must not trigger anomalies")
		}
		t = t.Add(5 * time.Second)
	}
	return t
}

func TestNoScoreBeforeWarmUp(t *testing.T) {
	r := New(DefaultConfig())
	at := feed(r, DefaultConfig().WarmUp-1)
	if evs := r.Observe(iv(at, 10_000_000_000, 9_000_000_000)); len(evs) != 0 {
		t.Fatalf("no score before warm-up, got %+v", evs)
	}
}

func TestSpikeDetectedAfterWarmUp(t *testing.T) {
	r := New(DefaultConfig())
	at := feed(r, 200)
	evs := r.Observe(iv(at, 2_000_000_000, 1_500_000_000))
	var gotRate, gotNet bool
	for _, e := range evs {
		if e.Feature == FeatValueRate && e.Z > 4 {
			gotRate = true
		}
		if e.Feature == FeatNetInd && e.Z > 4 && e.Reason != "" {
			gotNet = true
		}
	}
	if !gotRate || !gotNet {
		t.Fatalf("expected value-rate and net-flow anomalies, got %+v", evs)
	}
}

// The opening skip is relative to the instrument's OWN open: a stock (09:00) is not scored at
// 09:05; a gold fund (12:00) is not scored at 12:10 but is at 12:20.
func TestOpeningWindowNotScored(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sessions = calendar.Default().WithInstruments(map[string]string{"X": "stock"})
	r := New(cfg)
	feed(r, 200)
	open := day.Add(9*time.Hour + 5*time.Minute)
	if evs := r.Observe(iv(open, 5_000_000_000, 4_000_000_000)); len(evs) != 0 {
		t.Fatalf("opening window must not be scored: %+v", evs)
	}
	cfg.Sessions = calendar.Default().WithInstruments(map[string]string{"X": "gold"})
	for _, c := range []struct {
		hm     time.Duration
		scored bool
	}{{12*time.Hour + 10*time.Minute, false}, {12*time.Hour + 20*time.Minute, true}} {
		r := New(cfg)
		feed(r, 200)
		evs := r.Observe(iv(day.Add(c.hm), 5_000_000_000, 4_000_000_000))
		if (len(evs) != 0) != c.scored {
			t.Errorf("gold fund at %s: %d events, scored want %v", c.hm, len(evs), c.scored)
		}
	}
}

func TestIlliquidIgnored(t *testing.T) {
	r := New(DefaultConfig())
	feed(r, 200)
	if evs := r.Observe(iv(day.Add(11*time.Hour), 1000, 1000)); len(evs) != 0 {
		t.Fatalf("below MinValueRate must be ignored: %+v", evs)
	}
}

func TestDivergenceOncePerWindow(t *testing.T) {
	r := New(DefaultConfig())
	ws := day.Add(10 * time.Hour)
	w := &model.TenMinute{InsCode: "X", WindowStart: ws, NetHot: 6_000_000_000, PriceOpen: 10_000, PriceLastV: 10_010}
	ev := r.Divergence(w)
	if ev == nil || ev.Kind != "absorption" {
		t.Fatalf("want absorption, got %+v", ev)
	}
	if again := r.Divergence(w); again != nil {
		t.Fatalf("must emit once per window")
	}
	w2 := &model.TenMinute{InsCode: "X", WindowStart: ws.Add(10 * time.Minute), NetHot: -6_000_000_000, PriceOpen: 10_000, PriceLastV: 10_000}
	if ev := r.Divergence(w2); ev == nil || ev.Kind != "support" {
		t.Fatalf("want support, got %+v", ev)
	}
	w3 := &model.TenMinute{InsCode: "X", WindowStart: ws.Add(20 * time.Minute), NetHot: 6_000_000_000, PriceOpen: 10_000, PriceLastV: 10_200}
	if ev := r.Divergence(w3); ev != nil {
		t.Fatalf("price rose 2%%: no divergence, got %+v", ev)
	}
}
