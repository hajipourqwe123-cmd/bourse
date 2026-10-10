// Command canonical proves the Phase 5B architecture against recorded provider
// data. It makes no network requests: every input is a raw response already on
// disk, so the run is reproducible and no credential is involved.
//
//	canonical build -recordings DIR -archive DIR -out FILE
//
// It builds a security master and a dated industry table from a whole-market
// snapshot, normalizes one instrument's full daily history into canonical rows,
// classifies trading states, runs the quality gates, benchmarks two providers
// on their overlapping observations, and estimates survivorship exposure by
// comparing the traded symbols in the tablokhani archive against the
// currently-listed universe.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bourse/internal/canonical"
)

// snapshotRow is the subset of BrsApi AllSymbols (type=1) that carries identity.
type snapshotRow struct {
	Time string      `json:"time"`
	L18  string      `json:"l18"`  // ticker
	L30  string      `json:"l30"`  // long name
	ISIN string      `json:"isin"` //
	ID   json.Number `json:"id"`   // TSETMC instrument code
	CS   string      `json:"cs"`   // industry name
	CSID json.Number `json:"cs_id"`
	Z    json.Number `json:"z"`    // shares outstanding
	BVol json.Number `json:"bvol"` // base volume
	TMin json.Number `json:"tmin"` // allowed-range floor
	TMax json.Number `json:"tmax"` // allowed-range ceiling
	TVol json.Number `json:"tvol"`
}

type report struct {
	GeneratedAt    string             `json:"generated_at"`
	Inputs         map[string]string  `json:"inputs"`
	SecurityMaster securityMasterStat `json:"security_master"`
	Industry       industryStat       `json:"industry_membership"`
	Instrument     instrumentStat     `json:"normalized_instrument"`
	Calendar       calendarStat       `json:"trading_calendar"`
	Benchmark      any                `json:"provider_benchmark"`
	Survivorship   survivorshipStat   `json:"survivorship"`
	Notes          []string           `json:"notes"`
}

type securityMasterStat struct {
	SnapshotTradeDate  string         `json:"snapshot_trade_date"`
	Instruments        int            `json:"instruments"`
	WithISIN           int            `json:"with_parsable_isin"`
	ByClass            map[string]int `json:"by_instrument_class"`
	CompaniesMultiInst int            `json:"companies_with_multiple_instruments"`
	TickerCollisions   int            `json:"ticker_collisions_on_snapshot_date"`
	RightsLinkedParent int            `json:"rights_issues_linked_to_a_parent"`
	Ambiguities        []string       `json:"identity_ambiguities"`
	WithPriceLimits    int            `json:"with_allowed_range_limits"`
	WithSharesOutstand int            `json:"with_shares_outstanding"`
}

type industryStat struct {
	Industries           int     `json:"industries"`
	InstrumentsWithDated int     `json:"instruments_with_dated_membership"`
	ValidFrom            string  `json:"valid_from"`
	CoverageAtSnapshot   float64 `json:"coverage_at_snapshot_date"`
	CoverageBefore       float64 `json:"coverage_before_snapshot_date"`
}

type instrumentStat struct {
	InstrumentID    string         `json:"instrument_id"`
	Ticker          string         `json:"ticker"`
	Rows            int            `json:"canonical_rows"`
	Earliest        string         `json:"earliest"`
	Latest          string         `json:"latest"`
	YearsSpanned    float64        `json:"years_spanned"`
	TradedDays      int            `json:"traded_days"`
	ZeroVolumeDays  int            `json:"zero_volume_days"`
	StateCounts     map[string]int `json:"trading_state_counts"`
	RowsOnThuFri    int            `json:"rows_dated_thursday_or_friday"`
	TradedOnThuFri  int            `json:"traded_rows_dated_thursday_or_friday"`
	QualityCounts   map[string]int `json:"quality_findings"`
	NormalizeIssues []string       `json:"normalize_problems,omitempty"`
}

type calendarStat struct {
	DatesRecorded  int            `json:"dates_recorded"`
	StatusCounts   map[string]int `json:"status_counts"`
	TradingDays    int            `json:"verified_trading_days"`
	UnverifiedShut int            `json:"unverified_possible_closures"`
	Note           string         `json:"note"`
}

