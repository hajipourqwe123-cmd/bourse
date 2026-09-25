// Package anomaly is the first, deterministic tier of the AI layer (PRD AI-01, AI-02).
//
// AI-01 Live anomaly radar: for every instrument it keeps an exponentially weighted mean and
// variance of per-second flow features and scores each new interval against the instrument's OWN
// history. No score is produced until the baseline has enough observations (warm-up): an
// instrument without history is "insufficient history", never "normal".
//
// AI-02 Flow–price divergence: inside a 10-minute window, large net hot buying with a flat or
// falling price ("absorption": hidden supply) and large net hot selling with a flat or rising price
// ("support": hidden demand) are flagged once per window.
//
// Known limitation (Phase 2 fix): the baseline has no time-of-day profile, so the opening window
// (the first OpeningSkip after the instrument's OWN session open, from the session calendar) is
// not scored at all; nor is anything on a day its market is closed.
package anomaly

import (
	"math"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/flow"
	"bourse/internal/model"
)

// Config holds the radar's parameters.
type Config struct {
	Alpha          float64            // EWMA weight of the newest observation (default 0.02)
	WarmUp         int                // observations required before scoring (default 60)
	ZThreshold     float64            // |z| at or above which an anomaly is emitted (default 4)
	MinValueRate   float64            // rial/second floor below which intervals are ignored (default 1e6)
	OpeningSkip    time.Duration      // after the instrument's own session open; default 15m
	Sessions       *calendar.Calendar // trading sessions per instrument (default: the embedded calendar)
	DivergenceHot  int64              // |net hot| in a window that qualifies for AI-02 (default 5e9 rial)
	DivergenceFlat float64            // price change %, at or below/above which price is "flat" (default 0.2)
}

// DefaultConfig returns conservative defaults; all are design assumptions to be tuned on real data.
func DefaultConfig() Config {
	return Config{Alpha: 0.02, WarmUp: 60, ZThreshold: 4, MinValueRate: 1e6,
		OpeningSkip: 15 * time.Minute, Sessions: calendar.Default(), DivergenceHot: 5_000_000_000, DivergenceFlat: 0.2}
}

// Feature names.
const (
	FeatValueRate = "value_rate"   // rial/second traded
	FeatNetInd    = "net_ind_rate" // rial/second, real-person net flow
)

// Event is one anomaly (AI-01) or divergence (AI-02) signal.
type Event struct {
	InsCode string    `json:"ins_code"`
	Kind    string    `json:"kind"` // "anomaly" | "absorption" | "support"
	Feature string    `json:"feature,omitempty"`
	Z       float64   `json:"z,omitempty"`
	Value   float64   `json:"value"`
	Mean    float64   `json:"mean,omitempty"`
	At      time.Time `json:"at"`
	Reason  string    `json:"reason"` // human-readable, shown to users verbatim
}

type ewma struct {
	n        int
	mean, vr float64
}

func (e *ewma) score(x float64) (z float64, ok bool) {
	if e.vr <= 0 {
		return 0, false
	}
	return (x - e.mean) / math.Sqrt(e.vr), true
}

func (e *ewma) update(x, alpha float64) {
	if e.n == 0 {
		e.mean, e.n = x, 1
		return
	}
	d := x - e.mean
	e.mean += alpha * d
	e.vr = (1 - alpha) * (e.vr + alpha*d*d)
	e.n++
}

type symState struct {
	feats    map[string]*ewma
	lastDivW time.Time
}

// Radar is NOT safe for concurrent use; shard by InsCode like flow.Engine.
type Radar struct {
	cfg   Config
	state map[string]*symState
}

// New returns a radar.
func New(cfg Config) *Radar {
	if cfg.Sessions == nil {
		cfg.Sessions = calendar.Default()
	}
	return &Radar{cfg: cfg, state: map[string]*symState{}}
}

