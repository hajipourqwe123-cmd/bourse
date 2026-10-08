package tkarchive

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bourse/internal/calendar"
)

// Temporal status of a dataset: whether the time at which a value was knowable is
// established. TEMPORALLY_AMBIGUOUS data must not enter a backtest (look-ahead risk).
const (
	TemporalIntervalStamped = "INTERVAL_STAMPED"
	TemporalAmbiguous       = "TEMPORALLY_AMBIGUOUS"
)

// datasetPolicy is the per-dataset judgement recorded in docs/archive_feasibility.md.
var datasetPolicy = map[string]struct{ Temporal, BacktestUse string }{
	DatasetMatrix: {TemporalIntervalStamped, "exploratory only (category B): no price/volume levels, no executable price"},
	DatasetScores: {TemporalAmbiguous, "prohibited until score_date/created_at is aligned with price/volume data (category B)"},
}

type DayInfo struct {
	Date         string `json:"date"`
	Records      int    `json:"records"`
	Class        string `json:"class"` // ok, empty, low_coverage, partial_interval_grid, date_mismatch, schema_invalid, or a non-closed completeness state
	Completeness string `json:"completeness"`
	SchemaValid  bool   `json:"schema_valid"`
	Symbols      int    `json:"symbols"`
	Weekday      string `json:"weekday"`
	Mismatch     int    `json:"rows_with_other_date,omitempty"`
	DupSymbol    int    `json:"duplicate_symbols,omitempty"`
	Intervals    int    `json:"intervals,omitempty"`      // matrix: interval labels served for the day
	GridRef      int    `json:"grid_reference,omitempty"` // matrix: median interval count of genuine days within ±15 days
}

type Coverage struct {
	LogicalCoverage        float64 `json:"logical_coverage"`         // checksum-valid items / requested calendar days
	HistoricalCoverage     float64 `json:"historical_coverage"`      // genuine day observations / expected trading days
	SchemaValidCoverage    float64 `json:"schema_valid_coverage"`    // genuine and structurally valid / expected trading days
	UsableBacktestCoverage float64 `json:"usable_backtest_coverage"` // passes every gate and temporal status allows backtests
}

type DatasetReport struct {
	Dataset             string         `json:"dataset"`
	TemporalStatus      string         `json:"temporal_status"`
	BacktestUse         string         `json:"backtest_use"`
	RequestedItems      int            `json:"requested_items"`
	SuccessfulItems     int            `json:"successful_items"`
	FailedItems         int            `json:"failed_items"`
	DuplicateItems      int            `json:"duplicate_items"`
	CorruptItems        int            `json:"corrupt_items"`
	ExpectedTradingDays int            `json:"expected_trading_days"` // Sat-Wed; official holidays not subtracted (calendar list unverified/empty)
	GenuineDays         int            `json:"genuine_days"`
	SchemaValidDays     int            `json:"schema_valid_days"`
	UsableDays          int            `json:"usable_backtest_days"`
	Coverage            Coverage       `json:"coverage"`
	EarliestDate        string         `json:"earliest_date"`
	LatestDate          string         `json:"latest_date"`
	SymbolsCovered      int            `json:"symbols_covered"`
	DatesCovered        int            `json:"dates_covered"`
	MissingDates        []string       `json:"missing_dates"`
	DateGaps            []string       `json:"trading_date_gaps"` // expected trading days without a genuine observation
	EmptyDays           []string       `json:"empty_days"`        // zero-record responses
	SchemaVariants      map[string]int `json:"schema_variants"`
	RecordCount         int            `json:"record_count"`
	TotalSizeBytes      int64          `json:"total_size_bytes"`
	MedianRecords       int            `json:"median_records_per_day"`
	ClassCounts         map[string]int `json:"day_class_counts"`
	Days                []DayInfo      `json:"days"`
}

type Report struct {
	GeneratedAt       string          `json:"generated_at"`
	From              string          `json:"from"`
	To                string          `json:"to"`
	Datasets          []DatasetReport `json:"datasets"`
	SynchronizedGaps  []string        `json:"synchronized_gap_dates"`
	SynchronizedNotes string          `json:"note"`
}

func isoWeekday(d string) string {
	t, _ := time.Parse("2006-01-02", d)
	return t.Weekday().String()[:3]
}

func tradingWeekday(d string) bool { w := isoWeekday(d); return w != "Thu" && w != "Fri" }

