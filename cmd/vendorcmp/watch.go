package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"bourse/internal/tehran"
)

// watchConfig is -watch mode: LOCAL comparison while the collector runs. Every interval it reads
// the collector's latest SourceArena payload (SOURCEARENA_SAVE_LATEST: no SourceArena quota is
// spent) and fetches BrsApi once, at most maxBrs times per Tehran day, and only while the saved
// payload is fresh (the collector is polling). Each run rewrites the markdown report and appends
// one line to the NDJSON log; skips and errors are logged there too (the vendor scorecard counts
// outages from them). Neither file holds secrets or raw payloads.
type watchConfig struct {
	every    time.Duration
	maxBrs   int
	saFile   string
	fresh    time.Duration
	out, sum string
	// scorecard, rewritten after every round (owner request B)
	saBudget          string
	brsLimit, saLimit int
}

// runSummary is one NDJSON line of the watch log. Kind "run" is a comparison; "skip" and
// "error" carry only Reason (and the budget count).
type runSummary struct {
	Kind         string    `json:"kind"`
	At           time.Time `json:"at"` // BrsApi request START (run), or the event time
	Reason       string    `json:"reason,omitempty"`
	BrsUsedToday int       `json:"brsapi_used_today"`

	SaFileAt *time.Time `json:"sa_file_at,omitempty"`
	// FetchGapS = BrsApi request start − SourceArena payload write time: how far apart the two
	// FETCHES are, not how far behind either vendor's data is (neither sends a snapshot time).
	FetchGapS  float64 `json:"fetch_gap_s,omitempty"`
	BrsFetchMs int64   `json:"brs_fetch_ms,omitempty"`
	BrsDataAt  string  `json:"brs_data_at,omitempty"` // latest event time inside the BrsApi payload (time, no date)
	SaDataAt   string  `json:"sa_data_at,omitempty"`  // latest last-trade date/time inside the SourceArena payload

	BrsRows int `json:"brs_rows,omitempty"`
	SaRows  int `json:"sa_rows,omitempty"`
	Joined  int `json:"joined,omitempty"`
	OnlyBrs int `json:"only_brs,omitempty"`
	OnlySa  int `json:"only_sa,omitempty"`

	Mismatch map[string]int `json:"mismatch_by_group,omitempty"` // group → mismatching (row, field) cells
	// Static: day-constant fields that differ — a real disagreement. Counted only while both
	// vendors must be on the same trading day (staticWindow); otherwise StaticSkipped says why.
	Static        map[string]int `json:"static_mismatch,omitempty"`
	StaticSkipped string         `json:"static_skipped,omitempty"`
	// VolumeAhead: which vendor shows the larger day volume on a joined row; both_zero and missing
	// are separate so untraded rows do not inflate "equal".
	VolumeAhead map[string]int `json:"volume_ahead,omitempty"`
	// Per vendor, over ALL its rows: traded rows (volume > 0); traded rows whose individual +
	// institutional volume differs from the day volume on either side (SIDE_MISMATCH, the
	// engine's condition); and traded rows missing a side field (not a mismatch: missing data).
	Traded       map[string]int `json:"traded,omitempty"`
	SideMismatch map[string]int `json:"side_mismatch,omitempty"`
	SideMissing  map[string]int `json:"side_missing,omitempty"`
}

// staticFields do not change during a trading day (identity, yesterday's price, the permitted
// range): a mismatch there is not explained by the vendors' different polling moments.
var staticFields = map[string]bool{"symbol": true, "isin": true, "sector_code": true,
	"price_yesterday": true, "price_limit_min": true, "price_limit_max": true}

// staticWindow: both vendors have long rolled over to the day (the 2026-09-26 pre-open recording
// showed both on the new day by 08:23) and not yet to the next one.
func staticWindow(t time.Time) bool {
	h := t.In(tehran.Loc)
	m := h.Hour()*60 + h.Minute()
	return m >= 9*60 && m < 18*60
}

// budgetFile keeps the day's BrsApi request count across restarts (next to the summary log).
func (c watchConfig) budgetFile() string {
	return filepath.Join(filepath.Dir(c.sum), "brsapi-used.json")
}

