package market

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"bourse/internal/anomaly"
	"bourse/internal/calendar"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

const day = "2026-09-23" // Wednesday: every class trades (stock 09:00–12:30, gold 12:00–18:00)

func at(hm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", day+" "+hm, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func testState() *State {
	cfg := DefaultConfig()
	cfg.Sessions = calendar.Default().WithInstruments(map[string]string{
		"S1": Stock, "S2": Stock, "S3": Stock, "S4": Stock, "S5": Stock, "S6": Stock, "S7": Stock, "S8": Stock, "S9": Stock,
		"SA": Stock, "SB": Stock, "SC": Stock, "SD": Stock,
		"E1": EquityETF, "F1": FixedIncome, "G1": Gold, "V1": Silver,
	})
	st := New(cfg)
	st.Advance(day)
	return st
}

func snap(ins, hm string, last, yesterday, volume, value int64) model.Snapshot {
	t := at(hm)
	return model.Snapshot{InsCode: ins, Symbol: "sym" + ins, Source: "test", SourceTime: t, IngestTime: t.Add(time.Second),
		PriceLast: last, PriceYesterday: yesterday, Volume: volume, Value: value}
}

func game(ins, class, hm string, volume, hot, plus, retail, unattr int64, partial bool) model.GameTotals {
	return model.GameTotals{InsCode: ins, Class: class, Day: day, AsOf: at(hm), Volume: volume,
		NetHot: hot, NetHotPlus: plus, NetRetail: retail, NetUnattributed: unattr, Partial: partial}
}

func eq(t *testing.T, what string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = null, want %d", what, want)
	} else if *got != want {
		t.Errorf("%s = %d, want %d", what, *got, want)
	}
}

func null(t *testing.T, what string, got *int64) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %d, want null (missing, never 0)", what, *got)
	}
}

func TestRowMissingIsNullNeverZero(t *testing.T) {
	st := testState()
	a := snap("S1", "09:10:00", 1030, 1000, 500, 515_000)
	a.Missing = []string{model.FPriceLast, model.FValue}
	st.ApplySnapshot(a)
	st.ApplySnapshot(snap("S2", "09:10:00", 1000, 0, 0, 0))          // no yesterday price, nothing traded
	st.ApplySnapshot(snap("S3", "09:10:00", 1010, 1000, 10, 10_100)) // traded, no totals yet
	b := snap("S4", "09:10:00", 1010, 1000, 0, 0)
	b.Missing = []string{model.FVolume} // unknown volume: flow cannot be assumed zero
	st.ApplySnapshot(b)
	rows := st.Rows()
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	r1, r2, r3, r4 := rows[0], rows[1], rows[2], rows[3]
	null(t, "S1 last", r1.Last)
	null(t, "S1 value", r1.Value)
	if r1.Chg != nil {
		t.Errorf("S1 chg = %v, want null (last missing)", *r1.Chg)
	}
	if r2.Chg != nil {
		t.Errorf("S2 chg = %v, want null (no yesterday price)", *r2.Chg)
	}
	eq(t, "S2 net_hot (nothing traded: measured zero)", r2.NetHot, 0)
	null(t, "S3 net_hot (traded, no totals)", r3.NetHot)
	null(t, "S4 net_hot (volume unknown)", r4.NetHot)
	null(t, "S1 net_hot", r1.NetHot)
	if r3.Chg == nil || *r3.Chg != 1 {
		t.Errorf("S3 chg = %v, want 1", r3.Chg)
	}
	if r3.HotAsOf != nil {
		t.Errorf("hot_as_of without totals must be null")
	}
	js, _ := json.Marshal(r1)
	for _, f := range []string{`"last":null`, `"value":null`, `"net_hot":null`, `"hot_as_of":null`} {
		if !strings.Contains(string(js), f) {
			t.Errorf("missing field must serialise as %s: %s", f, js)
		}
	}
}

// A volume that glitches back to 0 after trading is not a measured zero.
func TestEverTradedNotResetByZeroVolume(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1010, 1000, 500, 5_000))
	st.ApplySnapshot(snap("S1", "09:10:05", 1010, 1000, 0, 0))
	null(t, "net_hot after a volume glitch", st.Rows()[0].NetHot)
	if f := st.Summary().Flows[0]; f.Missing != 1 || f.NetHot != nil {
		t.Errorf("flow = %+v, want the instrument missing", f)
	}
}