type survivorshipStat struct {
	ArchiveSymbols        int     `json:"archive_traded_symbols_2026"`
	CurrentlyListed       int     `json:"currently_listed_symbols"`
	AbsentFromCurrentList int     `json:"archive_symbols_absent_from_current_list"`
	ExposureRatio         float64 `json:"survivorship_exposure_ratio_upper_bound"`
	// Absent symbols split by cause. A rights issue expires by design, so it
	// is not a delisting; counting it as one would overstate the bias.
	AbsentRightsIssue  int      `json:"absent_rights_issue_suffix"`
	AbsentNonRights    int      `json:"absent_excluding_rights_issues"`
	ExposureExclRights float64  `json:"survivorship_exposure_excluding_rights"`
	ExamplesRights     []string `json:"examples_rights_issue"`
	ExamplesNonRights  []string `json:"examples_excluding_rights"`
	ListedNotInArchive int      `json:"listed_symbols_absent_from_archive"`
	Note               string   `json:"note"`
}

func main() {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	rec := fs.String("recordings", `D:\Bourse\bourse\recordings`, "recorded raw provider responses")
	arch := fs.String("archive", `D:\Bourse\data\tablokhani_archive`, "tablokhani archive root (read-only)")
	out := fs.String("out", `D:\Bourse\data\canonical_proof_report.json`, "report path (outside git)")
	// The whole-market snapshot was recorded 2026-09-25 20:18 UTC. Its `time`
	// field is 12:30:01 Tehran, and the last trading day before the recording
	// was Wednesday 2026-09-23, which is also the newest row in the history
	// sample. The snapshot is therefore attributed to that trade date.
	snapDate := fs.String("snapshot-date", "2026-09-23", "trade date the whole-market snapshot describes")
	if len(os.Args) > 1 && os.Args[1] == "build" {
		fs.Parse(os.Args[2:])
	} else {
		fs.Parse(os.Args[1:])
	}

	r := report{GeneratedAt: nowISO(), Inputs: map[string]string{}}
	master, _, smStat, indStat, listed := buildMaster(*rec, *snapDate, &r)
	r.SecurityMaster, r.Industry = smStat, indStat

	bars, instStat, cal := buildInstrument(*rec, master, &r)
	r.Instrument = instStat
	r.Calendar = calendarStatOf(cal)
	r.Benchmark = runBenchmark(*rec, bars, &r)
	r.Survivorship = survivorship(*arch, listed)

	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printSummary(r, *out)
}

func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

func parseISO(s string) (time.Time, error) { return time.Parse("2006-01-02", s) }