// scanDuplicates counts keys archived as more than one distinct raw file.
// Annotation lines repeat the same file and are not duplicates.
func scanDuplicates(path string) (dups int) {
	files := map[string]map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			if files[e.MarketDate] == nil {
				files[e.MarketDate] = map[string]bool{}
			}
			files[e.MarketDate][e.SHA256] = true
		}
	}
	for _, s := range files {
		dups += len(s) - 1
	}
	return
}

// schemaValid checks the structure the dataset contract relies on.
func schemaValid(dataset string, body []byte) bool {
	switch dataset {
	case DatasetMatrix:
		var v struct {
			Success   bool                         `json:"success"`
			Date      string                       `json:"date"`
			Intervals []string                     `json:"intervals"`
			Data      []map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &v) != nil || !v.Success || v.Date == "" || (len(v.Data) > 0 && len(v.Intervals) == 0) {
			return false
		}
		for _, r := range v.Data {
			for _, f := range []string{"symbol", "hotMoneyValues", "priceChangeValues", "priceLevelValues"} {
				if _, ok := r[f]; !ok {
					return false
				}
			}
		}
		return true
	case DatasetScores:
		var rows []map[string]json.RawMessage
		if json.Unmarshal(body, &rows) != nil {
			return false
		}
		for _, r := range rows {
			for _, f := range []string{"symbol", "score_date", "total_score", "created_at"} {
				if _, ok := r[f]; !ok {
					return false
				}
			}
		}
		return true
	}
	return false
}

func intervalCount(body []byte) int {
	var v struct {
		Intervals []string `json:"intervals"`
	}
	json.Unmarshal(body, &v)
	return len(v.Intervals)
}

// markPartialGrids flags matrix days with fewer intervals than the provider served
// on neighbouring genuine days. The reference is local because the provider's grid
// changed (20-21 labels to 12:30, then 36 to 15:00); no fixed count means "complete".
func markPartialGrids(days []DayInfo) {
	var g []*DayInfo
	for i := range days {
		if days[i].Class == "ok" || days[i].Class == "low_coverage" {
			g = append(g, &days[i])
		}
	}
	for _, d := range g {
		t, _ := time.Parse("2006-01-02", d.Date)
		var near []int
		for _, o := range g {
			u, _ := time.Parse("2006-01-02", o.Date)
			if o != d && u.Sub(t) <= 15*24*time.Hour && t.Sub(u) <= 15*24*time.Hour {
				near = append(near, o.Intervals)
			}
		}
		d.GridRef = median(near)
		if d.Class == "ok" && d.Intervals < d.GridRef {
			d.Class = "partial_interval_grid"
		}
	}
}

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	sort.Ints(xs)
	return xs[len(xs)/2]
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(int(float64(a)/float64(b)*10000+0.5)) / 10000
}

// pick returns the entry that represents day k: the base capture, or a verified
// recapture when only the recapture is session-complete.
func pick(s *Store, cal *calendar.Calendar, m map[string]*Entry, k string) *Entry {
	e := m[k]
	if r := m[k+"@recapture"]; r != nil && s.Verify(r) == nil && Closed(EntryCompleteness(cal, r)) &&
		(e == nil || !Closed(EntryCompleteness(cal, e))) {
		return r
	}
	return e
}