func TestRowGamePartialAndLagging(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1010, 1000, 10, 10_100))
	st.ApplyGame(game("S1", Stock, "09:10:00", 10, -42, 0, 0, 0, true))
	// An older totals message (other stream order) must not replace the newer one.
	st.ApplyGame(game("S1", Stock, "09:05:00", 5, 7, 0, 0, 0, false))
	r := st.Rows()[0]
	eq(t, "net_hot", r.NetHot, -42)
	if !r.Partial || r.Lagging || r.HotAsOf == nil || !r.HotAsOf.Equal(at("09:10:00")) {
		t.Errorf("row = %+v", r)
	}
	// Engine behind: snapshot 40 s newer with more volume than the totals include.
	st.ApplySnapshot(snap("S1", "09:10:40", 1010, 1000, 20, 20_100))
	if r = st.Rows()[0]; !r.Lagging {
		t.Error("totals 40 s behind with more volume must be lagging")
	}
	// No new volume since the totals: not lagging, however old.
	st2 := testState()
	st2.ApplySnapshot(snap("S1", "09:10:00", 1010, 1000, 10, 10_100))
	st2.ApplyGame(game("S1", Stock, "09:09:00", 10, 1, 0, 0, 0, false))
	st2.ApplySnapshot(snap("S1", "09:12:00", 1010, 1000, 10, 10_100))
	if st2.Rows()[0].Lagging {
		t.Error("no new volume: not lagging")
	}
	// More volume but within LagAfter: not lagging.
	st2.ApplySnapshot(snap("S1", "09:09:20", 1010, 1000, 12, 12_100)) // older: ignored
	st3 := testState()
	st3.ApplySnapshot(snap("S1", "09:10:00", 1010, 1000, 10, 10_100))
	st3.ApplyGame(game("S1", Stock, "09:09:40", 8, 1, 0, 0, 0, false))
	if st3.Rows()[0].Lagging {
		t.Error("20 s behind: within LagAfter")
	}
}

func TestOutOfOrderAndEqualTimeIgnored(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1010, 1000, 10, 10_100))
	st.ApplySnapshot(snap("S1", "09:09:00", 990, 1000, 5, 5_000))
	st.ApplySnapshot(snap("S1", "09:10:00", 995, 1000, 5, 5_000))
	eq(t, "last", st.Rows()[0].Last, 1010)
}

// The vendor's previous-day totals before the open are not today's (finding: pre-open carry-over).
func TestPreOpenCarryoverIgnored(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "08:50:00", 1010, 1000, 900_000, 9_000_000)) // yesterday's totals
	st.ApplySnapshot(snap("S2", "08:50:00", 1000, 1000, 0, 0))               // real pre-open baseline
	sum := st.Summary()
	if sum.Carryover != 1 || sum.Instruments != 1 || len(st.Rows()) != 1 || st.Rows()[0].Ins != "S2" {
		t.Fatalf("carryover %d instruments %d rows %+v", sum.Carryover, sum.Instruments, st.Rows())
	}
	eq(t, "stock value (carry-over excluded)", sum.KPIs[1].Value, 0)
	st.ApplySnapshot(snap("S1", "09:00:05", 1000, 1000, 0, 0)) // vendor reset at the open
	st.ApplySnapshot(snap("S1", "09:01:00", 1005, 1000, 10, 10_050))
	if sum = st.Summary(); sum.Carryover != 0 || sum.Instruments != 2 {
		t.Errorf("after the reset: carryover %d instruments %d", sum.Carryover, sum.Instruments)
	}
	for _, b := range sum.KPIs[1].Series {
		if b.Partial {
			t.Errorf("a zero baseline after the carry-over is complete: %+v", b)
		}
	}
}