func buildMaster(rec, snapDate string, r *report) (*canonical.Master, *canonical.IndustryTable, securityMasterStat, industryStat, map[string]bool) {
	// No single AllSymbols request returns the whole market: type=1 carries
	// equities, funds and rights issues (1608 instruments) and type=4 carries
	// debt instruments (597), with no overlap between them. The universe is
	// the union, and whether further types exist is an open question
	// (docs/provider_truth_benchmark.md).
	var rows []snapshotRow
	for _, f := range []struct{ key, name string }{
		{"whole_market_snapshot_type1", "allsymbols-type1.json"},
		{"whole_market_snapshot_type4", "allsymbols-type4.json"},
	} {
		path := filepath.Join(rec, "brsapi-probe", f.name)
		body, err := os.ReadFile(path)
		if err != nil {
			if f.key == "whole_market_snapshot_type1" {
				fmt.Fprintln(os.Stderr, "snapshot:", err)
				os.Exit(1)
			}
			continue
		}
		r.Inputs[f.key] = path
		var part []snapshotRow
		if err := json.Unmarshal(body, &part); err != nil {
			fmt.Fprintln(os.Stderr, "snapshot parse:", err)
			os.Exit(1)
		}
		rows = append(rows, part...)
	}
	master, industry := canonical.NewMaster(), canonical.NewIndustryTable()
	st := securityMasterStat{SnapshotTradeDate: snapDate, ByClass: map[string]int{}}
	ind := industryStat{ValidFrom: snapDate}
	companies, industries := map[string]int{}, map[string]bool{}
	listed := map[string]bool{}
	var rights []string

	for _, row := range rows {
		insCode := row.ID.String()
		if insCode == "" || insCode == "0" {
			st.Ambiguities = append(st.Ambiguities,
				fmt.Sprintf("ticker %q has no instrument code and cannot be given a canonical identity", row.L18))
			continue
		}
		in := canonical.Instrument{
			InstrumentID: "tsetmc:" + insCode,
			ProviderIDs:  map[string]string{"brsapi": insCode},
			InsCode:      insCode, ISIN: row.ISIN, Ticker: row.L18, Name: row.L30,
			IndustryID: row.CSID.String(), ValidFrom: snapDate,
			ListingDate: "", Status: canonical.StatusListed,
			IdentitySource: "brsapi AllSymbols type=1 snapshot", Confidence: canonical.ConfidenceObserved,
		}
		if p, err := canonical.ParseISIN(row.ISIN); err == nil {
			st.WithISIN++
			in.CompanyID, in.InstrumentClass = p.CompanyCode, p.Class
			in.Board, in.Market = p.BoardDigit, "IR"
			companies[p.CompanyCode]++
			if p.Class == canonical.ClassRightsIssue {
				rights = append(rights, p.CompanyCode)
			}
		} else {
			in.InstrumentClass = canonical.ClassOtherClass
			in.Confidence = canonical.ConfidenceAssumed
			in.Ambiguities = append(in.Ambiguities, "ISIN not parsable: class, board and company link unknown")
			st.Ambiguities = append(st.Ambiguities,
				fmt.Sprintf("instrument %s has an unparsable ISIN %q", insCode, row.ISIN))
		}
		// listing_date and delisting_date are not in this feed. Leaving them
		// empty is deliberate: a guessed listing date would silently define
		// the point-in-time universe.
		in.Ambiguities = append(in.Ambiguities, "listing_date unknown: snapshot carries no listing history")
		st.ByClass[in.InstrumentClass]++
		if f, _ := row.TMin.Float64(); f > 0 {
			st.WithPriceLimits++
		}
		if f, _ := row.Z.Float64(); f > 0 {
			st.WithSharesOutstand++
		}
		if err := master.Add(in); err != nil {
			fmt.Fprintln(os.Stderr, "master:", err)
			os.Exit(1)
		}
		listed[row.L18] = true
		if id := row.CSID.String(); id != "" && id != "0" {
			industries[id] = true
			if err := industry.Add(canonical.IndustryMembership{
				InstrumentID: in.InstrumentID, IndustryID: id, IndustryName: row.CS,
				ValidFrom: snapDate, Source: "brsapi AllSymbols type=1 snapshot",
				Confidence: canonical.ConfidenceObserved,
			}); err != nil {
				fmt.Fprintln(os.Stderr, "industry:", err)
				os.Exit(1)
			}
		}
	}
	st.Instruments = master.Len()
	for _, n := range companies {
		if n > 1 {
			st.CompaniesMultiInst++
		}
	}
	for _, c := range rights {
		if len(master.CompanyGroup(c, snapDate)) > 1 {
			st.RightsLinkedParent++
		}
	}
	st.TickerCollisions = len(master.TickerCollisions(snapDate))

	ids := master.IDs()
	ind.Industries = len(industries)
	ind.InstrumentsWithDated = industry.Len()
	_, ind.CoverageAtSnapshot = industry.KnownCoverage(ids, snapDate)
	_, ind.CoverageBefore = industry.KnownCoverage(ids, "2026-01-05")
	return master, industry, st, ind, listed
}

func buildInstrument(rec string, master *canonical.Master, r *report) ([]canonical.DailyBar, instrumentStat, *canonical.TradingCalendar) {
	t0p := filepath.Join(rec, "brsapi-probe", "history-folad.json")
	t1p := filepath.Join(rec, "brsapi-probe", "history-folad-type1.json")
	r.Inputs["instrument_history_type0"] = t0p
	r.Inputs["instrument_history_type1"] = t1p
	t0, err := os.ReadFile(t0p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "history:", err)
		os.Exit(1)
	}
	t1, _ := os.ReadFile(t1p)

	// Foolad's instrument code, resolved through the security master by ticker
	// on the snapshot date rather than hard-coded.
	id, ticker := "unknown", "فولاد"
	if ids := master.ResolveTicker(ticker, r.SecurityMaster.SnapshotTradeDate); len(ids) == 1 {
		id = ids[0]
	}
	bars, problems, err := canonical.NormalizeBrsAPIHistory(id, "brsapi", t0, t1)
	if err != nil {
		fmt.Fprintln(os.Stderr, "normalize:", err)
		os.Exit(1)
	}
	canonical.ClassifyStates(bars)

	// One instrument cannot establish a market-wide calendar. A traded day is
	// proof the market was open; a zero-volume day proves only that THIS
	// instrument did not trade (it was halted), which says nothing about the
	// market, so it is left undetermined rather than called a closure.
	cal := canonical.NewTradingCalendar()
	for _, b := range bars {
		if b.Volume > 0 {
			cal.ObserveTrading(b.TradeDate, "brsapi:"+ticker, 1)
		}
	}
	q := canonical.CheckBars(bars, master, cal)

	st := instrumentStat{InstrumentID: id, Ticker: ticker, Rows: len(bars),
		StateCounts: map[string]int{}, QualityCounts: q.Counts, NormalizeIssues: problems}
	for _, b := range bars {
		st.StateCounts[b.TradingState]++
		if b.Volume > 0 {
			st.TradedDays++
		} else {
			st.ZeroVolumeDays++
		}
		if canonical.IsWeekendByRule(b.TradeDate) {
			st.RowsOnThuFri++
			if b.Volume > 0 {
				st.TradedOnThuFri++
			}
		}
	}
	if len(bars) > 0 {
		st.Earliest, st.Latest = bars[0].TradeDate, bars[len(bars)-1].TradeDate
		st.YearsSpanned = yearsBetween(st.Earliest, st.Latest)
	}
	return bars, st, cal
}

