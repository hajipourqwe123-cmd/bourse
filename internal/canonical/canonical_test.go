package canonical

import (
	"testing"
)

func TestParseISINAndClasses(t *testing.T) {
	cases := []struct{ isin, company, class string }{
		{"IRO1FOLD0001", "FOLD", ClassOrdinaryShare},
		{"IRR3ARFZ0101", "ARFZ", ClassRightsIssue},
		{"IRT1YGHT0002", "YGHT", ClassFundUnit},
		{"IRE9ENRG0001", "ENRG", ClassEnergy},
	}
	for _, c := range cases {
		p, err := ParseISIN(c.isin)
		if err != nil {
			t.Errorf("%s: %v", c.isin, err)
			continue
		}
		if p.CompanyCode != c.company || p.Class != c.class {
			t.Errorf("%s = %+v, want company %s class %s", c.isin, p, c.company, c.class)
		}
	}
	for _, bad := range []string{"", "US0378331005", "IRO1FOLD", "IRO1FOLD00011", "xx"} {
		if _, err := ParseISIN(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

// A rights issue and its parent share the ISIN company code, which is how the
// two are linked without relying on tickers.
func TestCompanyGroupLinksRightsIssueToParent(t *testing.T) {
	m := NewMaster()
	for _, in := range []Instrument{
		{InstrumentID: "i1", Ticker: "ارفع", CompanyID: "ARFZ", ValidFrom: "2026-01-01", Status: StatusListed},
		{InstrumentID: "i2", Ticker: "ارفعح", CompanyID: "ARFZ", ValidFrom: "2026-05-01", ValidTo: "2026-07-01",
			InstrumentClass: ClassRightsIssue, Status: StatusListed},
		{InstrumentID: "i3", Ticker: "فولاد", CompanyID: "FOLD", ValidFrom: "2026-01-01", Status: StatusListed},
	} {
		if err := m.Add(in); err != nil {
			t.Fatal(err)
		}
	}
	if g := m.CompanyGroup("ARFZ", "2026-06-01"); len(g) != 2 {
		t.Errorf("during the rights window the group is %v, want both instruments", g)
	}
	if g := m.CompanyGroup("ARFZ", "2026-09-01"); len(g) != 1 {
		t.Errorf("after the rights issue expires the group is %v, want only the parent", g)
	}
	if err := m.Add(Instrument{Ticker: "no-id"}); err == nil {
		t.Error("an instrument without instrument_id must be rejected")
	}
}

// Identity survives a rename, and a ticker reused by a different instrument
// later resolves by date, never by ticker alone.
func TestRenameAndTickerReuseResolveByDate(t *testing.T) {
	m := NewMaster()
	must := func(in Instrument) {
		if err := m.Add(in); err != nil {
			t.Fatal(err)
		}
	}
	must(Instrument{InstrumentID: "i1", InsCode: "111", Ticker: "الف",
		ValidFrom: "2020-01-01", ValidTo: "2023-01-01", Status: StatusListed})
	must(Instrument{InstrumentID: "i1", InsCode: "111", Ticker: "ب", PreviousTicker: "الف",
		ValidFrom: "2023-01-01", Status: StatusListed})
	// A different instrument later takes the retired ticker.
	must(Instrument{InstrumentID: "i2", InsCode: "222", Ticker: "الف",
		ValidFrom: "2025-01-01", Status: StatusListed})

	if got := m.ResolveTicker("الف", "2021-06-01"); len(got) != 1 || got[0] != "i1" {
		t.Errorf(`"الف" in 2021 = %v, want [i1]`, got)
	}
	if got := m.ResolveTicker("الف", "2025-06-01"); len(got) != 1 || got[0] != "i2" {
		t.Errorf(`"الف" in 2025 = %v, want [i2]`, got)
	}
	// The same instrument_id spans the rename.
	v, ok := m.At("i1", "2024-01-01")
	if !ok || v.Ticker != "ب" || v.PreviousTicker != "الف" {
		t.Errorf("identity after the rename = %+v", v)
	}
	if m.Len() != 2 {
		t.Errorf("distinct instruments = %d, want 2", m.Len())
	}
}

func TestTickerCollisionIsReportedNotGuessed(t *testing.T) {
	m := NewMaster()
	m.Add(Instrument{InstrumentID: "i1", Ticker: "دوگانه", ValidFrom: "2026-01-01", Status: StatusListed})
	m.Add(Instrument{InstrumentID: "i2", Ticker: "دوگانه", ValidFrom: "2026-01-01", Status: StatusListed})
	c := m.TickerCollisions("2026-06-01")
	if len(c) != 1 || len(c["دوگانه"]) != 2 {
		t.Fatalf("collision not reported: %v", c)
	}
	if got := m.ResolveTicker("دوگانه", "2026-06-01"); len(got) != 2 {
		t.Errorf("an ambiguous ticker must return every candidate, got %v", got)
	}
}

// The universe on a date includes delisted names that were listed then, which
// is what makes a backtest survivorship-aware.
func TestUniverseIsPointInTime(t *testing.T) {
	m := NewMaster()
	m.Add(Instrument{InstrumentID: "alive", Ticker: "a", ValidFrom: "2020-01-01", Status: StatusListed})
	m.Add(Instrument{InstrumentID: "dead", Ticker: "d", ValidFrom: "2020-01-01", ValidTo: "2022-01-01",
		Status: StatusListed, DelistingDate: "2022-01-01"})
	m.Add(Instrument{InstrumentID: "dead", Ticker: "d", ValidFrom: "2022-01-01",
		Status: StatusDelisted, DelistingDate: "2022-01-01"})

	if u := m.Universe("2021-06-01", StatusListed); len(u) != 2 {
		t.Errorf("2021 universe = %v, want both (delisted name was still listed then)", u)
	}
	if u := m.Universe("2023-06-01", StatusListed); len(u) != 1 || u[0] != "alive" {
		t.Errorf("2023 listed universe = %v, want [alive]", u)
	}
	if u := m.Universe("2023-06-01"); len(u) != 2 {
		t.Errorf("2023 universe with no status filter = %v, want both", u)
	}
}

// Trading days are established by observed trades, never by the weekday.
func TestTradingCalendarEvidencePrecedence(t *testing.T) {
	c := NewTradingCalendar()
	// A Saturday with no data anywhere: possible closure, NOT a holiday.
	c.ObserveNoData("2026-02-07", "brsapi") // Saturday
	d, _ := c.Day("2026-02-07")
	if d.ClosureType != PossibleMarketClosure || d.IsTradingDay || d.Verified {
		t.Errorf("no-data Saturday = %+v, want unverified POSSIBLE_MARKET_CLOSURE", d)
	}
	// Thursday with no data: weekend by rule, still unverified.
	c.ObserveNoData("2026-02-05", "brsapi")
	if d, _ := c.Day("2026-02-05"); d.ClosureType != WeekendByRule || d.Verified {
		t.Errorf("no-data Thursday = %+v, want unverified WEEKEND_BY_RULE", d)
	}
	// Observed trades promote a date and cannot be downgraded afterwards.
	c.ObserveTrading("2026-02-07", "brsapi", 812)
	c.ObserveNoData("2026-02-07", "sourcearena")
	d, _ = c.Day("2026-02-07")
	if !d.IsTradingDay || d.ClosureType != ObservedTrading || !d.Verified || d.InstrumentsTraded != 812 {
		t.Errorf("after observing trades = %+v, want verified OBSERVED_TRADING", d)
	}
	// Trades on a Friday are evidence too: the weekly rule is not a law.
	c.ObserveTrading("2026-02-06", "brsapi", 3)
	if d, _ := c.Day("2026-02-06"); !d.IsTradingDay {
		t.Error("observed Friday trades must mark the day as trading")
	}
	// Only a verified source confirms a closure.
	c.ConfirmClosure("2026-03-21", "Nowruz", "official notice")
	d, _ = c.Day("2026-03-21")
	if d.ClosureType != ConfirmedClosure || !d.Verified || d.HolidayReason != "Nowruz" {
		t.Errorf("confirmed closure = %+v", d)
	}
	c.ObserveNoData("2026-03-21", "brsapi") // must not weaken a confirmed closure
	if d, _ := c.Day("2026-03-21"); d.ClosureType != ConfirmedClosure {
		t.Error("a confirmed closure must not be downgraded by missing data")
	}
	if u := c.UnverifiedClosures(); len(u) != 1 || u[0] != "2026-02-07" {
		// 2026-02-07 was promoted to trading, so it should NOT be listed.
		if len(u) != 0 {
			t.Errorf("unverified closures = %v, want none after promotion", u)
		}
	}
}

func TestNormalizeBrsAPIHistoryPreservesRawAndConvertsDates(t *testing.T) {
	type0 := []byte(`[
	 {"date":"1405-07-01","time":"12:30:00","tno":21174,"tvol":2210827100,"tval":7398241941410,
	  "pmin":3280,"pmax":3350,"py":3260,"pf":3350,"pl":3350,"pc":3350},
	 {"date":"1405-06-31","time":"12:29:00","tno":0,"tvol":0,"tval":0,
	  "pmin":0,"pmax":0,"py":3260,"pf":0,"pl":3260,"pc":3260}]`)
	type1 := []byte(`[{"date":"1405-07-01","Buy_CountI":2232,"Buy_CountN":24,"Sell_CountI":4728,
	  "Sell_CountN":27,"Buy_I_Volume":1345256558,"Buy_N_Volume":865570542,
	  "Sell_I_Volume":1703245171,"Sell_N_Volume":507581929}]`)

	bars, problems, err := NormalizeBrsAPIHistory("i1", "brsapi", type0, type1)
	if err != nil || len(problems) != 0 {
		t.Fatalf("normalize: %v %v", err, problems)
	}
	if len(bars) != 2 {
		t.Fatalf("bars = %d, want 2", len(bars))
	}
	// Oldest first, Jalali converted to ISO.
	if bars[0].TradeDate != "2026-09-22" || bars[1].TradeDate != "2026-09-23" {
		t.Fatalf("dates = %s, %s", bars[0].TradeDate, bars[1].TradeDate)
	}
	b := bars[1]
	if b.FirstPrice != 3350 || b.High != 3350 || b.Low != 3280 || b.Volume != 2210827100 || b.TradeCount != 21174 {
		t.Errorf("raw price/volume not preserved: %+v", b)
	}
	if b.RealBuyVolume != 1345256558 || b.LegalBuyVolume != 865570542 || b.RealBuyCount != 2232 {
		t.Errorf("real/legal flows not mapped: %+v", b)
	}
	// History carries no shares_outstanding or base volume: flagged, not invented.
	if b.SharesOutstanding != 0 || b.BaseVolume != 0 {
		t.Error("point-in-time attributes must not be backfilled")
	}
	if !hasCode(b.QualityCodes, QMissingPointInTime) {
		t.Errorf("missing point-in-time attributes must be flagged: %v", b.QualityCodes)
	}
	// The row without a type=1 match is flagged as missing real/legal.
	if !hasCode(bars[0].QualityCodes, QMissingRealLegal) {
		t.Errorf("absent real/legal must be flagged: %v", bars[0].QualityCodes)
	}
	// A halted row keeps zero prices and classifies as HALTED, not a lock.
	ClassifyStates(bars)
	if bars[0].TradingState == "LIMIT_UP_LOCKED" || bars[0].TradingState == "LIMIT_DOWN_LOCKED" {
		t.Errorf("zero-volume row must never be a lock, got %s", bars[0].TradingState)
	}
	if _, _, err := NormalizeBrsAPIHistory("i1", "brsapi", []byte(`not json`), nil); err == nil {
		t.Error("a non-JSON body must be an error, not an empty series")
	}
	// An unparsable provider date is reported, never silently dropped.
	_, probs, err := NormalizeBrsAPIHistory("i1", "brsapi", []byte(`[{"date":"9999-99-99","pl":1}]`), nil)
	if err != nil || len(probs) != 1 {
		t.Errorf("bad date = %v, problems %v; want one reported problem", err, probs)
	}
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

func TestQualityGatesDetectWithoutRepairing(t *testing.T) {
	cal := NewTradingCalendar()
	for _, d := range []string{"2026-01-05", "2026-01-06", "2026-01-07"} {
		cal.ObserveTrading(d, "brsapi", 900)
	}
	master := NewMaster()
	master.Add(Instrument{InstrumentID: "i1", Ticker: "x", ValidFrom: "2026-01-01", Status: StatusListed})

	bars := []DailyBar{
		// impossible OHLC: last above high
		{TradeDate: "2026-01-05", InstrumentID: "i1", Volume: 10, Value: 100, High: 100, Low: 90,
			FirstPrice: 95, LastPrice: 120, ClosingPrice: 95, PreviousClose: 95, TradeCount: 2,
			RequestedDate: "2026-01-05", ServedDate: "2026-01-05"},
		// wrong served date + close outside the traded range (base-volume rule)
		{TradeDate: "2026-01-06", InstrumentID: "i1", Volume: 50, Value: 5000, High: 110, Low: 100,
			FirstPrice: 105, LastPrice: 105, ClosingPrice: 130, PreviousClose: 95, TradeCount: 1,
			RequestedDate: "2026-01-06", ServedDate: "2026-01-04"},
		// negative volume: OHLC gates are skipped on a non-traded row, but the
		// negative value itself must still be reported
		{TradeDate: "2026-01-06", InstrumentID: "i1", Volume: -5, Value: -10, High: 0, Low: 0,
			ClosingPrice: 130, PreviousClose: 130, TradeCount: -1,
			RequestedDate: "2026-01-06", ServedDate: "2026-01-06"},
		// unknown instrument (no master version covers it)
		{TradeDate: "2026-01-07", InstrumentID: "ghost", Volume: 1, Value: 1, High: 10, Low: 10,
			FirstPrice: 10, LastPrice: 10, ClosingPrice: 10, PreviousClose: 10, TradeCount: 1,
			RequestedDate: "2026-01-07", ServedDate: "2026-01-07"},
	}
	before := bars[0].High
	q := CheckBars(bars, master, cal)

	for _, code := range []string{QImpossibleOHLC, QWrongServedDate, QNegativeVolumeValue,
		QCloseOutsideRange, QUnknownInstrument, QActionDiscontinuity} {
		if q.Counts[code] == 0 {
			t.Errorf("gate %s did not fire: %v", code, q.Counts)
		}
	}
	if bars[0].High != before {
		t.Error("gates must not modify raw values")
	}
	if !hasCode(bars[0].QualityCodes, QImpossibleOHLC) {
		t.Errorf("row-level code missing: %v", bars[0].QualityCodes)
	}
	if q.BarsChecked != 4 {
		t.Errorf("bars checked = %d, want 4", q.BarsChecked)
	}
	if q.Counts[QDuplicateObs] == 0 {
		t.Error("the two 2026-01-06 rows must be reported as a duplicate observation")
	}
}

func TestQualityGateMissingTradingDayAndDuplicates(t *testing.T) {
	cal := NewTradingCalendar()
	for _, d := range []string{"2026-01-05", "2026-01-06", "2026-01-07"} {
		cal.ObserveTrading(d, "brsapi", 900)
	}
	bars := []DailyBar{
		{TradeDate: "2026-01-05", InstrumentID: "i1", Volume: 1, High: 10, Low: 10, FirstPrice: 10,
			LastPrice: 10, ClosingPrice: 10, TradeCount: 1, RequestedDate: "2026-01-05", ServedDate: "2026-01-05"},
		{TradeDate: "2026-01-05", InstrumentID: "i1", Volume: 1, High: 10, Low: 10, FirstPrice: 10,
			LastPrice: 10, ClosingPrice: 10, TradeCount: 1, RequestedDate: "2026-01-05", ServedDate: "2026-01-05"},
		{TradeDate: "2026-01-07", InstrumentID: "i1", Volume: 1, High: 10, Low: 10, FirstPrice: 10,
			LastPrice: 10, ClosingPrice: 10, TradeCount: 1, RequestedDate: "2026-01-07", ServedDate: "2026-01-07"},
	}
	q := CheckBars(bars, nil, cal)
	if q.Counts[QDuplicateObs] != 1 {
		t.Errorf("duplicate not detected: %v", q.Counts)
	}
	if q.Counts[QMissingTradingDay] != 1 {
		t.Errorf("missing verified trading day (2026-01-06) not detected: %v", q.Counts)
	}
	have, expected, ratio := CoverageOf(bars, cal, "2026-01-01", "2026-01-31")
	if have != 2 || expected != 3 || ratio > 0.67 || ratio < 0.66 {
		t.Errorf("coverage = %d/%d (%.4f), want 2/3", have, expected, ratio)
	}
}

func TestBenchmarkQuantifiesDisagreement(t *testing.T) {
	a := []DailyBar{
		{TradeDate: "2026-01-05", LastPrice: 100, ClosingPrice: 99, High: 101, Low: 98, Volume: 1000, TradeCount: 10},
		{TradeDate: "2026-01-06", LastPrice: 200, ClosingPrice: 198, High: 202, Low: 196, Volume: 2000, TradeCount: 20},
		{TradeDate: "2026-01-07", LastPrice: 300, ClosingPrice: 300, High: 300, Low: 300, Volume: 3000, TradeCount: 30},
	}
	b := []DailyBar{
		// same last, different volume (A higher), and one date only in A
		{TradeDate: "2026-01-05", LastPrice: 100, ClosingPrice: 99, High: 101, Low: 98, Volume: 900, TradeCount: 10},
		{TradeDate: "2026-01-06", LastPrice: 200, ClosingPrice: 198, High: 202, Low: 196, Volume: 1800, TradeCount: 20},
		{TradeDate: "2026-01-08", LastPrice: 400, ClosingPrice: 400, High: 400, Low: 400, Volume: 4000, TradeCount: 40},
	}
	r := Benchmark("i1", "brsapi", "sourcearena", a, b)
	if r.OverlapDates != 2 || r.OnlyInA != 1 || r.OnlyInB != 1 {
		t.Fatalf("overlap=%d onlyA=%d onlyB=%d", r.OverlapDates, r.OnlyInA, r.OnlyInB)
	}
	byField := map[string]FieldStats{}
	for _, f := range r.Fields {
		byField[f.Field] = f
	}
	if f := byField["last_price"]; f.ExactMatchRate != 1 || f.BiasDirection != "none" {
		t.Errorf("last_price should agree exactly: %+v", f)
	}
	if f := byField["volume"]; f.ExactMatches != 0 || f.BiasDirection != "a_higher" || f.MeanRelError <= 0 {
		t.Errorf("volume disagreement not quantified: %+v", f)
	}
	// Flow fields absent from both are missing, never counted as agreement.
	if f := byField["real_buy_volume"]; f.Compared != 0 || f.ExactMatches != 0 || f.MissingA != 2 {
		t.Errorf("absent flows must count as missing: %+v", f)
	}
	if r.MissingRateA <= 0 || r.MissingRateB <= 0 {
		t.Errorf("missing rates = %v / %v, want both positive", r.MissingRateA, r.MissingRateB)
	}
}

func TestIndustryMembershipRefusesUndatedBackfill(t *testing.T) {
	tb := NewIndustryTable()
	if err := tb.Add(IndustryMembership{InstrumentID: "i1", IndustryID: "44",
		Source: "brsapi snapshot", Confidence: ConfidenceObserved}); err == nil {
		t.Error("an undated membership must be rejected")
	}
	if err := tb.Add(IndustryMembership{InstrumentID: "i1", IndustryID: "44",
		ValidFrom: "2026-09-25", Source: "brsapi snapshot", Confidence: ConfidenceObserved}); err != nil {
		t.Fatal(err)
	}
	// Before the observation date the membership is unknown, not assumed.
	if _, ok := tb.At("i1", "2026-01-05"); ok {
		t.Error("membership must be unknown before it was observed")
	}
	if m, ok := tb.At("i1", "2026-10-01"); !ok || m.IndustryID != "44" {
		t.Errorf("membership on/after the observation date = %+v, %v", m, ok)
	}
	if n, ratio := tb.KnownCoverage([]string{"i1", "i2"}, "2026-01-05"); n != 0 || ratio != 0 {
		t.Errorf("historical coverage = %d (%v), want 0", n, ratio)
	}
	if n, ratio := tb.KnownCoverage([]string{"i1", "i2"}, "2026-10-01"); n != 1 || ratio != 0.5 {
		t.Errorf("coverage = %d (%v), want 1 (0.5)", n, ratio)
	}
	if got := tb.Members("44", "2026-10-01"); len(got) != 1 {
		t.Errorf("members = %v", got)
	}
}

// SourceArena publishes no last price, value, trade count or real/legal split.
// Those fields must be reported as missing, not as disagreement, and its
// closing price must be compared against the other provider's closing price.
func TestDeclaredFieldAbsenceIsNotDisagreement(t *testing.T) {
	sa := []byte(`[{"date":"1405/06/31","close_price":"3230","first_price":"3380",
	  "highest_price":"3380","lowest_price":"3200","trade_volume":"3705610251"}]`)
	b, problems, err := NormalizeSourceArenaHistory("i1", "sourcearena", sa)
	if err != nil || len(problems) != 0 || len(b) != 1 {
		t.Fatalf("normalize: %v %v %d", err, problems, len(b))
	}
	if b[0].TradeDate != "2026-09-22" || b[0].LastPrice != 3230 || b[0].Volume != 3705610251 {
		t.Fatalf("string values not parsed: %+v", b[0])
	}
	// "close_price" is the last traded price, so no official close is claimed.
	if b[0].ClosingPrice != 0 || !b[0].Absent("closing_price") {
		t.Errorf("SourceArena must not claim an official closing price: %+v", b[0])
	}
	if !b[0].Absent("closing_price") || b[0].Absent("last_price") {
		t.Error("absence must be declared for closing_price, not last_price")
	}
	a := []DailyBar{{TradeDate: "2026-09-22", ClosingPrice: 3230, LastPrice: 3260,
		FirstPrice: 3380, High: 3380, Low: 3200, Volume: 3705610251, TradeCount: 45559}}
	r := Benchmark("i1", "brsapi", "sourcearena", a, b)
	byField := map[string]FieldStats{}
	for _, f := range r.Fields {
		byField[f.Field] = f
	}
	if f := byField["last_price"]; f.Compared != 1 || f.ExactMatches != 0 {
		t.Errorf("last prices must be compared (3260 vs 3230): %+v", f)
	}
	if f := byField["closing_price"]; f.Compared != 0 || f.MissingB != 1 {
		t.Errorf("closing_price must be missing on side B, not compared: %+v", f)
	}
	if _, _, err := NormalizeSourceArenaHistory("i1", "sa", []byte(`[{"date":"1405/06/31","close_price":"abc"}]`)); err != nil {
		t.Errorf("unparsable number should be a reported problem, not an error: %v", err)
	}
}