func TestBreadthLimitPricesExact(t *testing.T) {
	st := testState()
	// y = 1000: ceiling 1030, floor 970. y = 12345: ceiling ⌊12715.35⌋ = 12715, floor ⌈11974.65⌉ = 11975.
	for ins, p := range map[string][2]int64{
		"S1": {970, 1000}, "S2": {971, 1000}, "S3": {1000, 1000}, "S4": {1029, 1000}, "S5": {1030, 1000},
		"S6": {12715, 12345}, "S7": {12714, 12345}, "S8": {11975, 12345}, "S9": {11976, 12345},
	} {
		st.ApplySnapshot(snap(ins, "09:10:00", p[0], p[1], 1, 1))
	}
	st.ApplySnapshot(snap("SA", "09:10:00", 0, 1000, 1, 1))    // no last price: missing
	st.ApplySnapshot(snap("SB", "09:10:00", 1030, 0, 1, 1))    // no yesterday price: missing, not ceil
	st.ApplySnapshot(snap("SC", "09:10:00", 1030, 1000, 0, 0)) // no trade today: untraded
	st.ApplySnapshot(snap("E1", "09:10:00", 1100, 1000, 1, 1)) // equity ETF: not in stock breadth
	st.ApplySnapshot(snap("X9", "09:10:00", 1100, 1000, 1, 1)) // unknown: not in any class aggregate
	b := st.Summary().Breadth
	want := Breadth{Class: Stock, Instruments: 12, Missing: 2, Untraded: 1,
		Floor: 2, Down: 2, Flat: 1, Up: 2, Ceil: 2, AsOf: at("09:10:00")}
	if b != want {
		t.Errorf("breadth\n got %+v\nwant %+v", b, want)
	}
}

func TestClassFlowSumsMissingPartialAndUnknown(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:20:00", 1, 1, 10, 100_000_000_000))
	st.ApplySnapshot(snap("S2", "09:20:00", 1, 1, 10, 50_000_000_000))
	st.ApplySnapshot(snap("S3", "09:20:00", 1, 1, 0, 0))     // not traded: zero flow, contributes
	st.ApplySnapshot(snap("S4", "09:21:00", 1, 1, 5, 1_000)) // traded, no totals: missing
	st.ApplySnapshot(snap("X9", "09:20:00", 1, 1, 10, 999))  // unknown class: excluded
	st.ApplyGame(game("S1", Stock, "09:20:00", 10, -3_000_000_000, 100, 2_000_000_000, 7, false))
	st.ApplyGame(game("S2", Stock, "09:20:00", 10, -1_000_000_000, 200, 500_000_000, -2, false))
	st.ApplyGame(game("X9", calendar.Unknown, "09:20:00", 10, 1e12, 1e12, 1e12, 1e12, false))
	st.ApplyGame(game("G1", Gold, "12:20:00", 1, 5, 5, 5, 5, true)) // totals without a snapshot: not aggregated

	f := st.Summary().Flows[0]
	if f.Class != Stock || f.Instruments != 4 || f.Missing != 1 {
		t.Fatalf("stock flow class/instruments/missing = %s/%d/%d, want stock/4/1", f.Class, f.Instruments, f.Missing)
	}
	eq(t, "net_hot", f.NetHot, -4_000_000_000)
	eq(t, "net_hot_plus", f.NetHotPlus, 300)
	eq(t, "net_retail", f.NetRetail, 2_500_000_000)
	eq(t, "net_unattributed", f.NetUnattributed, 5)
	eq(t, "value (contributors)", f.Value, 150_000_000_000)
	if f.Pattern != nil {
		t.Errorf("pattern must be null while an instrument is missing, got %+v", f.Pattern)
	}
	if !f.AsOf.Equal(at("09:20:00")) {
		t.Errorf("as_of = %s", f.AsOf)
	}
	// S4's totals arrive: complete. materiality 1 % of 150e9+1000 ≈ 1.5e9: hot −4e9 out, retail +2.5e9 in.
	st.ApplyGame(game("S4", Stock, "09:21:00", 5, 0, 0, 0, 0, false))
	f = st.Summary().Flows[0]
	if f.Missing != 0 || f.Pattern == nil || f.Pattern.Rule != "hot_out_retail_in" {
		t.Fatalf("pattern = %+v (missing %d), want hot_out_retail_in", f.Pattern, f.Missing)
	}
	eq(t, "value", f.Value, 150_000_001_000)
	// A partial contributor suppresses the pattern and marks the class partial.
	st.ApplyGame(game("S2", Stock, "09:20:00", 10, -1_000_000_000, 200, 500_000_000, -2, true))
	f = st.Summary().Flows[0]
	if !f.Partial || f.Pattern != nil {
		t.Errorf("partial = %v pattern = %+v, want partial and no pattern", f.Partial, f.Pattern)
	}
	// Classes with no instrument: every band null (never 0).
	for _, fl := range st.Summary().Flows {
		if fl.Class == FixedIncome {
			null(t, "fixed_income net_hot", fl.NetHot)
			null(t, "fixed_income value", fl.Value)
		}
	}
}