func validateDataset(s *Store, cal *calendar.Calendar, dataset, from, to string) (DatasetReport, map[string]string, error) {
	pol := datasetPolicy[dataset]
	rep := DatasetReport{Dataset: dataset, TemporalStatus: pol.Temporal, BacktestUse: pol.BacktestUse,
		SchemaVariants: map[string]int{}, ClassCounts: map[string]int{}}
	a, _ := time.Parse("2006-01-02", from)
	b, _ := time.Parse("2006-01-02", to)
	m, err := s.Manifest(dataset)
	if err != nil {
		return rep, nil, err
	}
	rep.DuplicateItems = scanDuplicates(s.manifestPath(dataset))
	symbols := map[string]bool{}
	var counts []int
	flags := map[string]string{}
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		rep.RequestedItems++
		if tradingWeekday(k) {
			rep.ExpectedTradingDays++
		}
		e := pick(s, cal, m, k)
		if e == nil {
			rep.MissingDates = append(rep.MissingDates, k)
			continue
		}
		if err := s.Verify(e); err != nil {
			rep.CorruptItems++
			rep.MissingDates = append(rep.MissingDates, k)
			continue
		}
		rep.SuccessfulItems++
		rep.DatesCovered++
		rep.RecordCount += e.RecordCount
		rep.TotalSizeBytes += int64(e.ContentLength)
		rep.SchemaVariants[e.SchemaVersion]++
		if rep.EarliestDate == "" || k < rep.EarliestDate {
			rep.EarliestDate = k
		}
		if k > rep.LatestDate {
			rep.LatestDate = k
		}
		body, _ := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(e.File)))
		_, _, _, syms, _ := Analyze(body)
		day := DayInfo{Date: k, Records: e.RecordCount, Symbols: len(syms), Weekday: isoWeekday(k),
			Completeness: EntryCompleteness(cal, e), SchemaValid: schemaValid(dataset, body)}
		if dataset == DatasetMatrix {
			day.Intervals = intervalCount(body)
		}
		seen := map[string]bool{}
		for _, sy := range syms {
			symbols[sy] = true
			if seen[sy] {
				day.DupSymbol++
			}
			seen[sy] = true
		}
		if dataset == DatasetScores {
			var rows []map[string]any
			if json.Unmarshal(body, &rows) == nil {
				for _, r := range rows {
					if sd, _ := r["score_date"].(string); !strings.HasPrefix(sd, k) {
						day.Mismatch++
					}
				}
			}
		}
		if e.PayloadDate != "" && e.PayloadDate != k {
			day.Mismatch = e.RecordCount
		}
		day.Class = "ok"
		switch {
		case !Closed(day.Completeness):
			day.Class = strings.ToLower(day.Completeness)
		case e.RecordCount == 0:
			day.Class = "empty"
			rep.EmptyDays = append(rep.EmptyDays, k)
		case day.Mismatch > 0:
			day.Class = "date_mismatch"
		case !day.SchemaValid:
			day.Class = "schema_invalid"
		}
		if day.Class == "ok" {
			counts = append(counts, e.RecordCount)
		}
		rep.Days = append(rep.Days, day)
	}
	rep.MedianRecords = median(append([]int(nil), counts...))
	for i := range rep.Days {
		if d := &rep.Days[i]; d.Class == "ok" && float64(d.Records) < 0.7*float64(rep.MedianRecords) {
			d.Class = "low_coverage"
		}
	}
	if dataset == DatasetMatrix {
		markPartialGrids(rep.Days)
	}
	genuine := map[string]bool{}
	for i := range rep.Days {
		d := &rep.Days[i]
		rep.ClassCounts[d.Class]++
		if d.Class != "ok" {
			flags[d.Date] = d.Class
		}
		// genuine: a whole-day observation of the requested day with data
		if (d.Class == "ok" || d.Class == "low_coverage" || d.Class == "partial_interval_grid" || d.Class == "schema_invalid") && tradingWeekday(d.Date) {
			genuine[d.Date] = true
			rep.GenuineDays++
			if d.SchemaValid {
				rep.SchemaValidDays++
			}
			if d.Class == "ok" && pol.Temporal != TemporalAmbiguous {
				rep.UsableDays++
			}
		}
	}
	for _, k := range rep.MissingDates {
		flags[k] = "missing"
	}
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		if k := d.Format("2006-01-02"); tradingWeekday(k) && !genuine[k] {
			rep.DateGaps = append(rep.DateGaps, k)
		}
	}
	rep.Coverage = Coverage{
		LogicalCoverage:        ratio(rep.SuccessfulItems, rep.RequestedItems),
		HistoricalCoverage:     ratio(rep.GenuineDays, rep.ExpectedTradingDays),
		SchemaValidCoverage:    ratio(rep.SchemaValidDays, rep.ExpectedTradingDays),
		UsableBacktestCoverage: ratio(rep.UsableDays, rep.ExpectedTradingDays),
	}
	rep.SymbolsCovered = len(symbols)
	rep.FailedItems = rep.RequestedItems - rep.SuccessfulItems
	return rep, flags, nil
}