// loadUsed returns the day's count; an unreadable or corrupt file counts as the budget spent
// (fail closed: the free plan's 100/day must not be exceeded).
func (c watchConfig) loadUsed(day string) int {
	b, err := os.ReadFile(c.budgetFile())
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	var st struct {
		Day  string `json:"day"`
		Used int    `json:"used"`
	}
	if err == nil {
		err = json.Unmarshal(b, &st)
	}
	if err != nil {
		log.Printf("vendorcmp: watch: BrsApi budget file unreadable (%v): counting today's budget as spent", err)
		c.saveUsed(day, c.maxBrs) // spent for THIS day only: the next day starts again
		return c.maxBrs
	}
	if st.Day != day {
		return 0
	}
	return st.Used
}

func (c watchConfig) saveUsed(day string, used int) {
	b, _ := json.Marshal(map[string]any{"day": day, "used": used})
	tmp := c.budgetFile() + ".tmp"
	err := os.MkdirAll(filepath.Dir(tmp), 0o755)
	if err == nil {
		err = os.WriteFile(tmp, b, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, c.budgetFile())
	}
	if err != nil {
		log.Printf("vendorcmp: watch: save BrsApi count: %v", err)
	}
}

func (c watchConfig) event(kind, reason string, used int) {
	log.Printf("vendorcmp: watch: %s: %s", kind, reason)
	c.appendLine(runSummary{Kind: kind, At: time.Now(), Reason: reason, BrsUsedToday: used})
}

func (c watchConfig) appendLine(s runSummary) {
	line, _ := json.Marshal(s)
	if err := os.MkdirAll(filepath.Dir(c.sum), 0o755); err != nil {
		log.Printf("vendorcmp: watch: %v", err)
		return
	}
	f, err := os.OpenFile(c.sum, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("vendorcmp: watch: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("vendorcmp: watch: %v", err)
	}
}

func watch(c watchConfig) {
	day, used := "", 0
	for {
		now := time.Now()
		if d := tehran.TradingDay(now); d != day {
			day, used = d, c.loadUsed(d)
			log.Printf("vendorcmp: watch: %s: %d BrsApi requests already used today", day, used)
		}
		st, err := os.Stat(c.saFile)
		switch {
		case err != nil:
			c.event("skip", "sourcearena_missing", used)
		case now.Sub(st.ModTime()) > c.fresh:
			c.event("skip", fmt.Sprintf("sourcearena_stale %s", now.Sub(st.ModTime()).Round(time.Second)), used)
		case used >= c.maxBrs:
			c.event("skip", fmt.Sprintf("brsapi_budget %d", c.maxBrs), used)
		default:
			used++ // an attempt costs quota whether or not it succeeds: counted before sending
			c.saveUsed(day, used)
			if err := once(c, used); err != nil {
				c.event("error", err.Error(), used)
			}
		}
		if _, err := writeScore(day, c.sum, c.brsLimit, c.saLimit, c.saBudget, c.every); err != nil {
			log.Printf("vendorcmp: watch: scorecard: %v", err)
		}
		time.Sleep(c.every)
	}
}

// readStable opens the SourceArena payload once and stats the same handle, so a concurrent
// rename by the collector cannot pair one file's time with another file's content.
func readStable(path string) ([]byte, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}
	b, err := io.ReadAll(f)
	return b, st.ModTime(), err
}