// A contributor without a traded value would understate the materiality base: no pattern.
func TestPatternNeedsEveryContributorValue(t *testing.T) {
	st := testState()
	s1 := snap("S1", "09:20:00", 1, 1, 10, 900_000)
	s1.Missing = []string{model.FValue}
	st.ApplySnapshot(s1)
	st.ApplySnapshot(snap("S2", "09:20:00", 1, 1, 10, 1_000))
	st.ApplyGame(game("S1", Stock, "09:20:00", 10, -9000, 0, 9000, 0, false))
	st.ApplyGame(game("S2", Stock, "09:20:00", 10, 0, 0, 0, 0, false))
	f := st.Summary().Flows[0]
	if f.ValueMissing != 1 || f.Pattern != nil {
		t.Errorf("value_missing %d pattern %+v, want 1 and no pattern", f.ValueMissing, f.Pattern)
	}
}

func TestLaggingTotalsSuppressPattern(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:20:00", 1, 1, 10, 1_000))
	st.ApplyGame(game("S1", Stock, "09:10:00", 5, 0, 0, 0, 0, false))
	if f := st.Summary().Flows[0]; f.Lagging != 1 || f.Pattern != nil {
		t.Errorf("lagging %d pattern %+v", f.Lagging, f.Pattern)
	}
}

func TestPatternRules(t *testing.T) {
	v := int64(1000) // materiality 1 % → 10
	for _, c := range []struct {
		hot, retail int64
		value       *int64
		rule        string
	}{
		{-10, 10, &v, "hot_out_retail_in"},
		{10, -10, &v, "hot_in_retail_out"},
		{10, 10, &v, "both_in"},
		{-10, -10, &v, "both_out"},
		{-9, 10, &v, "none"}, // hot below materiality: neutral
		{-10, 9, &v, "none"},
		{-10, 10, nil, "none"},
		{-10, 10, i64(0), "none"},
	} {
		p := pattern(c.hot, c.retail, c.value, 0.01)
		if p.Rule != c.rule || p.Text != patternText[c.rule] || p.Text == "" {
			t.Errorf("pattern(%d, %d) = %+v, want %s", c.hot, c.retail, p, c.rule)
		}
	}
}

func TestKPIs(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 7_000))
	s2 := snap("S2", "09:11:00", 1, 1, 1, 9_999)
	s2.Missing = []string{model.FValue}
	st.ApplySnapshot(s2)
	st.ApplySnapshot(snap("G1", "12:10:00", 1, 1, 1, 40))
	st.ApplySnapshot(snap("V1", "12:11:00", 1, 1, 1, 2))
	st.ApplySnapshot(snap("X9", "09:10:00", 1, 1, 1, 1e9)) // unknown: in no KPI
	k := st.Summary().KPIs
	if len(k) != 4 || k[0].ID != "index_total" || k[0].Available || k[0].Reason == "" || k[0].Value != nil {
		t.Fatalf("first KPI must be the unavailable total index: %+v", k[0])
	}
	stock, fixed, metals := k[1], k[2], k[3]
	eq(t, "stock value", stock.Value, 7_000)
	if stock.Instruments != 2 || stock.Missing != 1 || stock.Note != NoteBlockTrades {
		t.Errorf("stock instruments/missing/note = %d/%d/%q", stock.Instruments, stock.Missing, stock.Note)
	}
	if stock.Secondary == nil || stock.Secondary.Class != EquityETF {
		t.Fatalf("stock secondary = %+v", stock.Secondary)
	}
	null(t, "equity etf value (no ETF seen)", stock.Secondary.Value)
	null(t, "fixed income value (no instrument)", fixed.Value)
	eq(t, "gold+silver value", metals.Value, 42)
	if !metals.AsOf.Equal(at("12:11:00")) {
		t.Errorf("metals as_of = %s", metals.AsOf)
	}
	st.ApplySnapshot(snap("E1", "09:10:00", 1, 1, 1, 300))
	k = st.Summary().KPIs
	eq(t, "equity etf value", k[1].Secondary.Value, 300)
	eq(t, "stock value (ETF not in it)", k[1].Value, 7_000)
}

func bars(st *State) map[string]Bar {
	out := map[string]Bar{}
	for _, b := range st.Summary().KPIs[1].Series {
		out[b.At.In(tehran.Loc).Format("15:04")] = b
	}
	return out
}

func deref(p *int64) any {
	if p == nil {
		return "null"
	}
	return *p
}

