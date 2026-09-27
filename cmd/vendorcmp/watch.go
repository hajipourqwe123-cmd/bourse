package main

import (
	"encoding/json"
	"fmt"
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
// one summary line to the NDJSON log. Neither holds secrets or raw payloads.
type watchConfig struct {
	every    time.Duration
	maxBrs   int
	saFile   string
	fresh    time.Duration
	out, sum string
}

// runSummary is one NDJSON line of the watch log.
type runSummary struct {
	At           time.Time      `json:"at"`
	SaFileAt     time.Time      `json:"sa_file_at"`
	SkewSeconds  float64        `json:"skew_s"` // BrsApi fetch time − SourceArena payload time (> 0: BrsApi is later)
	BrsRows      int            `json:"brs_rows"`
	SaRows       int            `json:"sa_rows"`
	Joined       int            `json:"joined"`
	OnlyBrs      int            `json:"only_brs"`
	OnlySa       int            `json:"only_sa"`
	Mismatch     map[string]int `json:"mismatch_by_group"` // group → mismatching (row, field) cells
	Static       map[string]int `json:"static_mismatch"`   // fields that must not change intraday: a real disagreement
	VolumeAhead  map[string]int `json:"volume_ahead"`      // which vendor shows the larger day volume on a joined row
	BrsUsedToday int            `json:"brsapi_used_today"`
	Note         string         `json:"note,omitempty"`
}

// staticFields do not change during a trading day (identity, yesterday's price, the permitted
// range): a mismatch there is not explained by the vendors' different polling moments.
var staticFields = map[string]bool{"symbol": true, "isin": true, "sector_code": true,
	"price_yesterday": true, "price_limit_min": true, "price_limit_max": true}

func watch(c watchConfig) {
	day, used := "", 0
	for {
		now := time.Now()
		if d := tehran.TradingDay(now); d != day {
			day, used = d, 0
		}
		st, err := os.Stat(c.saFile)
		switch {
		case err != nil:
			log.Printf("vendorcmp: watch: SourceArena payload %s: %v (collector not saving?); skipped", c.saFile, err)
		case now.Sub(st.ModTime()) > c.fresh:
			log.Printf("vendorcmp: watch: SourceArena payload is %s old (collector idle); skipped, no BrsApi request", now.Sub(st.ModTime()).Round(time.Second))
		case used >= c.maxBrs:
			log.Printf("vendorcmp: watch: BrsApi budget of %d requests for %s used; skipped until tomorrow", c.maxBrs, day)
		default:
			used++ // an attempt costs quota whether or not it succeeds
			if err := once(c, used); err != nil {
				log.Printf("vendorcmp: watch: %v", err)
			}
		}
		time.Sleep(c.every)
	}
}

func once(c watchConfig, used int) error {
	brs, _, err := load("", "BRSAPI_KEY", brsURL)
	brsAt := time.Now()
	if err != nil {
		return fmt.Errorf("brsapi: %v", err)
	}
	st, err := os.Stat(c.saFile)
	if err != nil {
		return err
	}
	sa, err := os.ReadFile(c.saFile)
	if err != nil {
		return err
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
	skew := brsAt.Sub(st.ModTime()).Seconds()
	rep.BrsAt = "دریافت زنده، " + brsAt.In(tehran.Loc).Format("15:04:05") + " تهران"
	rep.SaAt = "آخرین پاسخ collector، " + st.ModTime().In(tehran.Loc).Format("15:04:05") + " تهران"
	rep.Note = fmt.Sprintf("بازار باز: دو پاسخ %.0f ثانیه فاصله دارند، پس اختلاف در جمع‌ها، قیمت‌ها و دفتر می‌تواند فقط از فاصله زمانی باشد. "+
		"فقط اختلاف در فیلدهای ثابت روز (شناسه، قیمت دیروز، دامنه مجاز) ناهمخوانی واقعی است.", skew)
	if err := os.MkdirAll(filepath.Dir(c.out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(c.out, []byte(rep.Markdown()), 0o644); err != nil {
		return err
	}
	s := summarize(rep)
	s.At, s.SaFileAt, s.SkewSeconds, s.BrsUsedToday = brsAt, st.ModTime(), skew, used
	line, _ := json.Marshal(s)
	f, err := os.OpenFile(c.sum, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	log.Printf("vendorcmp: watch: joined %d, static mismatches %v, volume ahead %v, skew %.0fs, BrsApi %d/%d today",
		s.Joined, s.Static, s.VolumeAhead, skew, used, c.maxBrs)
	return err
}

// summarize reduces a report to counts (no values).
func summarize(rep *Report) runSummary {
	s := runSummary{BrsRows: rep.BrsRows, SaRows: rep.SaRows, Joined: len(rep.Pairs), OnlyBrs: len(rep.OnlyBrs),
		OnlySa: len(rep.OnlySa), Mismatch: map[string]int{}, Static: map[string]int{}, VolumeAhead: map[string]int{}}
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
		case bv > sv:
			s.VolumeAhead["brsapi"]++
		case sv > bv:
			s.VolumeAhead["sourcearena"]++
		default:
			s.VolumeAhead["equal"]++
		}
	}
	return s
}