func once(c watchConfig, used int) error {
	start := time.Now()
	brs, _, err := load("", "BRSAPI_KEY", brsURL)
	took := time.Since(start)
	if err != nil {
		return fmt.Errorf("brsapi: %v", err)
	}
	sa, saAt, err := readStable(c.saFile)
	if err != nil {
		return fmt.Errorf("sourcearena payload: %v", err)
	}
	a, err := parseRows(brs)
	if err != nil {
		return fmt.Errorf("brsapi payload: %v", err)
	}
	b, err := parseRows(sa)
	if err != nil {
		return fmt.Errorf("sourcearena payload: %v", err)
	}
	rep := compare(a, b)
	gap := start.Sub(saAt).Seconds()
	rep.BrsAt = "دریافت زنده، " + start.In(tehran.Loc).Format("15:04:05") + " تهران"
	rep.SaAt = "آخرین پاسخ collector، " + saAt.In(tehran.Loc).Format("15:04:05") + " تهران"
	rep.Note = fmt.Sprintf("بازار باز: دو درخواست %.0f ثانیه فاصله دارند، پس اختلاف در جمع‌ها، قیمت‌ها و دفتر می‌تواند فقط از فاصله زمانی باشد. "+
		"فقط اختلاف در فیلدهای ثابت روز (شناسه، قیمت دیروز، دامنه مجاز) بین ۰۹:۰۰ و ۱۸:۰۰ ناهمخوانی واقعی است.", gap)
	if err := os.MkdirAll(filepath.Dir(c.out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(c.out, []byte(rep.Markdown()), 0o644); err != nil {
		return err
	}
	s := summarize(rep, a, b)
	s.Kind, s.At, s.SaFileAt, s.FetchGapS, s.BrsFetchMs, s.BrsUsedToday = "run", start, &saAt, gap, took.Milliseconds(), used
	if !staticWindow(start) {
		s.Static, s.StaticSkipped = nil, "outside 09:00–18:00: the vendors may be on different trading days"
	}
	c.appendLine(s)
	log.Printf("vendorcmp: watch: joined %d, static mismatches %v, volume ahead %v, side mismatch %v, fetch gap %.0fs, BrsApi %d/%d today",
		s.Joined, s.Static, s.VolumeAhead, s.SideMismatch, gap, used, c.maxBrs)
	return nil
}

// summarize reduces a report to counts (no values). brs and sa are all rows of each payload.
func summarize(rep *Report, brs, sa []row) runSummary {
	s := runSummary{BrsRows: rep.BrsRows, SaRows: rep.SaRows, Joined: len(rep.Pairs), OnlyBrs: len(rep.OnlyBrs),
		OnlySa: len(rep.OnlySa), BrsDataAt: rep.BrsDataAt, SaDataAt: rep.SaDataAt,
		Mismatch: map[string]int{}, Static: map[string]int{}, VolumeAhead: map[string]int{},
		Traded: map[string]int{}, SideMismatch: map[string]int{}, SideMissing: map[string]int{}}
	for _, f := range rep.Stats {
		if f.Mismatch == 0 {
			continue
		}
		s.Mismatch[f.Group] += f.Mismatch
		if staticFields[f.Name] {
			s.Static[f.Name] = f.Mismatch
		}
	}
	for _, p := range rep.Pairs {
		bv, ok1 := p.Brs.int("tvol")
		sv, ok2 := p.Sa.int("trade_volume")
		switch {
		case !ok1 || !ok2:
			s.VolumeAhead["missing"]++
		case bv == 0 && sv == 0:
			s.VolumeAhead["both_zero"]++
		case bv > sv:
			s.VolumeAhead["brsapi"]++
		case sv > bv:
			s.VolumeAhead["sourcearena"]++
		default:
			s.VolumeAhead["equal"]++
		}
	}
	side(s, "brsapi", brs, "tvol", "Buy_I_Volume", "Buy_N_Volume", "Sell_I_Volume", "Sell_N_Volume")
	side(s, "sourcearena", sa, "trade_volume", "real_buy_volume", "co_buy_volume", "real_sell_volume", "co_sell_volume")
	return s
}

// side counts, for one vendor, traded rows and those whose individual + institutional volume
// differs from the day volume on the buy or sell side (the engine's SIDE_MISMATCH condition).
func side(s runSummary, vendor string, rows []row, vol, ib, nb, is, ns string) {
	for _, r := range rows {
		v, ok := r.int(vol)
		if !ok || v <= 0 {
			continue
		}
		s.Traded[vendor]++
		a, ok1 := r.int(ib)
		b, ok2 := r.int(nb)
		c, ok3 := r.int(is)
		d, ok4 := r.int(ns)
		switch {
		case !ok1 || !ok2 || !ok3 || !ok4:
			s.SideMissing[vendor]++
		case a+b != v || c+d != v:
			s.SideMismatch[vendor]++
		}
	}
}