func checkBars(t *testing.T, got map[string]Bar, want map[string]Bar) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%d windows, want %d", len(got), len(want))
	}
	for w, b := range want {
		g, ok := got[w]
		switch {
		case !ok:
			t.Errorf("window %s missing", w)
		case (g.Value == nil) != (b.Value == nil) || (g.Value != nil && *g.Value != *b.Value) || g.Partial != b.Partial:
			t.Errorf("window %s = value %v partial %v, want %v %v", w, deref(g.Value), g.Partial, deref(b.Value), b.Partial)
		}
	}
}

func TestSeriesCompleteDay(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "08:50:00", 1, 1, 0, 0)) // pre-open zero baseline
	st.ApplySnapshot(snap("S1", "09:01:00", 1, 1, 1, 100))
	st.ApplySnapshot(snap("S1", "09:09:59", 1, 1, 1, 250))
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 400))
	checkBars(t, bars(st), map[string]Bar{
		"08:50": {Value: i64(0)}, "09:00": {Value: i64(250)}, "09:10": {Value: i64(150)},
	})
}

func TestSeriesLateStartPartialFromOpen(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:21:00", 1, 1, 1, 5_000)) // first value above zero
	st.ApplySnapshot(snap("S1", "09:29:50", 1, 1, 1, 5_010)) // same window: no gap
	st.ApplySnapshot(snap("S1", "09:30:10", 1, 1, 1, 5_020)) // 20 s later: no gap
	// Windows from the stock open (09:00) to 09:20 are partial; unobserved ones are null.
	checkBars(t, bars(st), map[string]Bar{
		"09:00": {Partial: true}, "09:10": {Partial: true}, "09:20": {Value: i64(10), Partial: true},
		"09:30": {Value: i64(10)},
	})
}

// A glitch down and back up books only the real increment (finding: phantom bar).
func TestSeriesGlitchKeepsBaseline(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "08:59:00", 1, 1, 0, 0))
	st.ApplySnapshot(snap("S1", "09:15:00", 1, 1, 1, 1_000))
	st.ApplySnapshot(snap("S1", "09:15:10", 1, 1, 1, 10)) // glitch
	st.ApplySnapshot(snap("S1", "09:25:00", 1, 1, 1, 1_010))
	got := bars(st)
	// 09:10 holds the 1000 (15 minutes of trading time since the 09:00 open cross windows:
	// partial); 09:20 books only the real 10, not the 1000 recovered from the glitch.
	checkBars(t, got, map[string]Bar{
		"08:50": {Value: i64(0)}, "09:00": {Partial: true},
		"09:10": {Value: i64(1_000), Partial: true}, "09:20": {Value: i64(10), Partial: true},
	})
}

// A polling gap crossing windows: every window it touches is partial, unobserved ones null.
func TestSeriesGapMarksWindows(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:00:30", 1, 1, 0, 0))
	st.ApplySnapshot(snap("S1", "09:01:00", 1, 1, 1, 100))
	st.ApplySnapshot(snap("S1", "09:45:00", 1, 1, 1, 9_100))
	st.ApplySnapshot(snap("S1", "09:45:05", 1, 1, 1, 9_200))
	checkBars(t, bars(st), map[string]Bar{
		"09:00": {Value: i64(100), Partial: true},
		"09:10": {Partial: true}, "09:20": {Partial: true}, "09:30": {Partial: true},
		"09:40": {Value: i64(9_100), Partial: true},
	})
}

func TestSeriesTrimmedToSeriesBars(t *testing.T) {
	st := testState()
	for i := 0; i < 40*20; i++ { // 09:00 … 15:39:30 every 30 s (no gaps)
		ts := at("09:00:00").Add(time.Duration(i) * 30 * time.Second)
		sn := snap("S1", "09:00:00", 1, 1, 1, int64(i))
		sn.SourceTime, sn.IngestTime = ts, ts
		st.ApplySnapshot(sn)
	}
	s := st.Summary().KPIs[1].Series
	if len(s) != 36 || !s[35].At.Equal(at("15:30:00")) || s[35].Partial || s[35].Value == nil || *s[35].Value != 20 {
		t.Errorf("series len %d last %+v, want 36 bars ending 15:30 with 20", len(s), s[len(s)-1])
	}
}

