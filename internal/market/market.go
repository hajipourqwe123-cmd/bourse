// Package market builds the market dashboard's state (G-01) from the engine's bus outputs:
// md.snap (latest snapshot per instrument), flow.game (latest day totals), ai.signal and quality.
// It is pure (no I/O, no clock) and not safe for concurrent use; cmd/gateway owns the locking
// and advances the trading day from the wall clock.
//
// Every formula is published in docs/market-metrics.md. Rules that shape every value:
//   - Missing inputs give a null value (a nil pointer), never 0 (rule 1). A 0 is only ever a
//     measured zero (e.g. flow of an instrument that has not traded today).
//   - Aggregates are per instrument class from the session calendar; class "unknown" is never
//     part of a class aggregate, only counted.
//   - Every aggregate carries AsOf (latest source_time of its inputs), Est (an input's source
//     time was estimated by the collector), Instruments (instruments of the aggregate seen today)
//     and Missing (those of them whose input is missing: not in the value).
//   - The state holds exactly one Tehran trading day (Advance); data of any other day is dropped.
package market

import (
	"sort"
	"time"

	"bourse/internal/anomaly"
	"bourse/internal/calendar"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// Instrument classes (calendar class names).
const (
	Stock       = "stock"
	EquityETF   = "equity_etf"
	OtherFund   = "other_fund"
	FixedIncome = "fixed_income"
	Gold        = "gold"
	Silver      = "silver"
)

// Classes lists the classes that get a flow aggregate, in display order (board: flow tabs).
var Classes = []string{Stock, EquityETF, Gold, Silver, FixedIncome, OtherFund}

// Window is the KPI sparkline bar width (Tehran clock).
const Window = 10 * time.Minute

// Config holds the parameters of the aggregates.
type Config struct {
	Sessions      *calendar.Calendar
	HotThreshold  int64   // rial; must equal the engine's (HOT_THRESHOLD_RIAL): labels the "large" band
	PlusThreshold int64   // rial; must equal the engine's (PLUS_THRESHOLD_RIAL): labels the "medium" band
	Materiality   float64 // pattern note: a band's |net| must reach this share of the class's traded value
	RadarKeep     int     // recent signals kept for the initial state
	SeriesBars    int     // KPI sparkline length (10-minute bars)
	// GapAfter: a longer interval between two snapshots of an instrument crosses windows whose
	// share of its trading is unknown: they are marked partial (as the engine's GapAfter).
	GapAfter time.Duration
	// LagAfter: day totals older than the instrument's latest snapshot by more than this, while
	// that snapshot shows more volume than the totals include, are "lagging" (engine behind).
	LagAfter time.Duration
}

// DefaultConfig mirrors flow.DefaultConfig's thresholds.
func DefaultConfig() Config {
	return Config{Sessions: calendar.Default(), HotThreshold: 2_000_000_000, PlusThreshold: 1_000_000_000,
		Materiality: 0.01, RadarKeep: 50, SeriesBars: 36, GapAfter: 30 * time.Second, LagAfter: 30 * time.Second}
}

// Row is one instrument in the symbols table (and one tile of the market map).
type Row struct {
	Ins   string   `json:"ins"`
	Sym   string   `json:"sym"`
	Class string   `json:"class"`
	Last  *int64   `json:"last"`  // rial, last trade price; null = missing
	Chg   *float64 `json:"chg"`   // percent vs price_yesterday; null = missing
	Value *int64   `json:"value"` // rial, day-to-date traded value; null = missing
	// NetHot is the day's attributed large (hot) real-person net flow in rial. Null when the
	// instrument traded today (or its volume is unknown) but no day totals arrived from the engine.
	NetHot  *int64     `json:"net_hot"`
	HotAsOf *time.Time `json:"hot_as_of"` // source time the totals are as of; null without totals
	Lagging bool       `json:"lagging"`   // totals do not cover the latest snapshot's trading yet
	Traded  bool       `json:"traded"`    // a snapshot of today showed trading; else chg is null (no trade today)
	Partial bool       `json:"partial"`   // day flow totals are partial (docs/data-quality.md DAY_START_MISSED)
	Missing []string   `json:"missing,omitempty"`
	Src     time.Time  `json:"src"` // source_time of the latest snapshot
	Ing     time.Time  `json:"ing"` // ingest_time of the latest snapshot
	Est     bool       `json:"est,omitempty"`
	Syn     bool       `json:"syn,omitempty"`
	Issues  int        `json:"issues"` // quality issues today (SOURCE_TIME_ESTIMATED excluded)
}

// Unavailable describes a metric the current data contract cannot provide.
type Unavailable struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// Pattern is the rule-based note on a class's flow bands.
type Pattern struct {
	Rule string `json:"rule"` // hot_out_retail_in | hot_in_retail_out | both_in | both_out | none
	Text string `json:"text"`
}

// ClassFlow is the real-person money flow of one class, split by band (rial, net = buy − sell).
type ClassFlow struct {
	Class       string `json:"class"`
	Instruments int    `json:"instruments"` // instruments of the class seen today
	// Missing counts instruments that traded (or whose volume is unknown) without day totals:
	// their flow is not in the sums. Lagging counts instruments whose totals are in the sums but
	// behind their latest snapshot. ValueMissing counts contributors without a traded value (the
	// pattern's materiality base would be understated).
	Missing         int       `json:"missing"`
	Lagging         int       `json:"lagging"`
	ValueMissing    int       `json:"value_missing"`
	NetHot          *int64    `json:"net_hot"` // null when no instrument contributes
	NetHotPlus      *int64    `json:"net_hot_plus"`
	NetRetail       *int64    `json:"net_retail"`
	NetUnattributed *int64    `json:"net_unattributed"`
	Value           *int64    `json:"value"`   // Σ day value of the contributors (materiality base)
	Partial         bool      `json:"partial"` // a contributor's day totals are partial
	AsOf            time.Time `json:"as_of"`
	Est             bool      `json:"est,omitempty"`
	Pattern         *Pattern  `json:"pattern"` // null unless every input is complete and current
}

// Bar is the value traded in one 10-minute window (Tehran clock).
type Bar struct {
	At      time.Time `json:"at"`
	Value   *int64    `json:"value"`   // null: no snapshot of the group was observed in the window
	Partial bool      `json:"partial"` // part of the window's trading is not attributable to it
}

// Secondary is a class shown on another class's KPI card (equity ETFs on the stock card).
type Secondary struct {
	Class       string `json:"class"`
	Value       *int64 `json:"value"`
	Instruments int    `json:"instruments"`
	Missing     int    `json:"missing"`
}

// KPI is one card of the KPI row.
type KPI struct {
	ID          string     `json:"id"` // index_total | value_stock | value_fixed | value_metals
	Available   bool       `json:"available"`
	Reason      string     `json:"reason,omitempty"`
	Classes     []string   `json:"classes,omitempty"`
	Value       *int64     `json:"value"` // rial, Σ day-to-date value; null = no instrument with a value
	Instruments int        `json:"instruments"`
	Missing     int        `json:"missing"` // instruments whose value is missing (not in Value)
	AsOf        time.Time  `json:"as_of"`
	Est         bool       `json:"est,omitempty"`
	Series      []Bar      `json:"series"`
	Secondary   *Secondary `json:"secondary,omitempty"`
	Note        string     `json:"note,omitempty"`
}

// Breadth counts stock-class instruments that traded today by price change vs yesterday.
type Breadth struct {
	Class       string    `json:"class"`
	Instruments int       `json:"instruments"` // stock instruments seen today
	Missing     int       `json:"missing"`     // no last price or no yesterday price
	Untraded    int       `json:"untraded"`    // no trade today: not in the buckets
	Floor       int       `json:"floor"`       // last ≤ the −3 % limit price  («در کف دامنه»)
	Down        int       `json:"down"`        // below yesterday, above the floor
	Flat        int       `json:"flat"`        // last = yesterday
	Up          int       `json:"up"`          // above yesterday, below the ceiling
	Ceil        int       `json:"ceil"`        // last ≥ the +3 % limit price  («در سقف دامنه»)
	AsOf        time.Time `json:"as_of"`
	Est         bool      `json:"est,omitempty"`
}

// Summary is everything above the symbols table.
type Summary struct {
	Day          string    `json:"day"`
	AsOf         time.Time `json:"as_of"`
	Syn          bool      `json:"syn"`
	Est          bool      `json:"est"`
	Instruments  int       `json:"instruments"`
	Unknown      int       `json:"unknown"`
	UnknownShare float64   `json:"unknown_share"`
	// UnknownNotice: most instruments (more than half) have no class, so per-class figures cover
	// a minority of the market; the dashboard says so instead of showing near-empty class cards.
	UnknownNotice bool `json:"unknown_notice"`
	// Carryover counts instruments whose only snapshots today are pre-open ones already showing
	// trading (the vendor's previous-day totals): ignored until real data of today arrives.
	Carryover int         `json:"carryover"`
	Issues    int         `json:"issues"`
	Flows     []ClassFlow `json:"flows"`
	KPIs      []KPI       `json:"kpis"`
	Breadth   Breadth     `json:"breadth"`
	Queues    Unavailable `json:"queues"`
}

// Signal is one radar item (AI-01 anomaly or AI-02 divergence).
type Signal struct {
	Ins    string    `json:"ins"`
	Sym    string    `json:"sym"`
	Class  string    `json:"class"`
	Kind   string    `json:"kind"` // anomaly | absorption | support
	Z      float64   `json:"z,omitempty"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	Syn    bool      `json:"syn,omitempty"`
}

// Unavailable metrics of the current data contract (docs/phase1-plan.md D-03).
var (
	IndicesUnavailable = Unavailable{Reason: "شاخص‌ها در قرارداد داده فعلی و پاسخ فروشنده نیستند (D-03)"}
	QueuesUnavailable  = Unavailable{Reason: "سطح یک دفتر سفارش در قرارداد داده فعلی و پاسخ فروشنده نیست (D-03)"}
)

// NoteBlockTrades labels the stock value KPI: trade type is not in the data yet (D-03).
const NoteBlockTrades = "معاملات بلوکی کسر نشده"

type inst struct {
	snap       model.Snapshot // zero until the first accepted snapshot
	game       *model.GameTotals
	issues     int
	class      string
	dirty      bool
	everTraded bool // a snapshot of today showed trading (or unknown volume)
	// Series: the value baseline (the highest value booked so far), when it was seen, and when
	// the value first fell below it (a dip: zero when none).
	base   int64
	baseAt time.Time
	dipAt  time.Time
	hasVal bool
}

type series struct {
	bars         map[time.Time]int64 // observed windows (0 = observed, nothing traded)
	partial      map[time.Time]bool
	partialUntil time.Time // every window up to and including this one is partial
}

// State is the market state of one trading day.
type State struct {
	cfg     Config
	day     string
	ins     map[string]*inst
	carried map[string]bool // instruments with only carry-over snapshots so far
	series  map[string]*series
	radar   []Signal
	issues  int // issues without an instrument
	// synthetic reports an instrument whose data is not real (rule 5); showSyn keeps it
	// (labelled) instead of hiding it. Set by the owner (SetSynthetic).
	synthetic func(ins string) bool
	showSyn   bool
}

// SetSynthetic tells the state which instruments carry non-real data and whether to show them
// (labelled) or leave them out of the radar and the issue counts. (Rows of synthetic snapshots
// are the owner's to filter before ApplySnapshot.) It covers data that arrived before the
// instrument's first snapshot identified it, since the four streams are read independently.
func (s *State) SetSynthetic(pred func(ins string) bool, show bool) {
	s.synthetic, s.showSyn = pred, show
}

func (s *State) isSyn(ins string) bool { return s.synthetic != nil && s.synthetic(ins) }

// hidden reports data of a synthetic instrument that must not be shown.
func (s *State) hidden(ins string) bool { return !s.showSyn && s.isSyn(ins) }

// New returns an empty state; nothing is accepted until Advance names the trading day.
func New(cfg Config) *State {
	if cfg.Sessions == nil {
		cfg.Sessions = calendar.Default()
	}
	d := DefaultConfig()
	if cfg.RadarKeep <= 0 {
		cfg.RadarKeep = d.RadarKeep
	}
	if cfg.SeriesBars <= 0 {
		cfg.SeriesBars = d.SeriesBars
	}
	if cfg.GapAfter <= 0 {
		cfg.GapAfter = d.GapAfter
	}
	if cfg.LagAfter <= 0 {
		cfg.LagAfter = d.LagAfter
	}
	s := &State{cfg: cfg}
	s.reset()
	return s
}

func (s *State) reset() {
	s.ins = map[string]*inst{}
	s.carried = map[string]bool{}
	s.series = map[string]*series{}
	s.radar = nil
	s.issues = 0
}

// Day is the trading day the state describes ("" before Advance).
func (s *State) Day() string { return s.day }

// Advance moves the state to Tehran trading day (YYYY-MM-DD), discarding the previous day; it
// reports whether the day changed. Earlier days are ignored. The owner calls it from the wall
// clock: data never moves the day, so a late or early message of another day cannot wipe or
// replace the dashboard.
func (s *State) Advance(day string) bool {
	if day <= s.day {
		return false
	}
	s.day = day
	s.reset()
	return true
}

func (s *State) today(t time.Time) bool { return s.day != "" && tehran.TradingDay(t) == s.day }

func (s *State) get(ins string) *inst {
	in := s.ins[ins]
	if in == nil {
		in = &inst{class: s.cfg.Sessions.Class(ins)}
		s.ins[ins] = in
	}
	return in
}

// showsTrading reports whether a snapshot shows trading today; an unknown volume counts as
// trading (its flow cannot be assumed zero).
func showsTrading(sn *model.Snapshot) bool {
	return !sn.Has(model.FVolume) || sn.Volume > 0 || (sn.Has(model.FValue) && sn.Value > 0)
}

// carryover reports a snapshot taken before the instrument's own open that already shows
// trading: the vendor still shows the previous day's totals (nothing trades before the open).
func (s *State) carryover(sn *model.Snapshot) bool {
	sess, ok := s.cfg.Sessions.Session(sn.InsCode, sn.SourceTime)
	return ok && sn.SourceTime.Before(sess.Open) && showsTrading(sn)
}

// ApplySnapshot records an instrument's snapshot of today; older snapshots than the latest, and
// pre-open carry-overs of the previous day's totals, are ignored.
func (s *State) ApplySnapshot(sn model.Snapshot) {
	if !s.today(sn.SourceTime) {
		return
	}
	if s.carryover(&sn) {
		if in := s.ins[sn.InsCode]; in == nil || in.snap.InsCode == "" {
			s.carried[sn.InsCode] = true
		}
		return
	}
	in := s.get(sn.InsCode)
	if in.snap.InsCode != "" && !sn.SourceTime.After(in.snap.SourceTime) {
		return
	}
	delete(s.carried, sn.InsCode)
	prev := in.snap
	in.snap = sn
	in.dirty = true
	in.everTraded = in.everTraded || showsTrading(&sn)
	if id := seriesKPI(in.class); id != "" && sn.Has(model.FValue) {
		s.book(id, in, &prev, &sn)
	}
}

// seriesKPI maps a class to the KPI whose sparkline it feeds.
func seriesKPI(class string) string {
	switch class {
	case Stock:
		return "value_stock"
	case FixedIncome:
		return "value_fixed"
	case Gold, Silver:
		return "value_metals"
	}
	return ""
}

// book adds the instrument's value increment to its KPI's 10-minute bar (the window of the
// snapshot that shows it). The baseline is the highest value booked so far:
//   - the first value of the day is the baseline; above zero, the trading before it cannot be
//     placed, so every window up to it is partial;
//   - a value below the baseline is a dip: nothing is booked and its window is partial; the
//     baseline stays, so a recovery from a glitch is not booked as new trading, and the window
//     of the recovery is partial too (the dip's split is ambiguous);
//   - a reset (value AND volume fell: the source restarted its day totals, e.g. yesterday's
//     totals still shown after the open), or a dip that outlasts its window, is a new level:
//     re-baseline there, the windows from the dip to it partial;
//   - an increment over more than GapAfter of the instrument's trading time (clipped to its
//     session) that crosses a window boundary spreads trading over windows in an unknown way:
//     every window it touches is partial.
func (s *State) book(id string, in *inst, prev, sn *model.Snapshot) {
	se := s.series[id]
	if se == nil {
		se = &series{bars: map[time.Time]int64{}, partial: map[time.Time]bool{}}
		s.series[id] = se
	}
	w := tehran.Floor10m(sn.SourceTime)
	se.bars[w] += 0 // observed
	markFrom := func(from time.Time) {
		for x := tehran.Floor10m(from); !x.After(w); x = x.Add(Window) {
			se.partial[x] = true
		}
	}
	if !in.hasVal {
		if sn.Value > 0 && w.After(se.partialUntil) {
			se.partialUntil = w
		}
		in.base, in.baseAt, in.hasVal = sn.Value, sn.SourceTime, true
		return
	}
	if sn.Value < in.base {
		reset := prev.Has(model.FVolume) && sn.Has(model.FVolume) && sn.Volume < prev.Volume
		outlasted := !in.dipAt.IsZero() && w.After(tehran.Floor10m(in.dipAt))
		if reset || outlasted {
			from := in.dipAt
			if from.IsZero() {
				from = sn.SourceTime
			}
			markFrom(from)
			in.base, in.baseAt, in.dipAt = sn.Value, sn.SourceTime, time.Time{}
			return
		}
		se.partial[w] = true
		if in.dipAt.IsZero() {
			in.dipAt = sn.SourceTime
		}
		return
	}
	if !in.dipAt.IsZero() {
		se.partial[w] = true // recovered from a dip
		in.dipAt = time.Time{}
	}
	if sn.Value > in.base {
		// Only the instrument's own trading time counts: nothing trades before its open or
		// after its close.
		from, to := in.baseAt, sn.SourceTime
		if sess, ok := s.cfg.Sessions.Session(sn.InsCode, sn.SourceTime); ok {
			if from.Before(sess.Open) {
				from = sess.Open
			}
			if to.After(sess.Close) {
				to = sess.Close
			}
		}
		if to.Sub(from) > s.cfg.GapAfter && tehran.Floor10m(to).After(tehran.Floor10m(from)) {
			markFrom(from)
		}
	}
	se.bars[w] += sn.Value - in.base
	in.base, in.baseAt = sn.Value, sn.SourceTime
}

// ApplyGame records an instrument's day totals of today (latest wins).
func (s *State) ApplyGame(g model.GameTotals) {
	if s.day == "" || g.Day != s.day {
		return
	}
	in := s.get(g.InsCode) // totals may precede the snapshot (streams are read independently)
	if in.game != nil && g.AsOf.Before(in.game.AsOf) {
		return
	}
	gc := g
	in.game = &gc
	in.dirty = true
}

// ApplySignal records a radar signal of today and returns it enriched with symbol and class.
func (s *State) ApplySignal(e anomaly.Event) (Signal, bool) {
	if !s.today(e.At) {
		return Signal{}, false
	}
	sig := Signal{Ins: e.InsCode, Kind: e.Kind, Z: e.Z, Reason: e.Reason, At: e.At, Class: s.cfg.Sessions.Class(e.InsCode),
		Syn: s.isSyn(e.InsCode)}
	if in := s.ins[e.InsCode]; in != nil {
		sig.Sym = in.snap.Symbol
	}
	s.radar = append(s.radar, sig)
	if len(s.radar) > s.cfg.RadarKeep {
		s.radar = s.radar[len(s.radar)-s.cfg.RadarKeep:]
	}
	return sig, true
}

// ApplyIssue counts a quality issue of today (SOURCE_TIME_ESTIMATED is shown as a latency label
// instead, once per instrument and day, so it is not counted).
func (s *State) ApplyIssue(q model.QualityIssue) {
	if q.Code == quality.TimeEstimated || !s.today(q.At) {
		return
	}
	if q.InsCode == "" {
		s.issues++
		return
	}
	in := s.get(q.InsCode) // the issue may precede the snapshot
	in.issues++
	in.dirty = true
}

// MarkDirty re-queues rows for the next delta (after a failed publish).
func (s *State) MarkDirty(ins []string) {
	for _, k := range ins {
		if in := s.ins[k]; in != nil {
			in.dirty = true
		}
	}
}

// Radar returns the recent signals to show, newest first (synthetic ones labelled or left out,
// decided now: an instrument may have been identified as synthetic after its signal arrived).
func (s *State) Radar() []Signal {
	out := make([]Signal, 0, len(s.radar))
	for i := len(s.radar) - 1; i >= 0; i-- {
		r := s.radar[i]
		if s.hidden(r.Ins) {
			continue
		}
		r.Syn = s.isSyn(r.Ins)
		out = append(out, r)
	}
	return out
}

// issueCount is the number of today's issues to show.
func (s *State) issueCount() int {
	n := s.issues
	for k, in := range s.ins {
		if !s.hidden(k) {
			n += in.issues
		}
	}
	return n
}

func i64(v int64) *int64 { return &v }

// ChangePct returns (last − yesterday) / yesterday × 100, or nil when either price is missing.
func ChangePct(sn *model.Snapshot) *float64 {
	if !sn.Has(model.FPriceLast) || sn.PriceLast <= 0 || sn.PriceYesterday <= 0 {
		return nil
	}
	v := float64(sn.PriceLast-sn.PriceYesterday) / float64(sn.PriceYesterday) * 100
	return &v
}

// lagging: the totals are older than the snapshot by more than LagAfter and do not include all
// of its volume (the engine is behind). A snapshot the engine cannot use (a flow field missing:
// never a baseline) does not count.
func (s *State) lagging(in *inst) bool {
	g, sn := in.game, &in.snap
	if g == nil {
		return false
	}
	for _, f := range model.FlowFields {
		if !sn.Has(f) {
			return false
		}
	}
	return sn.Volume > g.Volume && sn.SourceTime.Sub(g.AsOf) > s.cfg.LagAfter
}

func (s *State) row(in *inst) Row {
	sn := &in.snap
	r := Row{Ins: sn.InsCode, Sym: sn.Symbol, Class: in.class, Src: sn.SourceTime, Ing: sn.IngestTime,
		Est: sn.SourceTimeEstimated, Syn: model.IsSynthetic(sn) || s.isSyn(sn.InsCode), Issues: in.issues,
		Missing: sn.Missing, Traded: in.everTraded}
	if in.everTraded {
		r.Chg = ChangePct(sn) // no trade today: the vendor's prices are yesterday's, no change of today
	}
	if sn.Has(model.FPriceLast) && sn.PriceLast > 0 {
		r.Last = i64(sn.PriceLast)
	}
	if sn.Has(model.FValue) {
		r.Value = i64(sn.Value)
	}
	switch {
	case in.game != nil:
		at := in.game.AsOf
		r.NetHot, r.HotAsOf, r.Partial, r.Lagging = i64(in.game.NetHot), &at, in.game.Partial, s.lagging(in)
	case !in.everTraded:
		r.NetHot = i64(0) // measured: nothing traded today
	}
	return r
}

// Rows returns every instrument with a snapshot today, sorted by ins_code.
func (s *State) Rows() []Row {
	out := make([]Row, 0, len(s.ins))
	for _, in := range s.sorted() {
		out = append(out, s.row(in))
	}
	return out
}

// Dirty returns the rows changed since the previous call and clears the marks.
func (s *State) Dirty() []Row {
	var out []Row
	for _, in := range s.sorted() {
		if in.dirty {
			out = append(out, s.row(in))
			in.dirty = false
		}
	}
	return out
}

// sorted returns the instruments that have a snapshot, by ins_code.
func (s *State) sorted() []*inst {
	keys := make([]string, 0, len(s.ins))
	for k, in := range s.ins {
		if in.snap.InsCode != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]*inst, len(keys))
	for i, k := range keys {
		out[i] = s.ins[k]
	}
	return out
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// bucket places a traded stock in the breadth buckets. The ±3 % limit prices are rounded to the
// rial like the exchange's (ceiling ⌊1.03·y⌋, floor ⌈0.97·y⌉), so an instrument sitting at its
// limit price counts as at the limit; exact integer arithmetic, no float edges.
func (b *Breadth) bucket(last, y int64) {
	ceilP := 103 * y / 100
	floorP := (97*y + 99) / 100
	switch {
	case last == y:
		b.Flat++
	case last > y && last >= ceilP:
		b.Ceil++
	case last > y:
		b.Up++
	case last <= floorP:
		b.Floor++
	default:
		b.Down++
	}
}

// Summary computes every aggregate above the symbols table.
func (s *State) Summary() Summary {
	sum := Summary{Day: s.day, Issues: s.issueCount(), Queues: QueuesUnavailable, Carryover: len(s.carried)}
	flows := map[string]*ClassFlow{}
	type acc struct {
		value, n, missing, withV int64
		asOf                     time.Time
		est                      bool
	}
	values := map[string]*acc{}
	for _, c := range Classes {
		flows[c] = &ClassFlow{Class: c}
		values[c] = &acc{}
	}
	br := Breadth{Class: Stock}
	for _, in := range s.sorted() {
		sn := &in.snap
		sum.Instruments++
		sum.AsOf = later(sum.AsOf, sn.SourceTime)
		sum.Syn = sum.Syn || model.IsSynthetic(sn) || s.isSyn(sn.InsCode)
		sum.Est = sum.Est || sn.SourceTimeEstimated
		if in.class == calendar.Unknown {
			sum.Unknown++
			continue
		}
		if a := values[in.class]; a != nil {
			a.n++
			if sn.Has(model.FValue) {
				a.value += sn.Value
				a.withV++
				a.asOf = later(a.asOf, sn.SourceTime)
				a.est = a.est || sn.SourceTimeEstimated
			} else {
				a.missing++
			}
		}
		if f := flows[in.class]; f != nil {
			f.Instruments++
			contributes := true
			switch {
			case in.game != nil:
				g := in.game
				add(&f.NetHot, g.NetHot)
				add(&f.NetHotPlus, g.NetHotPlus)
				add(&f.NetRetail, g.NetRetail)
				add(&f.NetUnattributed, g.NetUnattributed)
				f.Partial = f.Partial || g.Partial
				f.AsOf = later(f.AsOf, g.AsOf)
				if s.lagging(in) {
					f.Lagging++
				}
			case !in.everTraded:
				for _, p := range []**int64{&f.NetHot, &f.NetHotPlus, &f.NetRetail, &f.NetUnattributed} {
					add(p, 0)
				}
				f.AsOf = later(f.AsOf, sn.SourceTime)
			default:
				f.Missing++
				contributes = false
			}
			if contributes {
				f.Est = f.Est || sn.SourceTimeEstimated
				if sn.Has(model.FValue) {
					add(&f.Value, sn.Value)
				} else {
					f.ValueMissing++
				}
			}
		}
		// Breadth: stock class only (other classes have different daily price limits); only
		// instruments that traded today.
		if in.class == Stock {
			br.Instruments++
			switch {
			case !sn.Has(model.FPriceLast) || sn.PriceLast <= 0 || sn.PriceYesterday <= 0:
				br.Missing++
			case !in.everTraded:
				br.Untraded++
			default:
				br.bucket(sn.PriceLast, sn.PriceYesterday)
				br.AsOf = later(br.AsOf, sn.SourceTime)
				br.Est = br.Est || sn.SourceTimeEstimated
			}
		}
	}
	if s.showSyn {
		for _, r := range s.radar {
			sum.Syn = sum.Syn || s.isSyn(r.Ins)
		}
	}
	if sum.Instruments > 0 {
		sum.UnknownShare = float64(sum.Unknown) / float64(sum.Instruments)
		sum.UnknownNotice = 2*sum.Unknown > sum.Instruments
	}
	for _, c := range Classes {
		f := flows[c]
		if !f.Partial && f.Missing == 0 && f.Lagging == 0 && f.ValueMissing == 0 && f.NetHot != nil {
			f.Pattern = pattern(*f.NetHot, *f.NetRetail, f.Value, s.cfg.Materiality)
		}
		sum.Flows = append(sum.Flows, *f)
	}
	sum.Breadth = br

	kpi := func(id string, classes ...string) KPI {
		k := KPI{ID: id, Available: true, Classes: classes, Series: s.bars(id, classes)}
		for _, c := range classes {
			a := values[c]
			k.Instruments += int(a.n)
			k.Missing += int(a.missing)
			if a.withV > 0 {
				add(&k.Value, a.value)
			}
			k.AsOf = later(k.AsOf, a.asOf)
			k.Est = k.Est || a.est
		}
		return k
	}
	stock := kpi("value_stock", Stock)
	stock.Note = NoteBlockTrades
	etf := values[EquityETF]
	stock.Secondary = &Secondary{Class: EquityETF, Instruments: int(etf.n), Missing: int(etf.missing)}
	if etf.withV > 0 {
		stock.Secondary.Value = i64(etf.value)
	}
	sum.KPIs = []KPI{
		{ID: "index_total", Reason: IndicesUnavailable.Reason, Series: []Bar{}},
		stock,
		kpi("value_fixed", FixedIncome),
		kpi("value_metals", Gold, Silver),
	}
	return sum
}

func add(p **int64, v int64) {
	if *p == nil {
		*p = i64(0)
	}
	**p += v
}

// bars returns the KPI's last SeriesBars 10-minute bars, oldest first: one per window from the
// first observed one (or, after a late start, from its classes' earliest open) to the latest;
// windows without any observation have a null value.
func (s *State) bars(id string, classes []string) []Bar {
	se := s.series[id]
	out := []Bar{}
	if se == nil || len(se.bars) == 0 {
		return out
	}
	var first, last time.Time
	for w := range se.bars {
		if first.IsZero() || w.Before(first) {
			first = w
		}
		if w.After(last) {
			last = w
		}
	}
	if !se.partialUntil.IsZero() {
		for _, c := range classes {
			if sess, ok := s.cfg.Sessions.ClassSession(c, first); ok {
				if o := tehran.Floor10m(sess.Open); o.Before(first) {
					first = o
				}
			}
		}
	}
	for w := first; !w.After(last); w = w.Add(Window) {
		b := Bar{At: w, Partial: se.partial[w] || !w.After(se.partialUntil)}
		if v, ok := se.bars[w]; ok {
			b.Value = i64(v)
		}
		out = append(out, b)
	}
	if len(out) > s.cfg.SeriesBars {
		out = out[len(out)-s.cfg.SeriesBars:]
	}
	return out
}

// Pattern texts (docs/market-metrics.md).
var patternText = map[string]string{
	"hot_out_retail_in": "پول درشت خارج و پول خرد وارد شده است.",
	"hot_in_retail_out": "پول درشت وارد و پول خرد خارج شده است.",
	"both_in":           "پول درشت و پول خرد هر دو وارد شده‌اند.",
	"both_out":          "پول درشت و پول خرد هر دو خارج شده‌اند.",
	"none":              "الگوی مشخصی دیده نمی‌شود.",
}

// pattern classifies the signs of the large (hot) and retail net flows. A band counts as in (+1)
// or out (−1) only when |net| ≥ materiality × the class's traded value; otherwise it is neutral
// and the rule is "none". No traded value (nil or 0): "none".
func pattern(hot, retail int64, value *int64, materiality float64) *Pattern {
	rule := "none"
	if value != nil && *value > 0 {
		m := materiality * float64(*value)
		sign := func(x int64) int {
			switch {
			case float64(x) >= m:
				return 1
			case float64(-x) >= m:
				return -1
			}
			return 0
		}
		switch h, r := sign(hot), sign(retail); {
		case h < 0 && r > 0:
			rule = "hot_out_retail_in"
		case h > 0 && r < 0:
			rule = "hot_in_retail_out"
		case h > 0 && r > 0:
			rule = "both_in"
		case h < 0 && r < 0:
			rule = "both_out"
		}
	}
	return &Pattern{Rule: rule, Text: patternText[rule]}
}

// SessionInfo is one class's session today, for the session strip.
type SessionInfo struct {
	Class    string    `json:"class"`
	Open     bool      `json:"open"`     // the class trades today
	PreOpen  time.Time `json:"pre_open"` // zero when not Open
	Start    time.Time `json:"start"`
	Close    time.Time `json:"close"`
	Verified bool      `json:"verified"`
}

// Sessions returns every class's session on t's Tehran trading day, in Classes order.
func Sessions(cal *calendar.Calendar, t time.Time) []SessionInfo {
	out := make([]SessionInfo, 0, len(Classes))
	for _, c := range Classes {
		si := SessionInfo{Class: c}
		if ss, ok := cal.ClassSession(c, t); ok {
			si.Open, si.PreOpen, si.Start, si.Close, si.Verified = true, ss.PreOpen, ss.Open, ss.Close, ss.Verified
		}
		out = append(out, si)
	}
	return out
}
