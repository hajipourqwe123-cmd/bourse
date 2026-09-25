// Package flow turns successive snapshots into money-flow metrics: hot money events, the
// three-band "market game" split and the 10-minute "big moves" matrix.
//
// Method (published to users): the source only exposes day-to-date totals per instrument, so each
// metric is computed on the difference between two consecutive snapshots of the same trading day.
// For one side (buy or sell) of real persons in an interval:
//
//	value      = Δvolume_side × VWAP_interval,   VWAP_interval = Δvalue_total / Δvolume_total
//	avg ticket = value / Δparticipants_side      (only when Δparticipants_side > 0)
//
// Participant counts are distinct-per-day, so an interval where people already counted trade again
// has Δparticipants = 0: its flow is reported as "unattributed" and never assigned to a size band.
package flow

import (
	"fmt"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// Config holds the thresholds. Monetary values are in rial.
type Config struct {
	HotThreshold  int64              // default 2_000_000_000 rial = 200M toman
	PlusThreshold int64              // default 1_000_000_000 rial = 100M toman
	StaleAfter    time.Duration      // default 30s
	Sessions      *calendar.Calendar // trading sessions per instrument (default: the embedded calendar)
}

// DefaultConfig mirrors the PRD defaults.
func DefaultConfig() Config {
	return Config{HotThreshold: 2_000_000_000, PlusThreshold: 1_000_000_000, StaleAfter: 30 * time.Second,
		Sessions: calendar.Default()}
}

// Interval summarises one accepted same-day interval; it feeds the AI layer (internal/anomaly).
type Interval struct {
	InsCode     string
	From, To    time.Time
	Value       int64 // rial traded in the interval (all participants)
	NetIndValue int64 // rial, real-person buy value − real-person sell value
	PriceFrom   int64
	PriceTo     int64
}

// Result is everything one snapshot produced.
type Result struct {
	Interval *Interval // nil when no usable interval was formed
	Events   []model.FlowEvent
	Issues   []model.QualityIssue
	Window   *model.TenMinute  // current 10-minute window after this snapshot (nil if not updated)
	Game     *model.GameTotals // day totals after this snapshot (nil if not updated)
}

type symState struct {
	prev *model.Snapshot // last snapshot accepted as a baseline (complete, in order)
	day  string
	game model.GameTotals
	win  *model.TenMinute
	// partialWin is the window holding a late day baseline (zero when the day is complete).
	partialWin time.Time
}

// Engine is NOT safe for concurrent use; shard instruments across engines by InsCode.
type Engine struct {
	cfg   Config
	state map[string]*symState
}

// New returns an engine with cfg.
func New(cfg Config) *Engine { return &Engine{cfg: cfg, state: map[string]*symState{}} }

func (e *Engine) band(avg int64) model.Band {
	switch {
	case avg >= e.cfg.HotThreshold:
		return model.BandHot
	case avg >= e.cfg.PlusThreshold:
		return model.BandHotPlus
	default:
		return model.BandRetail
	}
}

// Process consumes one snapshot.
func (e *Engine) Process(s model.Snapshot) Result {
	var r Result
	class := e.cfg.Sessions.Class(s.InsCode)
	sess, open := e.cfg.Sessions.Session(s.InsCode, s.SourceTime)
	inSession := false
	if is, ok := e.cfg.Sessions.Session(s.InsCode, s.IngestTime); ok {
		inSession = is.Trading(s.IngestTime)
	}
	r.Issues = quality.CheckSingle(&s, e.cfg.StaleAfter, inSession)
	for _, f := range model.FlowFields {
		if !s.Has(f) {
			return r // incomplete: never a baseline, never a metric
		}
	}

	st := e.state[s.InsCode]
	day := tehran.TradingDay(s.SourceTime)
	if st != nil && st.prev != nil && day < st.day { // YYYY-MM-DD compares chronologically
		// A late snapshot of an earlier day must not reset today's state.
		r.Issues = append(r.Issues, quality.EarlierDay(&s, st.prev))
		return r
	}
	if st == nil || st.day != day || st.prev == nil {
		st = &symState{day: day, game: model.GameTotals{InsCode: s.InsCode, Class: class, Day: day}}
		e.state[s.InsCode] = st
		cp := s
		st.prev = &cp
		// Late-start rule: the day is complete only if this first accepted baseline was taken
		// before the instrument's own open with zero day-to-date volume. Otherwise trades before
		// it are missing from every total of the day (over-marking is accepted).
		preOpenZero := s.Volume == 0 && (!open || s.SourceTime.Before(sess.Open))
		if !preOpenZero {
			st.game.Partial = true
			st.partialWin = windowStart(sess, open, s.SourceTime)
			when := "no session that day"
			if open {
				when = "session opens " + sess.Open.In(tehran.Loc).Format("15:04")
			}
			r.Issues = append(r.Issues, quality.DayStart(&s, fmt.Sprintf(
				"first accepted snapshot of %s at %s (%s) already has day volume %d: earlier trades are not counted; day totals and this 10-minute window are partial",
				day, s.SourceTime.In(tehran.Loc).Format("15:04:05"), when, s.Volume)))
		}
		return r // first snapshot of the day: no interval yet
	}

	d, iss, ok := quality.Diff(st.prev, &s)
	r.Issues = append(r.Issues, iss...)
	if !ok {
		if len(iss) > 0 && iss[0].Code != quality.OutOfOrder {
			cp := s // re-baseline on inconsistent totals; the bad interval is dropped
			st.prev = &cp
		}
		return r
	}

	from := st.prev.SourceTime
	var netHot int64
	if d.Volume > 0 {
		vwap := d.Value / d.Volume
		sides := []struct {
			side  model.Side
			vol   int64
			count int64
			sign  int64
		}{{model.Buy, d.IndBuyVol, d.IndBuyCount, 1}, {model.Sell, d.IndSellVol, d.IndSellCount, -1}}
		for _, sd := range sides {
			if sd.vol <= 0 {
				continue
			}
			value := int64(float64(d.Value) * float64(sd.vol) / float64(d.Volume))
			ev := model.FlowEvent{InsCode: s.InsCode, Symbol: s.Symbol, Class: class, Side: sd.side,
				IntervalFrom: from, IntervalTo: s.SourceTime, Volume: sd.vol, Value: value,
				VWAP: vwap, PriceLast: s.PriceLast}
			if sd.count > 0 {
				ev.Attribution = model.Attributed
				ev.Participants = sd.count
				ev.AvgTicket = value / sd.count
				ev.Band = e.band(ev.AvgTicket)
				switch ev.Band {
				case model.BandHot:
					st.game.NetHot += sd.sign * value
					netHot += sd.sign * value
				case model.BandHotPlus:
					st.game.NetHotPlus += sd.sign * value
				default:
					st.game.NetRetail += sd.sign * value
				}
				if ev.Band != model.BandRetail {
					r.Events = append(r.Events, ev)
				}
			} else {
				ev.Attribution = model.Unattributed
				st.game.NetUnattributed += sd.sign * value
				if value >= e.cfg.HotThreshold {
					ev.Band = model.BandHot // size of the interval, not of a participant; UI must say so
					r.Events = append(r.Events, ev)
				}
			}
		}
		g := st.game
		r.Game = &g
		buyV := int64(float64(d.Value) * float64(d.IndBuyVol) / float64(d.Volume))
		sellV := int64(float64(d.Value) * float64(d.IndSellVol) / float64(d.Volume))
		r.Interval = &Interval{InsCode: s.InsCode, From: from, To: s.SourceTime, Value: d.Value,
			NetIndValue: buyV - sellV, PriceFrom: st.prev.PriceLast, PriceTo: s.PriceLast}
	}

	ws := windowStart(sess, open, s.SourceTime)
	if st.win == nil || !st.win.WindowStart.Equal(ws) {
		st.win = &model.TenMinute{InsCode: s.InsCode, Class: class, WindowStart: ws, PriceOpen: st.prev.PriceLast,
			Partial: !st.partialWin.IsZero() && !ws.After(st.partialWin)}
	}
	st.win.NetHot += netHot
	st.win.PriceLastV = s.PriceLast
	w := *st.win
	r.Window = &w

	cp := s
	st.prev = &cp
	return r
}

// windowStart is the session-relative 10-minute window of t (clock-aligned when the market is
// closed that day).
func windowStart(sess calendar.Session, open bool, t time.Time) time.Time {
	if open {
		return calendar.WindowStart(sess, t)
	}
	return tehran.Floor10m(t)
}
