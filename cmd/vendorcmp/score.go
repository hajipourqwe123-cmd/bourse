package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bourse/internal/tehran"
)

// scoreLine is a runs.ndjson line as the scorecard reads it (older lines: no kind, skew_s).
type scoreLine struct {
	runSummary
	SkewS float64 `json:"skew_s"`
}

// vendorScore is one vendor's day in the trial.
type vendorScore struct {
	Ahead, SideMismatch, Traded, OnlyRows, Rows int
	Errors, Outages                             int
	QuotaUsed, QuotaLimit                       int
}

// Scorecard is the daily vendor trial report (owner request B), from the watch log and the two
// quota files. Everything is a count; no values, no secrets.
type Scorecard struct {
	Day                 string
	Runs, Skips, Errors int
	First, Last         time.Time
	Gaps                []float64
	Static              map[string]int
	Equal, BothZero     int
	V                   map[string]*vendorScore
	SkipReasons         map[string]int
	Every               time.Duration
}

func score(day, logPath, brsBudget, saBudget string, brsLimit, saLimit int, every time.Duration) (*Scorecard, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := &Scorecard{Day: day, Static: map[string]int{}, SkipReasons: map[string]int{}, Every: every,
		V: map[string]*vendorScore{"brsapi": {QuotaLimit: brsLimit}, "sourcearena": {QuotaLimit: saLimit}}}
	in := bufio.NewScanner(f)
	in.Buffer(make([]byte, 1<<20), 1<<24)
	for in.Scan() {
		var l scoreLine
		if json.Unmarshal(in.Bytes(), &l) != nil || tehran.TradingDay(l.At) != day {
			continue
		}
		kind := l.Kind
		if kind == "" && l.Joined > 0 {
			kind = "run"
		}
		switch kind {
		case "skip":
			sc.Skips++
			r := strings.Fields(l.Reason)
			if len(r) > 0 {
				sc.SkipReasons[r[0]]++
				if strings.HasPrefix(r[0], "sourcearena_") {
					sc.V["sourcearena"].Outages++ // no fresh SourceArena payload this round
				}
			}
			continue
		case "error":
			sc.Errors++
			if strings.HasPrefix(l.Reason, "brsapi") {
				sc.V["brsapi"].Errors++
			} else {
				sc.V["sourcearena"].Errors++
			}
			continue
		case "run":
		default:
			continue
		}
		sc.Runs++
		if sc.First.IsZero() || l.At.Before(sc.First) {
			sc.First = l.At
		}
		if l.At.After(sc.Last) {
			sc.Last = l.At
		}
		gap := l.FetchGapS
		if gap == 0 {
			gap = l.SkewS
		}
		sc.Gaps = append(sc.Gaps, gap)
		for k, v := range l.Static {
			sc.Static[k] += v
		}
		sc.V["brsapi"].Ahead += l.VolumeAhead["brsapi"]
		sc.V["sourcearena"].Ahead += l.VolumeAhead["sourcearena"]
		sc.Equal += l.VolumeAhead["equal"]
		sc.BothZero += l.VolumeAhead["both_zero"]
		sc.V["brsapi"].OnlyRows += l.OnlyBrs
		sc.V["sourcearena"].OnlyRows += l.OnlySa
		sc.V["brsapi"].Rows += l.BrsRows
		sc.V["sourcearena"].Rows += l.SaRows
		for v, n := range l.Traded {
			if sc.V[v] != nil {
				sc.V[v].Traded += n
			}
		}
		for v, n := range l.SideMismatch {
			if sc.V[v] != nil {
				sc.V[v].SideMismatch += n
			}
		}
		if l.BrsUsedToday > sc.V["brsapi"].QuotaUsed {
			sc.V["brsapi"].QuotaUsed = l.BrsUsedToday
		}
	}
	for vendor, path := range map[string]string{"brsapi": brsBudget, "sourcearena": saBudget} {
		var st struct {
			Day  string `json:"day"`
			Used int    `json:"used"`
		}
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &st) == nil && st.Day == day && st.Used > sc.V[vendor].QuotaUsed {
			sc.V[vendor].QuotaUsed = st.Used
		}
	}
	return sc, in.Err()
}

func share(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f٪", 100*float64(n)/float64(d))
}