func TestDayIsDrivenByAdvanceOnly(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 1))
	st.ApplyIssue(model.QualityIssue{InsCode: "S1", Code: quality.Stale, At: at("09:10:01")})
	next := snap("S2", "09:10:00", 1, 1, 1, 1)
	next.SourceTime = next.SourceTime.AddDate(0, 0, 1)
	st.ApplySnapshot(next) // next day's data does not move the day
	st.ApplyIssue(model.QualityIssue{InsCode: "S1", Code: quality.Stale, At: at("08:34:00").AddDate(0, 0, 1)})
	st.ApplySignal(anomaly.Event{InsCode: "S1", Kind: "anomaly", At: at("09:00:00").AddDate(0, 0, -1)})
	if st.Day() != day || len(st.Rows()) != 1 || st.Summary().Issues != 1 || len(st.Radar()) != 0 {
		t.Fatalf("day %s rows %d issues %d radar %d: other days' data must be dropped", st.Day(), len(st.Rows()), st.Summary().Issues, len(st.Radar()))
	}
	if st.Advance("2026-09-22") || st.Day() != day {
		t.Fatal("advancing to an earlier day must be ignored")
	}
	if !st.Advance("2026-09-24") || len(st.Rows()) != 0 || st.Summary().Issues != 0 {
		t.Fatal("a new day must start empty")
	}
	st.ApplySnapshot(next)
	st.ApplyGame(model.GameTotals{InsCode: "S2", Day: day, NetHot: 1})
	if len(st.Rows()) != 1 || st.Rows()[0].NetHot != nil {
		t.Errorf("earlier-day totals must be ignored: %+v", st.Rows())
	}
	fresh := New(DefaultConfig())
	fresh.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 1))
	if len(fresh.Rows()) != 0 {
		t.Error("nothing is accepted before Advance")
	}
}

func TestUnknownNotice(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 1))
	st.ApplySnapshot(snap("S2", "09:10:00", 1, 1, 1, 1))
	st.ApplySnapshot(snap("X1", "09:10:00", 1, 1, 1, 1))
	st.ApplySnapshot(snap("X2", "09:10:00", 1, 1, 1, 1))
	if s := st.Summary(); s.UnknownNotice || s.Unknown != 2 || s.UnknownShare != 0.5 {
		t.Errorf("half unknown: notice %v unknown %d share %v, want no notice", s.UnknownNotice, s.Unknown, s.UnknownShare)
	}
	st.ApplySnapshot(snap("X3", "09:10:00", 1, 1, 1, 1))
	if s := st.Summary(); !s.UnknownNotice {
		t.Errorf("3 of 5 unknown must raise the notice")
	}
}

func TestIssueBeforeSnapshotCounted(t *testing.T) {
	st := testState()
	st.ApplyIssue(model.QualityIssue{InsCode: "S1", Code: quality.Stale, At: at("09:10:01")})
	if len(st.Rows()) != 0 {
		t.Fatal("an issue alone must not create a row")
	}
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 1))
	if r := st.Rows(); len(r) != 1 || r[0].Issues != 1 {
		t.Errorf("rows = %+v, want S1 with 1 issue", r)
	}
}

func TestIssuesSynAndEst(t *testing.T) {
	st := testState()
	s := snap("SYNTHETIC0001", "09:10:00", 1, 1, 1, 1)
	s.SourceTimeEstimated = true
	st.ApplySnapshot(s)
	st.ApplyIssue(model.QualityIssue{InsCode: "SYNTHETIC0001", Code: quality.TimeEstimated, At: at("09:10:01")})
	st.ApplyIssue(model.QualityIssue{InsCode: "SYNTHETIC0001", Code: quality.Incomplete, At: at("09:10:01")})
	sum := st.Summary()
	if !sum.Syn || !sum.Est || sum.Issues != 1 || st.Rows()[0].Issues != 1 || !st.Rows()[0].Syn {
		t.Errorf("syn %v est %v issues %d row %+v", sum.Syn, sum.Est, sum.Issues, st.Rows()[0])
	}
}