func calendarStatOf(cal *canonical.TradingCalendar) calendarStat {
	return calendarStat{
		DatesRecorded: len(cal.Days()), StatusCounts: cal.CountsByStatus(),
		TradingDays: len(cal.TradingDates()), UnverifiedShut: len(cal.UnverifiedClosures()),
		Note: "built from ONE instrument's traded days. A traded day proves the market was open; " +
			"a zero-volume day proves only that this instrument was halted, so it is left " +
			"undetermined rather than treated as a closure. A whole-market load is required " +
			"before any date can be called a closure, and closures stay unverified until an " +
			"official source confirms them.",
	}
}

func runBenchmark(rec string, a []canonical.DailyBar, r *report) any {
	p := filepath.Join(rec, "vendor-probe", "sa-history-folad.json")
	body, err := os.ReadFile(p)
	if err != nil {
		return map[string]string{"status": "no second provider recording available"}
	}
	r.Inputs["second_provider_history"] = p
	id := r.Instrument.InstrumentID
	b, problems, err := canonical.NormalizeSourceArenaHistory(id, "sourcearena", body)
	if err != nil {
		return map[string]string{"status": "second provider body unparsable: " + err.Error()}
	}
	if len(problems) > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("second provider normalize problems: %d", len(problems)))
	}
	return canonical.Benchmark(id, "brsapi", "sourcearena", a, b)
}

// survivorship compares the symbols that actually traded in the 2026
// tablokhani archive against the currently-listed universe. A symbol present
// in the archive but absent from today's list is a name a present-day universe
// would silently drop.
func survivorship(archiveRoot string, listed map[string]bool) survivorshipStat {
	st := survivorshipStat{CurrentlyListed: len(listed),
		Note: "archive symbols are Persian tickers, so a match is by label, not identity: " +
			"a renamed instrument counts as absent. This is an UPPER bound on delistings and a " +
			"LOWER bound on identity work still required."}
	seen := map[string]bool{}
	for _, ds := range []string{"hot_money_matrix", "symbol_score_history"} {
		dir := filepath.Join(archiveRoot, ds)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, s := range extractSymbols(body) {
				seen[s] = true
			}
		}
	}
	st.ArchiveSymbols = len(seen)
	var absent []string
	for s := range seen {
		if !listed[s] {
			absent = append(absent, s)
		}
	}
	sort.Strings(absent)
	st.AbsentFromCurrentList = len(absent)
	// A ticker ending in the rights-issue marker is a temporary instrument
	// whose subscription window closed; its absence is expected.
	const rightsSuffix = "ح"
	for _, s := range absent {
		if strings.HasSuffix(s, rightsSuffix) {
			st.AbsentRightsIssue++
			if len(st.ExamplesRights) < 8 {
				st.ExamplesRights = append(st.ExamplesRights, s)
			}
			continue
		}
		st.AbsentNonRights++
		if len(st.ExamplesNonRights) < 12 {
			st.ExamplesNonRights = append(st.ExamplesNonRights, s)
		}
	}
	for s := range listed {
		if !seen[s] {
			st.ListedNotInArchive++
		}
	}
	if st.ArchiveSymbols > 0 {
		st.ExposureRatio = ratio4(len(absent), st.ArchiveSymbols)
		st.ExposureExclRights = ratio4(st.AbsentNonRights, st.ArchiveSymbols)
	}
	return st
}

func ratio4(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(int(float64(a)/float64(b)*10000+0.5)) / 10000
}

