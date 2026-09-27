package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"bourse/internal/bus"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// Tables written by the writer (infra/clickhouse/001_schema.sql).
const (
	tSnapshots = "snapshots"
	tFlow      = "flow_events"
	tGame      = "game_totals"
	t10m       = "flow_10m"
	tQuality   = "quality_issues"
)

// sink inserts rows (JSON objects, one per row) into a table.
type sink interface {
	insert(ctx context.Context, table string, rows [][]byte) error
}

// clickhouse inserts over the HTTP interface (JSONEachRow). The password is sent in a header only
// and never appears in errors or logs.
type clickhouse struct {
	base, db, user, pass string
	hc                   *http.Client
}

func newClickHouse(base, db, user, pass string) *clickhouse {
	return &clickhouse{base: strings.TrimRight(base, "/"), db: db, user: user, pass: pass, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (c *clickhouse) do(ctx context.Context, query string, body []byte) ([]byte, error) {
	u := c.base + "/?" + url.Values{"query": {query}, "date_time_input_format": {"basic"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-ClickHouse-User", c.user)
	req.Header.Set("X-ClickHouse-Key", c.pass)
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL carries no secret, but keep errors short
		}
		return nil, fmt.Errorf("clickhouse: %w", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return nil, fmt.Errorf("clickhouse: HTTP %d: %s", resp.StatusCode, msg)
	}
	return out, nil
}

func (c *clickhouse) insert(ctx context.Context, table string, rows [][]byte) error {
	_, err := c.do(ctx, fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", c.db, table), bytes.Join(rows, []byte("\n")))
	return err
}

// schema is what the writer's idempotency depends on: each table's sorting key (the dedup key) and
// its ReplacingMergeTree version column. Anything else is refused at start, so dedup can never
// fail silently against an older table.
var schema = map[string]string{
	tSnapshots: "ins_code, source_time, source",
	tFlow:      "ins_code, interval_to, side, interval_from",
	t10m:       "ins_code, window_start",
	tGame:      "ins_code, day",
	tQuality:   "code, ins_code, day, dedup_at, dedup_detail",
}

// checkSchema verifies every table's engine, version column and sorting key.
func (c *clickhouse) checkSchema(ctx context.Context) error {
	out, err := c.do(ctx, fmt.Sprintf("SELECT name, engine_full, sorting_key FROM system.tables WHERE database = '%s' FORMAT TSV", c.db), nil)
	if err != nil {
		return err
	}
	type info struct{ engine, key string }
	tables := map[string]info{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Split(l, "\t"); len(f) == 3 {
			tables[f[0]] = info{f[1], f[2]}
		}
	}
	var bad []string
	for t, key := range schema {
		got, ok := tables[t]
		switch {
		case !ok:
			bad = append(bad, t+" missing")
		case !strings.HasPrefix(got.engine, "ReplacingMergeTree(ver)"):
			bad = append(bad, fmt.Sprintf("%s: engine %q, want ReplacingMergeTree(ver)", t, got.engine))
		case got.key != key:
			bad = append(bad, fmt.Sprintf("%s: sorting key %q, want %q", t, got.key, key))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("clickhouse schema %s is not the W-01 schema (%s); apply infra/clickhouse (make ddl-reset on an empty database)",
			c.db, strings.Join(bad, "; "))
	}
	return nil
}

// ver is a row's ReplacingMergeTree version: when JetStream stored the message (unix ns). Unlike
// the stream sequence it keeps increasing when a stream is recreated, and a redelivery keeps it.
func ver(m bus.Msg) int64 { return m.Stored.UnixNano() }

// ClickHouse formats (UTC, basic input format).
func chTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000") }

// oncePerDay are the quality codes emitted once per instrument and day: stored once per
// (code, ins_code, day) however often they are re-emitted (engine recovery).
var oncePerDay = map[string]bool{quality.DayStartMissed: true, quality.PrevDayCarryover: true}

// mdGate orders the writer's consumers for rule 5: engine outputs carry no source, so a flow or
// quality batch is inserted only once writer-md has CHECKED (for synthetic data) every snapshot
// that could have produced it. An output is always stored after its input snapshot, so a batch
// received at local time h, whose newest message was stored at server time t, may go when
//   - writer-md has checked a snapshot stored at or after t (server clock vs server clock), or
//   - writer-md found MD empty on a fetch started after h (local clock vs local clock).
//
// The two clocks are never compared with each other.
type mdGate struct {
	checked atomic.Int64 // stored time (server, ns) of the newest snapshot writer-md has checked
	drained atomic.Int64 // local start time (ns) of writer-md's latest empty fetch
}

func (g *mdGate) open(h, t time.Time) bool {
	return g.checked.Load() >= t.UnixNano() || g.drained.Load() > h.UnixNano()
}

// wait blocks until the gate opens for a batch received at h whose newest message was stored at
// t, keeping the batch's messages in progress; it returns ctx's error if the writer stops first
// (writer-md found synthetic data: nothing of this batch is inserted).
func (g *mdGate) wait(ctx context.Context, h, t time.Time, msgs []bus.Msg) error {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for last := time.Now(); !g.open(h, t); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-tick.C:
			if now.Sub(last) > 5*time.Second {
				for _, m := range msgs {
					m.InProgress()
				}
				last = now
			}
		}
	}
	return nil
}

// writer turns bus messages into rows. It is stateless: idempotency is the tables' job.
type writer struct {
	// Exactly one of mark (writer-md: advanced after each checked batch) and gate (writer-flow,
	// writer-quality: waited on before inserting) is set in production; both nil in unit tests.
	mark, gate *mdGate
	sink       sink
	retry      []time.Duration // insert retries before giving up (the batch then stays unacked)
	// afterInsert (tests) runs after the rows are inserted and before the batch is acked.
	afterInsert func() error
	stats       struct{ rows, undecodable int }
}

// rows groups a batch's rows by table.
type rows map[string][][]byte

func (r rows) add(table string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs of strings and numbers
	}
	r[table] = append(r[table], b)
}

// handle converts and inserts one batch; an error leaves the batch unacked (redelivered).
func (w *writer) handle(ctx context.Context, msgs []bus.Msg) error {
	received := time.Now()
	out := rows{}
	for _, m := range msgs {
		err := w.convert(out, m)
		if errors.Is(err, errSynthetic) {
			// Nothing of this batch is inserted or acked: the writer stops (rule 5).
			return fmt.Errorf("%s seq %d: %w", m.Subject, m.StreamSeq, err)
		}
		if err != nil {
			w.stats.undecodable++
			ins := m.Subject[strings.LastIndexByte(m.Subject, '.')+1:]
			log.Printf("writer: %s seq %d: undecodable, stored as %s: %v", m.Subject, m.StreamSeq, quality.Undecodable, err)
			// At = the stored time, so a redelivered batch yields the same row (idempotent).
			out.add(tQuality, qualityRow(model.QualityIssue{InsCode: ins, Code: quality.Undecodable,
				Detail: fmt.Sprintf("writer: %s seq %d: %v", m.Subject, m.StreamSeq, err), At: m.Stored}, m))
		}
	}
	newest := msgs[0].Stored
	for _, m := range msgs {
		if m.Stored.After(newest) {
			newest = m.Stored
		}
	}
	if w.gate != nil {
		if err := w.gate.wait(ctx, received, newest, msgs); err != nil {
			return err
		}
	}
	for _, t := range []string{tSnapshots, tFlow, tGame, t10m, tQuality} {
		if len(out[t]) == 0 {
			continue
		}
		err := bus.Retry(ctx, w.retry, func() {
			for _, m := range msgs {
				m.InProgress()
			}
		}, func() error { return w.sink.insert(ctx, t, out[t]) })
		if err != nil {
			return fmt.Errorf("insert %d rows into %s: %w", len(out[t]), t, err)
		}
		w.stats.rows += len(out[t])
	}
	if w.mark != nil { // every snapshot of this batch was checked (none synthetic)
		w.mark.checked.Store(newest.UnixNano())
	}
	if w.afterInsert != nil {
		return w.afterInsert()
	}
	return nil
}

var errMismatch = errors.New("ins_code does not match the subject")

// errSynthetic stops the writer: synthetic data on the bus means a demo stack (collector and engine
// ran with ALLOW_SYNTHETIC_ON_BUS=1). Engine outputs carry no source, so outputs computed from a
// re-timed recording (rebase:, real instrument codes) cannot be told apart from real ones: nothing
// of such a stack may be archived (rule 5). Purge the streams (or wait out their 48 h) to resume.
var errSynthetic = errors.New("synthetic data on the bus: this stack must not be archived (rule 5)")

// convert adds m's row to out. It returns errSynthetic for synthetic data (never stored, rule 5)
// and another error for a message that cannot be stored as is.
func (w *writer) convert(out rows, m bus.Msg) error {
	kind, ins, ok := splitSubject(m.Subject)
	if !ok {
		return fmt.Errorf("unexpected subject")
	}
	if strings.HasPrefix(ins, "SYN") {
		return errSynthetic
	}
	switch kind {
	case "md.snap":
		var s model.Snapshot
		if err := decode(m.Data, &s); err != nil {
			return err
		}
		if s.InsCode != ins {
			return errMismatch
		}
		if model.IsSynthetic(&s) {
			return errSynthetic
		}
		if s.SourceTime.IsZero() {
			return errors.New("no source_time")
		}
		out.add(tSnapshots, snapshotRow(s, m))
	case "flow.event":
		var e model.FlowEvent
		if err := decode(m.Data, &e); err != nil {
			return err
		}
		if e.InsCode != ins {
			return errMismatch
		}
		if strings.HasPrefix(e.Symbol, "SYN") {
			return errSynthetic
		}
		if !oneOf(string(e.Side), "buy", "sell") || !oneOf(string(e.Band), "hot", "hot_plus", "retail") ||
			!oneOf(string(e.Attribution), "attributed", "unattributed") {
			return fmt.Errorf("side/band/attribution %q/%q/%q", e.Side, e.Band, e.Attribution)
		}
		out.add(tFlow, flowRow(e, m))
	case "flow.game":
		var g model.GameTotals
		if err := decode(m.Data, &g); err != nil {
			return err
		}
		if g.InsCode != ins {
			return errMismatch
		}
		if _, err := time.Parse("2006-01-02", g.Day); err != nil {
			return fmt.Errorf("day %q", g.Day)
		}
		out.add(tGame, gameRow(g, m))
	case "flow.10m":
		var t model.TenMinute
		if err := decode(m.Data, &t); err != nil {
			return err
		}
		if t.InsCode != ins {
			return errMismatch
		}
		out.add(t10m, tenRow(t, m))
	case "quality":
		var q model.QualityIssue
		if err := decode(m.Data, &q); err != nil {
			return err
		}
		if q.InsCode != ins {
			return errMismatch
		}
		if q.At.IsZero() {
			return errors.New("no at")
		}
		out.add(tQuality, qualityRow(q, m))
	default:
		return fmt.Errorf("unexpected subject")
	}
	return nil
}

func decode(data []byte, v any) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null payload")
	}
	return json.Unmarshal(data, v)
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// splitSubject splits "flow.game.123" into ("flow.game", "123").
func splitSubject(subj string) (kind, ins string, ok bool) {
	i := strings.LastIndexByte(subj, '.')
	if i <= 0 || i == len(subj)-1 {
		return "", "", false
	}
	return subj[:i], subj[i+1:], true
}

type snapshotRec struct {
	InsCode             string   `json:"ins_code"`
	Symbol              string   `json:"symbol"`
	Source              string   `json:"source"`
	SourceTime          string   `json:"source_time"`
	IngestTime          string   `json:"ingest_time"`
	SourceTimeEstimated bool     `json:"source_time_estimated"`
	PriceLast           int64    `json:"price_last"`
	PriceClose          int64    `json:"price_close"`
	PriceFirst          int64    `json:"price_first"`
	PriceYesterday      int64    `json:"price_yesterday"`
	PriceMin            int64    `json:"price_min"`
	PriceMax            int64    `json:"price_max"`
	TradeCount          int64    `json:"trade_count"`
	Volume              int64    `json:"volume"`
	Value               int64    `json:"value"`
	IndBuyVol           int64    `json:"ind_buy_vol"`
	IndSellVol          int64    `json:"ind_sell_vol"`
	InstBuyVol          int64    `json:"inst_buy_vol"`
	InstSellVol         int64    `json:"inst_sell_vol"`
	IndBuyCount         int64    `json:"ind_buy_count"`
	IndSellCount        int64    `json:"ind_sell_count"`
	InstBuyCount        int64    `json:"inst_buy_count"`
	InstSellCount       int64    `json:"inst_sell_count"`
	Missing             []string `json:"missing"`
	BusSeq              uint64   `json:"bus_seq"`
	Ver                 int64    `json:"ver"`
}

func snapshotRow(s model.Snapshot, m bus.Msg) snapshotRec {
	missing := s.Missing
	if missing == nil {
		missing = []string{}
	}
	return snapshotRec{s.InsCode, s.Symbol, s.Source, chTime(s.SourceTime), chTime(s.IngestTime), s.SourceTimeEstimated,
		s.PriceLast, s.PriceClose, s.PriceFirst, s.PriceYesterday, s.PriceMin, s.PriceMax,
		s.TradeCount, s.Volume, s.Value, s.IndBuyVol, s.IndSellVol, s.InstBuyVol, s.InstSellVol,
		s.IndBuyCount, s.IndSellCount, s.InstBuyCount, s.InstSellCount, missing, m.StreamSeq, ver(m)}
}

type flowRec struct {
	InsCode      string `json:"ins_code"`
	Symbol       string `json:"symbol"`
	Class        string `json:"class"`
	Side         string `json:"side"`
	Band         string `json:"band"`
	Attribution  string `json:"attribution"`
	IntervalFrom string `json:"interval_from"`
	IntervalTo   string `json:"interval_to"`
	Volume       int64  `json:"volume"`
	Value        int64  `json:"value"`
	Participants int64  `json:"participants"`
	AvgTicket    int64  `json:"avg_ticket"`
	VWAP         int64  `json:"vwap"`
	PriceLast    int64  `json:"price_last"`
	BusSeq       uint64 `json:"bus_seq"`
	Ver          int64  `json:"ver"`
}

func flowRow(e model.FlowEvent, m bus.Msg) flowRec {
	return flowRec{e.InsCode, e.Symbol, e.Class, string(e.Side), string(e.Band), string(e.Attribution),
		chTime(e.IntervalFrom), chTime(e.IntervalTo), e.Volume, e.Value, e.Participants, e.AvgTicket, e.VWAP, e.PriceLast, m.StreamSeq, ver(m)}
}

type gameRec struct {
	InsCode         string `json:"ins_code"`
	Class           string `json:"class"`
	Day             string `json:"day"`
	AsOf            string `json:"as_of"`
	Volume          int64  `json:"volume"`
	NetHot          int64  `json:"net_hot"`
	NetHotPlus      int64  `json:"net_hot_plus"`
	NetRetail       int64  `json:"net_retail"`
	NetUnattributed int64  `json:"net_unattributed"`
	Partial         bool   `json:"partial"`
	BusSeq          uint64 `json:"bus_seq"`
	Ver             int64  `json:"ver"`
}

func gameRow(g model.GameTotals, m bus.Msg) gameRec {
	return gameRec{g.InsCode, g.Class, g.Day, chTime(g.AsOf), g.Volume, g.NetHot, g.NetHotPlus, g.NetRetail, g.NetUnattributed, g.Partial, m.StreamSeq, ver(m)}
}

type tenRec struct {
	InsCode     string `json:"ins_code"`
	Class       string `json:"class"`
	WindowStart string `json:"window_start"`
	NetHot      int64  `json:"net_hot"`
	PriceOpen   int64  `json:"price_open"`
	PriceLast   int64  `json:"price_last"`
	Partial     bool   `json:"partial"`
	BusSeq      uint64 `json:"bus_seq"`
	Ver         int64  `json:"ver"`
}

func tenRow(t model.TenMinute, m bus.Msg) tenRec {
	return tenRec{t.InsCode, t.Class, t.WindowStart.UTC().Format("2006-01-02 15:04:05"), t.NetHot, t.PriceOpen, t.PriceLastV, t.Partial, m.StreamSeq, ver(m)}
}

type qualityRec struct {
	InsCode     string `json:"ins_code"`
	Code        string `json:"code"`
	Detail      string `json:"detail"`
	At          string `json:"at"`
	Day         string `json:"day"`
	DedupAt     string `json:"dedup_at"`
	DedupDetail string `json:"dedup_detail"`
	BusSeq      uint64 `json:"bus_seq"`
	Ver         int64  `json:"ver"`
}

func qualityRow(q model.QualityIssue, m bus.Msg) qualityRec {
	r := qualityRec{InsCode: q.InsCode, Code: q.Code, Detail: q.Detail, At: chTime(q.At), Day: tehran.TradingDay(q.At),
		DedupAt: chTime(q.At), DedupDetail: q.Detail, BusSeq: m.StreamSeq, Ver: ver(m)}
	if oncePerDay[q.Code] {
		r.DedupAt, r.DedupDetail, r.Ver = chTime(tehran.DayStart(q.At)), "", -r.Ver // keep the first emission
	}
	return r
}
