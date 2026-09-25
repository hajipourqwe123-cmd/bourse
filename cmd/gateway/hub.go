package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"bourse/internal/anomaly"
	"bourse/internal/bus"
	"bourse/internal/market"
	"bourse/internal/model"
	"bourse/internal/tehran"
)

// Centrifugo channels (contracts/subjects.md).
const (
	chSummary = "mkt:summary"
	chSymbols = "mkt:symbols"
	chRadar   = "radar:signals"
	chHot     = "flow:hot"
	chSym     = "sym:" // + ins_code
)

// Channel payloads. gw is the gateway's publish time; together with each value's source time
// (src / as_of) it lets the browser measure source→browser latency.
type (
	symbolsMsg struct {
		Seq  uint64       `json:"seq"`
		Day  string       `json:"day"` // a client holding another day re-reads /api/v1/state
		GW   time.Time    `json:"gw"`
		Rows []market.Row `json:"rows"`
	}
	summaryMsg struct {
		GW      time.Time      `json:"gw"`
		Summary market.Summary `json:"summary"` // summary.day: as for symbolsMsg.Day
	}
	signalMsg struct {
		GW     time.Time     `json:"gw"`
		Signal market.Signal `json:"signal"`
	}
	hotMsg struct {
		GW    time.Time       `json:"gw"`
		Event model.FlowEvent `json:"event"`
	}
	rowMsg struct {
		GW  time.Time  `json:"gw"`
		Row market.Row `json:"row"`
	}
)

// hub owns the market state: bus messages update it, the loop publishes changes every tick.
type hub struct {
	cfg   Config
	pub   publisher
	valid func() error     // lease validity, checked before every publish
	now   func() time.Time // wall clock: drives the trading day

	mu          sync.Mutex
	st          *market.State
	seq         uint64 // last mkt:symbols sequence handed to Centrifugo
	lastSummary []byte
	radar       []market.Signal
	hot         []model.FlowEvent
	pending     int // tails not yet caught up
	ready       bool
	undecodable int
	synSkipped  bool
	synIns      map[string]bool // instruments seen with synthetic snapshots
}

func newHub(cfg Config, pub publisher, valid func() error, now func() time.Time) *hub {
	h := &hub{cfg: cfg, pub: pub, valid: valid, now: now, st: market.New(cfg.Market), pending: 4, synIns: map[string]bool{}}
	h.st.SetSynthetic(h.synthetic, cfg.AllowSynthetic)
	h.st.Advance(tehran.TradingDay(h.now()))
	return h
}

type tail struct {
	stream, filter string
	start          time.Time
	fn             func(bus.Msg) error
	caughtUp       func()
}

// tails lists the four streams the gateway follows, each from today's Tehran midnight (store
// time), so the state of the whole trading day is rebuilt on start.
func (h *hub) tails() []tail {
	start := tehran.DayStart(h.now())
	mk := func(stream, filter string, fn func(bus.Msg)) tail {
		return tail{stream: stream, filter: filter, start: start,
			fn:       func(m bus.Msg) error { h.mu.Lock(); defer h.mu.Unlock(); fn(m); return nil },
			caughtUp: h.caughtUp}
	}
	return []tail{
		mk(bus.StreamMD, "md.snap.>", h.onSnapshot),
		mk(bus.StreamFlow, "flow.>", h.onFlow),
		mk(bus.StreamAI, "ai.signal.>", h.onSignal),
		mk(bus.StreamQuality, "quality.>", h.onIssue),
	}
}

func (h *hub) caughtUp() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending--
	if h.pending == 0 {
		h.ready = true
		log.Printf("gateway: state rebuilt for %s: %d instruments; publishing", h.st.Day(), len(h.st.Rows()))
	}
}

// synthetic reports an instrument code of SYNTHETIC data (rule 5), or one seen with synthetic
// snapshots (model.IsSynthetic: synthetic source, SYN* code or symbol, re-timed recording).
func (h *hub) synthetic(ins string) bool { return strings.HasPrefix(ins, "SYN") || h.synIns[ins] }