// scoring reports whether an interval ending at t is scored: the market is open that day and t
// is past the opening skip. An unmapped (unknown-class) instrument could be in any class, so it
// is skipped after EVERY class's open that day.
func (r *Radar) scoring(ins string, t time.Time) (scored, open bool) {
	sess, open := r.cfg.Sessions.Session(ins, t)
	if !open {
		return false, false
	}
	opens := []time.Time{sess.Open}
	if sess.Class == calendar.Unknown {
		opens = r.cfg.Sessions.Opens(t)
	}
	for _, o := range opens {
		if !t.Before(o) && t.Before(o.Add(r.cfg.OpeningSkip)) {
			return false, true
		}
	}
	return !t.Before(sess.Open.Add(r.cfg.OpeningSkip)), true
}

func (r *Radar) sym(ins string) *symState {
	st := r.state[ins]
	if st == nil {
		st = &symState{feats: map[string]*ewma{}}
		r.state[ins] = st
	}
	return st
}

// Observe scores one interval (AI-01). Scoring uses the baseline BEFORE the update, so an anomaly
// does not dampen its own z-score.
func (r *Radar) Observe(iv *flow.Interval) []Event {
	if iv == nil {
		return nil
	}
	secs := iv.To.Sub(iv.From).Seconds()
	if secs <= 0 {
		return nil
	}
	rate := float64(iv.Value) / secs
	if rate < r.cfg.MinValueRate {
		return nil
	}
	scoring, open := r.scoring(iv.InsCode, iv.To)
	if !open {
		return nil // a closed day (e.g. an unlisted holiday) must not train the baseline either
	}

	st := r.sym(iv.InsCode)
	var out []Event
	for _, f := range []struct {
		name string
		x    float64
	}{{FeatValueRate, rate}, {FeatNetInd, float64(iv.NetIndValue) / secs}} {
		e := st.feats[f.name]
		if e == nil {
			e = &ewma{}
			st.feats[f.name] = e
		}
		if scoring && e.n >= r.cfg.WarmUp {
			if z, ok := e.score(f.x); ok && math.Abs(z) >= r.cfg.ZThreshold {
				out = append(out, Event{InsCode: iv.InsCode, Kind: "anomaly", Feature: f.name, Z: round2(z),
					Value: f.x, Mean: e.mean, At: iv.To, Reason: reason(f.name, z)})
			}
		}
		e.update(f.x, r.cfg.Alpha)
	}
	return out
}

func reason(feat string, z float64) string {
	dir := "بالاتر"
	if z < 0 {
		dir = "پایین‌تر"
	}
	switch feat {
	case FeatValueRate:
		return "شدت معاملات به‌طور غیرعادی " + dir + " از رفتار معمول همین نماد است"
	case FeatNetInd:
		if z > 0 {
			return "ورود پول حقیقی به‌طور غیرعادی بالاتر از رفتار معمول همین نماد است"
		}
		return "خروج پول حقیقی به‌طور غیرعادی بالاتر از رفتار معمول همین نماد است"
	}
	return ""
}

// Divergence checks one 10-minute window (AI-02). It emits at most one event per window.
func (r *Radar) Divergence(w *model.TenMinute) *Event {
	if w == nil || w.PriceOpen <= 0 {
		return nil
	}
	st := r.sym(w.InsCode)
	if st.lastDivW.Equal(w.WindowStart) {
		return nil
	}
	chg := w.ChangePct()
	var ev *Event
	switch {
	case w.NetHot >= r.cfg.DivergenceHot && chg <= r.cfg.DivergenceFlat:
		ev = &Event{Kind: "absorption", Reason: "ورود پول درشت بدون رشد قیمت: احتمال عرضه پنهان"}
	case w.NetHot <= -r.cfg.DivergenceHot && chg >= -r.cfg.DivergenceFlat:
		ev = &Event{Kind: "support", Reason: "خروج پول درشت بدون افت قیمت: احتمال تقاضای پنهان"}
	default:
		return nil
	}
	ev.InsCode, ev.Value, ev.At = w.InsCode, float64(w.NetHot), w.WindowStart
	st.lastDivW = w.WindowStart
	return ev
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
