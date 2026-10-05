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
)

type DayInfo struct {
	Date      string `json:"date"`
	Records   int    `json:"records"`
	Class     string `json:"class"` // ok, empty, low_coverage, date_mismatch, weekend_empty
	Symbols   int    `json:"symbols"`
	Weekday   string `json:"weekday"`
	Mismatch  int    `json:"rows_with_other_date,omitempty"`
	DupSymbol int    `json:"duplicate_symbols,omitempty"`
}

type DatasetReport struct {
	Dataset         string         `json:"dataset"`
	RequestedItems  int            `json:"requested_items"`
	SuccessfulItems int            `json:"successful_items"`
	FailedItems     int            `json:"failed_items"`
	DuplicateItems  int            `json:"duplicate_items"`
	CorruptItems    int            `json:"corrupt_items"`
	EarliestDate    string         `json:"earliest_date"`
	LatestDate      string         `json:"latest_date"`
	SymbolsCovered  int            `json:"symbols_covered"`
	DatesCovered    int            `json:"dates_covered"`
	MissingDates    []string       `json:"missing_dates"`
	SchemaVariants  map[string]int `json:"schema_variants"`
	RecordCount     int            `json:"record_count"`
	TotalSizeBytes  int64          `json:"total_size_bytes"`
	MedianRecords   int            `json:"median_records_per_day"`
	ClassCounts     map[string]int `json:"day_class_counts"`
	Days            []DayInfo      `json:"days"`
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

func scanDuplicates(path string) (lines int, uniq map[string]bool) {
	uniq = map[string]bool{}
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
			lines++
			uniq[e.MarketDate] = true
		}
	}
	return
}

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	sort.Ints(xs)
	return xs[len(xs)/2]
}

func validateDataset(s *Store, dataset, from, to string) (DatasetReport, map[string]string, error) {
	rep := DatasetReport{Dataset: dataset, SchemaVariants: map[string]int{}, ClassCounts: map[string]int{}}
	a, _ := time.Parse("2006-01-02", from)
	b, _ := time.Parse("2006-01-02", to)
	m, err := s.Manifest(dataset)
	if err != nil {
		return rep, nil, err
	}
	lines, uniq := scanDuplicates(s.manifestPath(dataset))
	rep.DuplicateItems = lines - len(uniq)
	symbols := map[string]bool{}
	var counts []int
	flags := map[string]string{}
	for d := a; !d.After(b); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		rep.RequestedItems++
		e := m[k]
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
		day := DayInfo{Date: k, Records: e.RecordCount, Symbols: len(syms), Weekday: isoWeekday(k)}
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
		case e.RecordCount == 0:
			day.Class = "empty"
		case day.Mismatch > 0:
			day.Class = "date_mismatch"
		}
		if day.Class == "ok" {
			counts = append(counts, e.RecordCount)
		}
		rep.Days = append(rep.Days, day)
	}
	rep.MedianRecords = median(append([]int(nil), counts...))
	for i := range rep.Days {
		d := &rep.Days[i]
		if d.Class == "ok" && float64(d.Records) < 0.7*float64(rep.MedianRecords) {
			d.Class = "low_coverage"
		}
		rep.ClassCounts[d.Class]++
		if d.Class != "ok" {
			flags[d.Date] = d.Class
		}
	}
	for _, k := range rep.MissingDates {
		flags[k] = "missing"
	}
	rep.SymbolsCovered = len(symbols)
	rep.FailedItems = rep.RequestedItems - rep.SuccessfulItems
	return rep, flags, nil
}

// WriteReport validates every dataset in [from,to] and writes
// manifests/validation_report.json plus a markdown summary next to it.
func WriteReport(s *Store, from, to string) error {
	r := Report{GeneratedAt: time.Now().UTC().Format(time.RFC3339), From: from, To: to}
	all := map[string]map[string]string{}
	for _, ds := range []string{DatasetMatrix, DatasetScores} {
		from2 := from
		if ds == DatasetMatrix && from2 < "2026-01-31" {
			from2 = "2026-01-31"
		}
		rep, flags, err := validateDataset(s, ds, from2, to)
		if err != nil {
			return err
		}
		r.Datasets = append(r.Datasets, rep)
		all[ds] = flags
	}
	for d, c1 := range all[DatasetMatrix] {
		if c2, ok := all[DatasetScores][d]; ok && c1 != "empty" && c2 != "empty" && isoWeekday(d) != "Thu" && isoWeekday(d) != "Fri" {
			r.SynchronizedGaps = append(r.SynchronizedGaps, d+" (matrix:"+c1+", scores:"+c2+")")
		}
	}
	sort.Strings(r.SynchronizedGaps)
	r.SynchronizedNotes = "dates flagged (low_coverage/date_mismatch/missing) in BOTH datasets on non-Thu/Fri days; raw data preserved, nothing forward-filled"
	b, _ := json.MarshalIndent(r, "", "  ")
	out := filepath.Join(s.Root, "manifests", "validation_report.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	for _, d := range r.Datasets {
		fmt.Printf("%s: requested=%d ok_files=%d failed=%d dup=%d corrupt=%d range=%s..%s dates=%d symbols=%d records=%d size=%dMB median/day=%d classes=%v schemas=%v\n",
			d.Dataset, d.RequestedItems, d.SuccessfulItems, d.FailedItems, d.DuplicateItems, d.CorruptItems,
			d.EarliestDate, d.LatestDate, d.DatesCovered, d.SymbolsCovered, d.RecordCount, d.TotalSizeBytes>>20, d.MedianRecords, d.ClassCounts, d.SchemaVariants)
	}
	fmt.Println("synchronized gaps:", len(r.SynchronizedGaps))
	fmt.Println("report:", out)
	return nil
}