func TestDirtyMarkDirtyAndRadar(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 1))
	st.ApplySnapshot(snap("S2", "09:10:00", 1, 1, 1, 1))
	if d := st.Dirty(); len(d) != 2 {
		t.Fatalf("dirty = %d", len(d))
	}
	if d := st.Dirty(); len(d) != 0 {
		t.Fatalf("dirty after clear = %d", len(d))
	}
	st.MarkDirty([]string{"S1", "nope"})
	if d := st.Dirty(); len(d) != 1 || d[0].Ins != "S1" {
		t.Fatalf("re-marked = %+v", d)
	}
	st.ApplySnapshot(snap("S2", "09:11:00", 2, 1, 1, 1))
	if d := st.Dirty(); len(d) != 1 || d[0].Ins != "S2" {
		t.Fatalf("dirty = %+v", d)
	}
	sig, ok := st.ApplySignal(anomaly.Event{InsCode: "S2", Kind: "anomaly", Z: 5.8, At: at("09:11:00"), Reason: "r"})
	if !ok || sig.Sym != "symS2" || sig.Class != Stock || sig.Reason != "r" {
		t.Errorf("signal = %+v", sig)
	}
	st.ApplySignal(anomaly.Event{InsCode: "S1", Kind: "absorption", At: at("09:12:00"), Reason: "d"})
	if r := st.Radar(); len(r) != 2 || r[0].Ins != "S1" {
		t.Errorf("radar must be newest first: %+v", r)
	}
	for i := 0; i < 60; i++ {
		st.ApplySignal(anomaly.Event{InsCode: "S1", Kind: "anomaly", At: at("09:13:00"), Reason: "x"})
	}
	if len(st.Radar()) != 50 {
		t.Errorf("radar keeps %d, want 50", len(st.Radar()))
	}
}

func TestSessions(t *testing.T) {
	ss := Sessions(calendar.Default(), at("10:00:00"))
	if len(ss) != len(Classes) {
		t.Fatalf("sessions = %d", len(ss))
	}
	for _, s := range ss {
		if !s.Open {
			t.Errorf("%s closed on a Wednesday", s.Class)
		}
		if s.Class == Gold && !s.Start.Equal(at("12:00:00")) {
			t.Errorf("gold opens %s", s.Start)
		}
	}
	for _, s := range Sessions(calendar.Default(), at("10:00:00").AddDate(0, 0, 2)) {
		if s.Open {
			t.Errorf("%s open on a Friday", s.Class)
		}
	}
}

// feed applies one stock snapshot of S1 every step from `from` to `to` with value growing by inc.
func feed(st *State, from, to string, step time.Duration, v0, inc, vol0 int64) (int64, int64) {
	v, vol := v0, vol0
	for ts := at(from); !ts.After(at(to)); ts = ts.Add(step) {
		sn := snap("S1", "09:00:00", 1, 1, vol, v)
		sn.SourceTime, sn.IngestTime = ts, ts
		st.ApplySnapshot(sn)
		v += inc
		vol++
	}
	return v, vol
}

// The vendor still shows yesterday's totals just after the open and resets at 09:00:10 (owner
// scenario, internal/flow session_test (a)): the series re-baselines at the reset.
func TestSeriesVendorResetAfterOpen(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:00:05", 1, 1, 5_000_000, 50_000_000_000)) // yesterday's totals
	feed(st, "09:00:10", "09:19:50", 20*time.Second, 1_000_000, 1_000_000, 100)
	got := bars(st)
	if b := got["09:00"]; !b.Partial {
		t.Errorf("09:00 = %+v, want partial (yesterday's totals, then the reset)", b)
	}
	// 09:10 … 09:19:50: 30 snapshots × 1,000,000, the first booked against 09:09:50's value.
	if b := got["09:10"]; b.Partial || b.Value == nil || *b.Value != 30_000_000 {
		t.Errorf("09:10 = value %v partial %v, want 30,000,000 complete", deref(b.Value), b.Partial)
	}
}

func TestSeriesDips(t *testing.T) {
	// Same window: dip and recovery; the increment is booked against the kept baseline.
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 1, 1_000))
	st.ApplySnapshot(snap("S1", "09:10:05", 1, 1, 1, 900))
	st.ApplySnapshot(snap("S1", "09:10:10", 1, 1, 1, 1_100))
	if b := bars(st)["09:10"]; !b.Partial || b.Value == nil || *b.Value != 100 {
		t.Errorf("same-window dip: %+v value %v", b, deref(b.Value))
	}
	// Straddling a boundary: the recovery window is partial too.
	st = testState()
	st.ApplySnapshot(snap("S1", "08:59:00", 1, 1, 0, 0))
	st.ApplySnapshot(snap("S1", "09:09:50", 1, 1, 1, 1_000))
	st.ApplySnapshot(snap("S1", "09:09:55", 1, 1, 1, 900))
	st.ApplySnapshot(snap("S1", "09:10:05", 1, 1, 1, 1_100))
	if b := bars(st)["09:10"]; !b.Partial || *b.Value != 100 {
		t.Errorf("straddling dip: recovery window %+v value %v, want partial 100", b, deref(b.Value))
	}
	// A dip that outlasts its window is a new level: re-baseline there.
	st = testState()
	st.ApplySnapshot(snap("S1", "08:59:00", 1, 1, 0, 0))
	st.ApplySnapshot(snap("S1", "09:09:50", 1, 1, 1, 1_000))
	st.ApplySnapshot(snap("S1", "09:09:55", 1, 1, 1, 900))
	st.ApplySnapshot(snap("S1", "09:10:05", 1, 1, 1, 950))
	st.ApplySnapshot(snap("S1", "09:10:25", 1, 1, 1, 1_000))
	if b := bars(st)["09:10"]; !b.Partial || *b.Value != 50 {
		t.Errorf("outlasting dip: %+v value %v, want partial 50 (from the new level 950)", b, deref(b.Value))
	}
}