// skipSynthetic drops synthetic data unless explicitly allowed (logged once).
func (h *hub) skipSynthetic(ins string) bool { return h.skip(ins, h.synthetic(ins)) }

func (h *hub) skip(ins string, syn bool) bool {
	if h.cfg.AllowSynthetic || !syn {
		return false
	}
	if !h.synSkipped {
		h.synSkipped = true
		log.Printf("gateway: synthetic instrument %s on the bus ignored (rule 5; ALLOW_SYNTHETIC_ON_BUS=1 only on a disposable local stack)", ins)
	}
	return true
}

func (h *hub) bad(m bus.Msg, err error) {
	h.undecodable++
	if h.undecodable <= 10 {
		log.Printf("gateway: %s seq %d undecodable, ignored: %v", m.Subject, m.StreamSeq, err)
	}
}

func (h *hub) onSnapshot(m bus.Msg) {
	s, err := model.DecodeSnapshot(m.Data)
	if err != nil {
		h.bad(m, err)
		return
	}
	if model.IsSynthetic(&s) {
		h.synIns[s.InsCode] = true
	}
	if h.skip(s.InsCode, h.synthetic(s.InsCode)) {
		return
	}
	h.st.ApplySnapshot(s)
}

func (h *hub) onFlow(m bus.Msg) {
	switch {
	case strings.HasPrefix(m.Subject, "flow.game."):
		var g model.GameTotals
		if err := json.Unmarshal(m.Data, &g); err != nil || g.InsCode == "" || g.Day == "" {
			h.bad(m, err)
			return
		}
		if !h.skipSynthetic(g.InsCode) {
			h.st.ApplyGame(g)
		}
	case strings.HasPrefix(m.Subject, "flow.event."):
		var e model.FlowEvent
		if err := json.Unmarshal(m.Data, &e); err != nil || e.InsCode == "" {
			h.bad(m, err)
			return
		}
		// Live only (not during the rebuild), hot band only, today only.
		if h.ready && e.Band == model.BandHot && !h.skipSynthetic(e.InsCode) &&
			tehran.TradingDay(e.IntervalTo) == h.st.Day() {
			h.hot = append(h.hot, e)
		}
	}
}

func (h *hub) onSignal(m bus.Msg) {
	var e anomaly.Event
	if err := json.Unmarshal(m.Data, &e); err != nil || e.InsCode == "" || e.At.IsZero() {
		h.bad(m, err)
		return
	}
	if h.skipSynthetic(e.InsCode) {
		return
	}
	if sig, ok := h.st.ApplySignal(e); ok && h.ready {
		h.radar = append(h.radar, sig)
	}
}

func (h *hub) onIssue(m bus.Msg) {
	var q model.QualityIssue
	if err := json.Unmarshal(m.Data, &q); err != nil || q.Code == "" || q.At.IsZero() {
		h.bad(m, err)
		return
	}
	if !h.skipSynthetic(q.InsCode) {
		h.st.ApplyIssue(q)
	}
}

// loop publishes every tick until ctx ends; it returns on a lost lease.
func (h *hub) loop(ctx context.Context) error {
	t := time.NewTicker(h.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if err := h.flush(ctx); err != nil {
			return err
		}
	}
}