// extractSymbols pulls the "symbol" values out of either archive shape (an
// object with data[] or a bare array) without binding to a full schema.
func extractSymbols(body []byte) []string {
	var asObj struct {
		Data []struct {
			Symbol string `json:"symbol"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &asObj) == nil && len(asObj.Data) > 0 {
		out := make([]string, 0, len(asObj.Data))
		for _, r := range asObj.Data {
			if r.Symbol != "" {
				out = append(out, r.Symbol)
			}
		}
		return out
	}
	var asArr []struct {
		Symbol string `json:"symbol"`
	}
	if json.Unmarshal(body, &asArr) == nil {
		out := make([]string, 0, len(asArr))
		for _, r := range asArr {
			if r.Symbol != "" {
				out = append(out, r.Symbol)
			}
		}
		return out
	}
	return nil
}

func yearsBetween(a, b string) float64 {
	ta, e1 := parseISO(a)
	tb, e2 := parseISO(b)
	if e1 != nil || e2 != nil {
		return 0
	}
	y := tb.Sub(ta).Hours() / 24 / 365.25
	return float64(int(y*100+0.5)) / 100
}

func printSummary(r report, out string) {
	fmt.Printf("security master: %d instruments, %d with parsable ISIN, %d ticker collisions on %s\n",
		r.SecurityMaster.Instruments, r.SecurityMaster.WithISIN, r.SecurityMaster.TickerCollisions,
		r.SecurityMaster.SnapshotTradeDate)
	fmt.Printf("  classes: %v\n", r.SecurityMaster.ByClass)
	fmt.Printf("  companies with >1 instrument: %d; rights issues linked to a parent: %d\n",
		r.SecurityMaster.CompaniesMultiInst, r.SecurityMaster.RightsLinkedParent)
	fmt.Printf("  allowed-range limits: %d; shares outstanding: %d\n",
		r.SecurityMaster.WithPriceLimits, r.SecurityMaster.WithSharesOutstand)
	fmt.Printf("industry: %d industries, dated from %s; coverage at snapshot %.4f, before snapshot %.4f\n",
		r.Industry.Industries, r.Industry.ValidFrom, r.Industry.CoverageAtSnapshot, r.Industry.CoverageBefore)
	fmt.Printf("instrument %s (%s): %d rows %s..%s (%.2f years), traded %d, zero-volume %d\n",
		r.Instrument.Ticker, r.Instrument.InstrumentID, r.Instrument.Rows,
		r.Instrument.Earliest, r.Instrument.Latest, r.Instrument.YearsSpanned,
		r.Instrument.TradedDays, r.Instrument.ZeroVolumeDays)
	fmt.Printf("  states: %v\n  quality: %v\n", r.Instrument.StateCounts, r.Instrument.QualityCounts)
	fmt.Printf("  rows dated Thu/Fri: %d (traded: %d)\n", r.Instrument.RowsOnThuFri, r.Instrument.TradedOnThuFri)
	fmt.Printf("calendar: %d dates, %d trading days, %d unverified possible closures %v\n",
		r.Calendar.DatesRecorded, r.Calendar.TradingDays, r.Calendar.UnverifiedShut, r.Calendar.StatusCounts)
	fmt.Printf("survivorship: %d archive symbols vs %d listed; %d absent (upper bound %.4f)\n",
		r.Survivorship.ArchiveSymbols, r.Survivorship.CurrentlyListed,
		r.Survivorship.AbsentFromCurrentList, r.Survivorship.ExposureRatio)
	fmt.Printf("  of those: %d expired rights issues, %d other (exposure excl. rights %.4f); %d listed names never in the archive\n",
		r.Survivorship.AbsentRightsIssue, r.Survivorship.AbsentNonRights,
		r.Survivorship.ExposureExclRights, r.Survivorship.ListedNotInArchive)
	if b, ok := r.Benchmark.(canonical.BenchmarkResult); ok {
		fmt.Printf("benchmark %s vs %s: overlap %d, onlyA %d, onlyB %d\n",
			b.ProviderA, b.ProviderB, b.OverlapDates, b.OnlyInA, b.OnlyInB)
		for _, f := range b.Fields {
			if f.Compared > 0 {
				fmt.Printf("  %-18s compared %3d exact %.4f meanRel %.6f bias %s\n",
					f.Field, f.Compared, f.ExactMatchRate, f.MeanRelError, f.BiasDirection)
			} else {
				fmt.Printf("  %-18s not comparable (missingA %d missingB %d)\n", f.Field, f.MissingA, f.MissingB)
			}
		}
	}
	fmt.Println("report:", out)
}