func TestSeriesGapClippedToSession(t *testing.T) {
	st := testState()
	feed(st, "12:10:00", "12:29:50", 10*time.Second, 1_000, 10, 1) // 12:10 partial: first value > 0
	sn := snap("S1", "12:45:00", 1, 1, 200, 1_000+120*10+5)        // after the 12:30 close
	st.ApplySnapshot(sn)
	if b := bars(st)["12:20"]; b.Partial {
		t.Errorf("12:20 = %+v: 10 s of trading time before the close is no gap", b)
	}
}

func TestShowsTradingValueWithoutVolume(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1010, 1000, 0, 5)) // volume 0 but value > 0
	if r := st.Rows()[0]; r.NetHot != nil || !r.Traded || r.Chg == nil {
		t.Errorf("row = %+v: value > 0 is trading (net_hot unknown)", r)
	}
	st.ApplySnapshot(snap("S2", "09:10:00", 1010, 1000, 0, 0))
	if r := st.Rows()[1]; r.Traded || r.Chg != nil || r.Last == nil {
		t.Errorf("untraded row = %+v: no change of today, last price kept", r)
	}
}

func TestLaggingIgnoresSnapshotsTheEngineSkips(t *testing.T) {
	st := testState()
	st.ApplySnapshot(snap("S1", "09:10:00", 1, 1, 10, 100))
	st.ApplyGame(game("S1", Stock, "09:10:00", 10, 0, 0, 0, 0, false))
	sn := snap("S1", "09:11:00", 1, 1, 20, 200)
	sn.Missing = []string{model.FIndBuyCount} // incomplete: never processed by the engine
	st.ApplySnapshot(sn)
	if st.Rows()[0].Lagging {
		t.Error("an incomplete snapshot cannot make the totals lag")
	}
}

func TestSyntheticPredicateCoversEarlyData(t *testing.T) {
	syn := map[string]bool{}
	st := testState()
	st.SetSynthetic(func(ins string) bool { return syn[ins] }, false)
	// Signal and issue of IRX1 arrive before the snapshot that identifies it as synthetic.
	st.ApplySignal(anomaly.Event{InsCode: "IRX1", Kind: "anomaly", At: at("09:10:00"), Reason: "r"})
	st.ApplyIssue(model.QualityIssue{InsCode: "IRX1", Code: quality.Stale, At: at("09:10:00")})
	st.ApplyIssue(model.QualityIssue{Code: quality.Stale, At: at("09:10:00")}) // no instrument: counted
	syn["IRX1"] = true
	if len(st.Radar()) != 0 || st.Summary().Issues != 1 {
		t.Errorf("radar %d issues %d: synthetic data must be left out", len(st.Radar()), st.Summary().Issues)
	}
	st.SetSynthetic(func(ins string) bool { return syn[ins] }, true)
	if r := st.Radar(); len(r) != 1 || !r[0].Syn || st.Summary().Issues != 2 || !st.Summary().Syn {
		t.Errorf("shown: radar %+v issues %d syn %v, want labelled", r, st.Summary().Issues, st.Summary().Syn)
	}
}

// Trade count alone is day activity: a pre-open snapshot with only trades ≠ 0 is carryover
// (the engine's PREV_DAY_CARRYOVER definition).
func TestPreOpenCarryoverTradeCountOnly(t *testing.T) {
	st := testState()
	sn := snap("S1", "08:50:00", 1, 1, 0, 0)
	sn.TradeCount = 3
	st.ApplySnapshot(sn)
	if s := st.Summary(); s.Carryover != 1 || s.Instruments != 0 {
		t.Errorf("carryover %d instruments %d, want 1/0", s.Carryover, s.Instruments)
	}
}