// WriteReport validates every dataset in [from,to] and writes
// manifests/validation_report.json and manifests/validation_report.md.
func WriteReport(s *Store, from, to string) error {
	cal := calendar.Default()
	r := Report{GeneratedAt: time.Now().UTC().Format(time.RFC3339), From: from, To: to}
	all := map[string]map[string]string{}
	for _, ds := range []string{DatasetMatrix, DatasetScores} {
		from2 := from
		if ds == DatasetMatrix && from2 < "2026-01-31" {
			from2 = "2026-01-31"
		}
		rep, flags, err := validateDataset(s, cal, ds, from2, to)
		if err != nil {
			return err
		}
		r.Datasets = append(r.Datasets, rep)
		all[ds] = flags
	}
	for d, c1 := range all[DatasetMatrix] {
		if c2, ok := all[DatasetScores][d]; ok && tradingWeekday(d) {
			r.SynchronizedGaps = append(r.SynchronizedGaps, d+" (matrix:"+c1+", scores:"+c2+")")
		}
	}
	sort.Strings(r.SynchronizedGaps)
	r.SynchronizedNotes = "Sat-Wed dates flagged in BOTH datasets. No data in either (matrix date_mismatch + scores empty) is consistent with a market closure but the holiday calendar is unverified; scores present while the matrix is missing or thin is a provider-side degradation. Raw data preserved, nothing forward-filled"
	b, _ := json.MarshalIndent(r, "", "  ")
	out := filepath.Join(s.Root, "manifests", "validation_report.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(strings.TrimSuffix(out, ".json")+".md", []byte(markdown(r)), 0o644); err != nil {
		return err
	}
	for _, d := range r.Datasets {
		fmt.Printf("%s: requested=%d ok_files=%d failed=%d dup=%d corrupt=%d range=%s..%s symbols=%d records=%d size=%dMB coverage=%+v classes=%v\n",
			d.Dataset, d.RequestedItems, d.SuccessfulItems, d.FailedItems, d.DuplicateItems, d.CorruptItems,
			d.EarliestDate, d.LatestDate, d.SymbolsCovered, d.RecordCount, d.TotalSizeBytes>>20, d.Coverage, d.ClassCounts)
	}
	fmt.Println("synchronized gaps:", len(r.SynchronizedGaps))
	fmt.Println("report:", out)
	return nil
}

func markdown(r Report) string {
	var w strings.Builder
	fmt.Fprintf(&w, "# Tablokhani archive validation (%s .. %s)\n\nGenerated %s. Raw data is never modified, forward-filled or fabricated.\n\n", r.From, r.To, r.GeneratedAt)
	for _, d := range r.Datasets {
		fmt.Fprintf(&w, "## %s\n\n| Item | Value |\n| --- | --- |\n", d.Dataset)
		row := func(k string, v any) { fmt.Fprintf(&w, "| %s | %v |\n", k, v) }
		row("temporal_status", d.TemporalStatus)
		row("backtest_use", d.BacktestUse)
		row("requested / successful / failed", fmt.Sprintf("%d / %d / %d", d.RequestedItems, d.SuccessfulItems, d.FailedItems))
		row("duplicates / corrupt", fmt.Sprintf("%d / %d", d.DuplicateItems, d.CorruptItems))
		row("earliest / latest", d.EarliestDate+" / "+d.LatestDate)
		row("expected trading days (Sat-Wed)", d.ExpectedTradingDays)
		row("genuine / schema-valid / usable days", fmt.Sprintf("%d / %d / %d", d.GenuineDays, d.SchemaValidDays, d.UsableDays))
		row("logical_coverage", d.Coverage.LogicalCoverage)
		row("historical_coverage", d.Coverage.HistoricalCoverage)
		row("schema_valid_coverage", d.Coverage.SchemaValidCoverage)
		row("usable_backtest_coverage", d.Coverage.UsableBacktestCoverage)
		row("symbols", d.SymbolsCovered)
		row("records", d.RecordCount)
		row("size (bytes)", d.TotalSizeBytes)
		row("median records per ok day", d.MedianRecords)
		row("day classes", d.ClassCounts)
		row("schema variants", d.SchemaVariants)
		row("empty days", len(d.EmptyDays))
		fmt.Fprintf(&w, "\nTrading-date gaps (%d): %s\n\n", len(d.DateGaps), strings.Join(d.DateGaps, ", "))
	}
	fmt.Fprintf(&w, "## Synchronized gaps (%d)\n\n%s\n\n%s\n", len(r.SynchronizedGaps), r.SynchronizedNotes, strings.Join(r.SynchronizedGaps, "\n"))
	return w.String()
}