// flush publishes what changed since the last tick in one batch. A failed publish is logged and
// the summary is re-sent next tick; lost row deltas are recovered by clients from the sequence
// gap (they re-read /api/v1/state).
func (h *hub) flush(ctx context.Context) error {
	h.mu.Lock()
	if !h.ready {
		h.mu.Unlock()
		return nil
	}
	now := h.now()
	if h.st.Advance(tehran.TradingDay(now)) {
		log.Printf("gateway: new trading day %s", h.st.Day())
	}
	var pubs []publication
	rows := h.st.Dirty()
	if len(rows) > 0 {
		h.seq++
		pubs = append(pubs, publication{Channel: chSymbols, Data: symbolsMsg{Seq: h.seq, Day: h.st.Day(), GW: now, Rows: rows}})
		for _, r := range rows {
			pubs = append(pubs, publication{Channel: chSym + r.Ins, Data: rowMsg{GW: now, Row: r}})
		}
	}
	sum := h.st.Summary()
	sb, _ := json.Marshal(sum)
	summaryChanged := !bytes.Equal(sb, h.lastSummary)
	if summaryChanged {
		h.lastSummary = sb
		pubs = append(pubs, publication{Channel: chSummary, Data: summaryMsg{GW: now, Summary: sum}})
	}
	// Radar and hot items: synthetic ones are decided now (their snapshot may have arrived after
	// them), left out or labelled.
	radar, hot := h.radar, h.hot
	h.radar, h.hot = nil, nil
	for _, s := range radar {
		if h.skip(s.Ins, h.synthetic(s.Ins)) {
			continue
		}
		s.Syn = h.synthetic(s.Ins)
		pubs = append(pubs, publication{Channel: chRadar, Data: signalMsg{GW: now, Signal: s}})
	}
	for _, e := range hot {
		if !h.skipSynthetic(e.InsCode) {
			pubs = append(pubs, publication{Channel: chHot, Data: hotMsg{GW: now, Event: e}})
		}
	}
	h.mu.Unlock()

	if len(pubs) == 0 {
		return nil
	}
	if err := h.valid(); err != nil {
		return err // not ours to publish any more
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.pub.Publish(pctx, pubs); err != nil {
		// Re-send next tick: the summary, and the rows (as a new sequence: clients that missed
		// this one see a gap and re-read the state, so nothing stays stale after the close).
		log.Printf("gateway: %v", err)
		ins := make([]string, len(rows))
		for i, r := range rows {
			ins[i] = r.Ins
		}
		h.mu.Lock()
		h.st.MarkDirty(ins)
		if summaryChanged {
			h.lastSummary = nil
		}
		h.radar, h.hot = append(radar, h.radar...), append(hot, h.hot...)
		h.mu.Unlock()
	}
	return nil
}

// stateMsg is GET /api/v1/state.
type stateMsg struct {
	Seq                uint64               `json:"seq"`
	Now                time.Time            `json:"now"`
	Day                string               `json:"day"`
	Sessions           []market.SessionInfo `json:"sessions"`
	CalendarUnverified bool                 `json:"calendar_unverified"`
	HotThreshold       int64                `json:"hot_threshold"`
	PlusThreshold      int64                `json:"plus_threshold"`
	StaleAfterMs       int64                `json:"stale_after_ms"`
	CentrifugoWS       string               `json:"centrifugo_ws"`
	DevToken           bool                 `json:"dev_token"`
	Summary            market.Summary       `json:"summary"`
	Rows               []market.Row         `json:"rows"`
	Radar              []market.Signal      `json:"radar"`
}

// state returns the full state (false until the rebuild is complete). Rows may include changes
// not yet published: the next delta repeats them, which clients apply idempotently.
func (h *hub) state(now time.Time) (stateMsg, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.ready {
		return stateMsg{}, false
	}
	return stateMsg{
		Seq: h.seq, Now: now, Day: h.st.Day(),
		Sessions:           market.Sessions(h.cfg.Market.Sessions, now),
		CalendarUnverified: h.cfg.Market.Sessions.Unverified() > 0,
		HotThreshold:       h.cfg.Market.HotThreshold, PlusThreshold: h.cfg.Market.PlusThreshold,
		StaleAfterMs: h.cfg.StaleAfter.Milliseconds(), CentrifugoWS: h.cfg.CentrifugoWS, DevToken: h.cfg.DevToken,
		Summary: h.st.Summary(), Rows: h.st.Rows(), Radar: h.st.Radar(),
	}, true
}