// Markdown renders the Persian daily report.
func (s *Scorecard) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# کارنامه روزانه فروشندگان داده — %s\n\n", s.Day)
	b.WriteString("گزارش محلی آزمایش دو فروشنده (`vendorcmp -score`). فقط شمارش؛ نه کلید، نه داده خام. " +
		"BrsApi فقط در حالت مقایسه پرسیده می‌شود و سورس‌آرنا منبع اصلی است.\n\n")
	if s.Runs == 0 {
		fmt.Fprintf(&b, "در این روز هیچ مقایسه‌ای اجرا نشد (رد شده: %d، خطا: %d).\n", s.Skips, s.Errors)
	} else {
		fmt.Fprintf(&b, "مقایسه‌ها: %d نوبت، از %s تا %s تهران. رد شده: %d، خطا: %d.\n\n", s.Runs,
			s.First.In(tehran.Loc).Format("15:04"), s.Last.In(tehran.Loc).Format("15:04"), s.Skips, s.Errors)
	}
	sort.Float64s(s.Gaps)
	med := "—"
	if len(s.Gaps) > 0 {
		med = fmt.Sprintf("%.0f ثانیه", s.Gaps[len(s.Gaps)/2])
	}
	b.WriteString("| سنجه | BrsApi | سورس‌آرنا |\n| --- | ---: | ---: |\n")
	br, sa := s.V["brsapi"], s.V["sourcearena"]
	cmp := br.Ahead + sa.Ahead + s.Equal
	fmt.Fprintf(&b, "| تازگی: ردیف‌هایی که حجم روز این فروشنده بیشتر است (از %d ردیف معامله‌شده مشترک) | %d (%s) | %d (%s) |\n",
		cmp, br.Ahead, share(br.Ahead, cmp), sa.Ahead, share(sa.Ahead, cmp))
	fmt.Fprintf(&b, "| نرخ SIDE_MISMATCH (حقیقی+حقوقی ≠ حجم روز، از ردیف‌های معامله‌شده) | %s | %s |\n",
		share(br.SideMismatch, br.Traded), share(sa.SideMismatch, sa.Traded))
	fmt.Fprintf(&b, "| ردیف‌هایی که فقط این فروشنده دارد (میانگین هر نوبت) | %s | %s |\n", avg(br.OnlyRows, s.Runs), avg(sa.OnlyRows, s.Runs))
	fmt.Fprintf(&b, "| ردیف در هر پاسخ (میانگین) | %s | %s |\n", avg(br.Rows, s.Runs), avg(sa.Rows, s.Runs))
	fmt.Fprintf(&b, "| قطعی یا خطا (نوبت) | %d خطا | %d نوبت بدون پاسخ تازه (≈ %s) + %d خطا |\n",
		br.Errors, sa.Outages, (time.Duration(sa.Outages) * s.Every).Round(time.Minute), sa.Errors)
	fmt.Fprintf(&b, "| مصرف سهمیه امروز | %d از %s | %d از %s |\n", br.QuotaUsed, limit(br.QuotaLimit), sa.QuotaUsed, limit(sa.QuotaLimit))
	b.WriteString("\n")
	fmt.Fprintf(&b, "- فاصله دو دریافت (میانه): %s. «بیشتر بودن حجم» در این فاصله تازگی را تقریبی نشان می‌دهد؛ هیچ‌کدام زمان snapshot نمی‌فرستند.\n", med)
	fmt.Fprintf(&b, "- ردیف‌های با حجم برابر: %d؛ هر دو صفر: %d.\n", s.Equal, s.BothZero)
	if len(s.Static) == 0 {
		b.WriteString("- فیلدهای ثابت روز (شناسه، قیمت دیروز، دامنه): **بدون ناهمخوانی** (فقط ۰۹:۰۰ تا ۱۸:۰۰ شمرده می‌شود).\n")
	} else {
		fmt.Fprintf(&b, "- **ناهمخوانی در فیلدهای ثابت روز:** %v\n", s.Static)
	}
	if len(s.SkipReasons) > 0 {
		fmt.Fprintf(&b, "- دلایل ردشدن نوبت‌ها: %v (sourcearena_stale یعنی collector پاسخ تازه نداشت: قطعی، سهمیه یا خاموش بودن).\n", s.SkipReasons)
	}
	return b.String()
}

func avg(n, runs int) string {
	if runs == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f", float64(n)/float64(runs))
}

func limit(n int) string {
	if n <= 0 {
		return "نامعلوم"
	}
	return fmt.Sprint(n)
}

// writeScore writes the scorecard next to the watch log: scorecard-<day>.md.
func writeScore(day, logPath string, brsLimit, saLimit int, saBudget string, every time.Duration) (string, error) {
	sc, err := score(day, logPath, filepath.Join(filepath.Dir(logPath), "brsapi-used.json"), saBudget, brsLimit, saLimit, every)
	if err != nil {
		return "", err
	}
	out := filepath.Join(filepath.Dir(logPath), "scorecard-"+day+".md")
	return out, os.WriteFile(out, []byte(sc.Markdown()), 0o644)
}
