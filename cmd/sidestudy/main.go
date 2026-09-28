// sidestudy measures SIDE_MISMATCH per vendor: is the client-type (individual/institutional)
// data lagging the trade data, and by how much? It also compares the engine's current rule
// (an interval whose client-type deltas do not sum to its volume delta is dropped and the day
// becomes partial) with the proposed one (flows from the client-type series' own deltas; partial
// only when the client-type lag exceeds a threshold) on the same recording. LOCAL study tool:
// the report holds statistics only, no raw rows.
//
//	go run ./cmd/sidestudy -snapshots md.ndjson -recording preopen.ndjson -out docs/side-mismatch-study.md
//
// -snapshots: canonical snapshots (model.Snapshot NDJSON, e.g. an export of the MD stream).
// -recording: raw two-vendor recording of infra/preopen-record.sh ({ingest, vendor, body}).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"bourse/internal/model"
)

// poll is one instrument in one vendor response.
type poll struct {
	t                  time.Time
	vol, val           int64
	ib, nb, is, ns     int64 // individual/institutional buy/sell volume (day to date)
	hasSide, hasValues bool
}

type series map[string][]poll // ins_code → polls in time order

func main() {
	log.SetFlags(0)
	snaps := flag.String("snapshots", "", "canonical snapshot NDJSON (MD stream export)")
	rec := flag.String("recording", "", "raw two-vendor recording (preopen-record.sh)")
	from := flag.String("from", "", "recording: only responses at or after this Tehran HH:MM (e.g. 08:30)")
	out := flag.String("out", "", "markdown report (default stdout)")
	threshold := flag.Duration("threshold", 5*time.Minute, "proposed partial threshold on the client-type lag")
	flag.Parse()

	var parts []string
	if *snaps != "" {
		by, err := readSnapshots(*snaps)
		if err != nil {
			log.Fatal(err)
		}
		for v, s := range by {
			parts = append(parts, analyse(v, "خروجی جریان MD ("+*snaps+")", s, *threshold).markdown())
		}
	}
	if *rec != "" {
		by, err := readRecording(*rec, *from)
		if err != nil {
			log.Fatal(err)
		}
		vs := make([]string, 0, len(by))
		for v := range by {
			vs = append(vs, v)
		}
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, analyse(v, "ضبط خام ("+*rec+")", by[v], *threshold).markdown())
		}
	}
	md := strings.Join(parts, "\n")
	if *out == "" {
		fmt.Print(md)
		return
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		log.Fatal(err)
	}
}

func readSnapshots(path string) (map[string]series, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	by := map[string]series{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var s model.Snapshot
		if json.Unmarshal(sc.Bytes(), &s) != nil || s.InsCode == "" || !s.Has(model.FVolume) {
			continue
		}
		p := poll{t: s.IngestTime, vol: s.Volume, val: s.Value, ib: s.IndBuyVol, nb: s.InstBuyVol, is: s.IndSellVol, ns: s.InstSellVol,
			hasSide: s.Has(model.FIndBuyVol) && s.Has(model.FInstBuyVol) && s.Has(model.FIndSellVol) && s.Has(model.FInstSellVol)}
		if by[s.Source] == nil {
			by[s.Source] = series{}
		}
		by[s.Source][s.InsCode] = append(by[s.Source][s.InsCode], p)
	}
	for _, s := range by {
		s.sort()
	}
	return by, sc.Err()
}

// vendor keys of the raw payloads (docs/source-mapping.md).
var keys = map[string][7]string{ // code, vol, val, ind buy, inst buy, ind sell, inst sell
	"brsapi":      {"id", "tvol", "tval", "Buy_I_Volume", "Buy_N_Volume", "Sell_I_Volume", "Sell_N_Volume"},
	"sourcearena": {"instance_code", "trade_volume", "trade_value", "real_buy_volume", "co_buy_volume", "real_sell_volume", "co_sell_volume"},
}

func readRecording(path, from string) (map[string]series, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tehran, _ := time.LoadLocation("Asia/Tehran")
	by := map[string]series{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	for sc.Scan() {
		var line struct {
			Ingest time.Time                    `json:"ingest"`
			Vendor string                       `json:"vendor"`
			Body   []map[string]json.RawMessage `json:"body"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || len(line.Body) == 0 {
			continue
		}
		if from != "" && line.Ingest.In(tehran).Format("15:04") < from {
			continue
		}
		k, ok := keys[line.Vendor]
		if !ok {
			continue
		}
		if by[line.Vendor] == nil {
			by[line.Vendor] = series{}
		}
		for _, r := range line.Body {
			code := text(r[k[0]])
			vol, ok := num(r[k[1]])
			if code == "" || !ok {
				continue
			}
			p := poll{t: line.Ingest, vol: vol}
			p.val, _ = num(r[k[2]])
			var o1, o2, o3, o4 bool
			p.ib, o1 = num(r[k[3]])
			p.nb, o2 = num(r[k[4]])
			p.is, o3 = num(r[k[5]])
			p.ns, o4 = num(r[k[6]])
			p.hasSide = o1 && o2 && o3 && o4
			by[line.Vendor][code] = append(by[line.Vendor][code], p)
		}
	}
	for _, s := range by {
		s.sort()
	}
	return by, sc.Err()
}

func (s series) sort() {
	for _, ps := range s {
		sort.Slice(ps, func(i, j int) bool { return ps[i].t.Before(ps[j].t) })
	}
}

func text(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) == nil {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(string(raw))
}

func num(raw json.RawMessage) (int64, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

// result holds one vendor's statistics.
type result struct {
	vendor, source string
	polls          int // distinct response times
	first, last    time.Time
	threshold      time.Duration

	traded, mismatch, behind, ahead, unbalanced int // traded instrument-polls; of which: side ≠ volume, client < volume, client > volume, buy ≠ sell
	exactCopy                                   int // mismatches whose client buy sum equals an EARLIER polled volume exactly
	lags                                        []time.Duration
	censored                                    int // mismatches with no earlier poll at or below the client sum
	gapShare                                    []float64

	// current rule vs proposal, over consecutive pairs with volume change
	intervals, dropped          int
	volTotal, volDropped        int64 // Δvolume of all / of dropped intervals (shares)
	insTraded, insPartialBefore int
	clientAttributed            int64 // Σ accepted Δ(ind+inst buy) under the proposal
	lastVol, lastResidual       int64 // at each instrument's last poll: Σ day volume, Σ (volume − client sum) > 0
	insPartialAfter             int   // instruments whose lag exceeded the threshold at least once
	pollsOverThreshold          int
	insOverAt                   map[time.Duration]int // sensitivity: instruments over each candidate threshold
}

var candidates = []time.Duration{3 * time.Minute, 5 * time.Minute, 10 * time.Minute}

func analyse(vendor, source string, s series, threshold time.Duration) *result {
	r := &result{vendor: vendor, source: source, threshold: threshold, insOverAt: map[time.Duration]int{}}
	times := map[time.Time]bool{}
	for _, ps := range s {
		insDropped, insTraded, insOver := false, false, false
		var maxLag time.Duration // censored (unknown) counts as unbounded
		for i, p := range ps {
			times[p.t] = true
			if r.first.IsZero() || p.t.Before(r.first) {
				r.first = p.t
			}
			if p.t.After(r.last) {
				r.last = p.t
			}
			if !p.hasSide || p.vol <= 0 {
				continue
			}
			insTraded = true
			r.traded++
			buy, sell := p.ib+p.nb, p.is+p.ns
			if buy != sell {
				r.unbalanced++
			}
			if buy == p.vol && sell == p.vol {
				continue
			}
			r.mismatch++
			client := buy
			if sell < client {
				client = sell
			}
			switch {
			case client < p.vol:
				r.behind++
				r.gapShare = append(r.gapShare, float64(p.vol-client)/float64(p.vol))
			case client > p.vol:
				r.ahead++
			}
			// Lag: the client-type data is "as of" the latest earlier poll whose volume it covers.
			found := false
			for j := i - 1; j >= 0; j-- {
				if ps[j].vol == buy {
					r.exactCopy++
					break
				}
			}
			for j := i - 1; j >= 0; j-- {
				if ps[j].vol <= client {
					lag := p.t.Sub(ps[j].t)
					r.lags = append(r.lags, lag)
					found = true
					if lag > maxLag {
						maxLag = lag
					}
					if lag > threshold {
						r.pollsOverThreshold++
						insOver = true
					}
					break
				}
			}
			if !found {
				maxLag = 1 << 62
				r.censored++
				r.pollsOverThreshold++ // unknown lag: partial under the proposal too
				insOver = true
			}
		}
		// Current rule: consecutive pairs.
		for i := 1; i < len(ps); i++ {
			a, b := ps[i-1], ps[i]
			if !a.hasSide || !b.hasSide {
				continue
			}
			// Proposal: the client-type series' own delta over EVERY pair (it catches up in intervals
			// without trades too), accepted when non-negative and balanced (buy = sell).
			db, ds := (b.ib-a.ib)+(b.nb-a.nb), (b.is-a.is)+(b.ns-a.ns)
			if db >= 0 && db == ds && b.ib >= a.ib && b.nb >= a.nb && b.is >= a.is && b.ns >= a.ns {
				r.clientAttributed += db
			}
			dv := b.vol - a.vol
			if dv <= 0 {
				continue
			}
			r.intervals++
			r.volTotal += dv
			if (b.ib-a.ib)+(b.nb-a.nb) != dv || (b.is-a.is)+(b.ns-a.ns) != dv {
				r.dropped++
				r.volDropped += dv
				insDropped = true
			}
		}
		if insTraded {
			r.insTraded++
			for i := len(ps) - 1; i >= 0; i-- {
				if p := ps[i]; p.hasSide {
					client := min(p.ib+p.nb, p.is+p.ns)
					r.lastVol += p.vol
					if p.vol > client {
						r.lastResidual += p.vol - client
					}
					break
				}
			}
		}
		if insDropped {
			r.insPartialBefore++
		}
		if insOver {
			r.insPartialAfter++
		}
		if insTraded {
			for _, c := range candidates {
				if maxLag > c {
					r.insOverAt[c]++
				}
			}
		}
	}
	r.polls = len(times)
	return r
}

func pct(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f٪", 100*float64(n)/float64(d))
}

func quantiles(ds []time.Duration) string {
	if len(ds) == 0 {
		return "—"
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	q := func(p float64) time.Duration { return ds[int(p*float64(len(ds)-1))] }
	return fmt.Sprintf("میانه %s، p75 %s، p90 %s، p95 %s، بیشینه %s", q(.5).Round(time.Second), q(.75).Round(time.Second),
		q(.9).Round(time.Second), q(.95).Round(time.Second), ds[len(ds)-1].Round(time.Second))
}

func hist(ds []time.Duration) string {
	edges := []time.Duration{0, time.Minute, 2 * time.Minute, 3 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	var b strings.Builder
	b.WriteString("| بازه تأخیر | تعداد |\n| --- | ---: |\n")
	for i, lo := range edges {
		n := 0
		for _, d := range ds {
			if d >= lo && (i == len(edges)-1 || d < edges[i+1]) {
				n++
			}
		}
		hi := "بیشتر"
		if i < len(edges)-1 {
			hi = edges[i+1].String()
		}
		fmt.Fprintf(&b, "| %s تا %s | %d |\n", lo, hi, n)
	}
	return b.String()
}

func (r *result) markdown() string {
	var b strings.Builder
	teh, _ := time.LoadLocation("Asia/Tehran")
	fmt.Fprintf(&b, "## %s — %s\n\n", r.vendor, r.source)
	fmt.Fprintf(&b, "بازه: %s تا %s تهران، %d پاسخ (فاصله میانگین %s).\n\n", r.first.In(teh).Format("2006-01-02 15:04:05"),
		r.last.In(teh).Format("15:04:05"), r.polls, avgGap(r))
	fmt.Fprintf(&b, "| سنجه | مقدار |\n| --- | ---: |\n")
	fmt.Fprintf(&b, "| نماد-نوبت معامله‌شده (حجم > ۰) | %d |\n", r.traded)
	fmt.Fprintf(&b, "| SIDE_MISMATCH (حقیقی+حقوقی ≠ حجم روز) | %d (%s) |\n", r.mismatch, pct(r.mismatch, r.traded))
	fmt.Fprintf(&b, "| … که جمع نوع‌خریدار **کمتر** از حجم است (عقب) | %d (%s) |\n", r.behind, pct(r.behind, r.mismatch))
	fmt.Fprintf(&b, "| … که جمع نوع‌خریدار **بیشتر** از حجم است (جلو) | %d |\n", r.ahead)
	fmt.Fprintf(&b, "| … که جمع خرید دقیقاً برابر حجم یک نوبت **قبلی** است (نسخه قدیمی) | %d (%s) |\n", r.exactCopy, pct(r.exactCopy, r.mismatch))
	fmt.Fprintf(&b, "| جمع خرید ≠ جمع فروش در همان نوبت (ناسازگاری درونی) | %d |\n", r.unbalanced)
	fmt.Fprintf(&b, "| سهم عقب‌ماندگی از حجم روز (میانه) | %s |\n", medianShare(r.gapShare))
	fmt.Fprintf(&b, "| تأخیر نوع‌خریدار (دقت = فاصله نوبت‌ها) | %s |\n", quantiles(r.lags))
	fmt.Fprintf(&b, "| ناهمخوانی بدون نوبت قبلی قابل‌تطبیق (تأخیر نامعلوم) | %d |\n\n", r.censored)
	b.WriteString(hist(r.lags))
	b.WriteString("\n**قاعده فعلی در برابر پیشنهاد (روی همین داده):**\n\n| | قاعده فعلی | پیشنهاد |\n| --- | ---: | ---: |\n")
	fmt.Fprintf(&b, "| بازه‌های با تغییر حجم | %d | %d |\n", r.intervals, r.intervals)
	fmt.Fprintf(&b, "| بازه‌های کنارگذاشته (جریان صفر، روز ناقص) | %d (%s) | ۰ (جریان از دلتای خود سری نوع‌خریدار) |\n", r.dropped, pct(r.dropped, r.intervals))
	fmt.Fprintf(&b, "| حجم روز که در آخرین نوبت هنوز نوع‌خریدارش نیامده | — | %s از %d سهم |\n",
		pct(int(r.lastResidual/1000), int(r.lastVol/1000)), r.lastVol)
	fmt.Fprintf(&b, "| حجم نسبت داده‌شده به حقیقی/حقوقی (پیشنهاد می‌تواند از ۱۰۰٪ بگذرد: عقب‌ماندگی پیش از نخستین نوبت هم جبران می‌شود) | %s از %d سهم | %s از %d سهم |\n",
		pct(int(min64(r.volTotal-r.volDropped, 1<<62)/1000), int(r.volTotal/1000)), r.volTotal,
		pct(int(r.clientAttributed/1000), int(r.volTotal/1000)), r.volTotal)
	fmt.Fprintf(&b, "| نمادهای «روز ناقص» | %d از %d | %d از %d (تأخیر > %s دست‌کم یک بار) |\n", r.insPartialBefore, r.insTraded,
		r.insPartialAfter, r.insTraded, r.threshold)
	fmt.Fprintf(&b, "| نماد-نوبت‌های ناقص | %d | %d |\n\n", r.dropped, r.pollsOverThreshold)
	b.WriteString("حساسیت آستانه (نمادهایی که تأخیرشان دست‌کم یک بار از آستانه گذشت یا نامعلوم بود): ")
	for i, c := range candidates {
		if i > 0 {
			b.WriteString("، ")
		}
		fmt.Fprintf(&b, "%s: %d از %d", c, r.insOverAt[c], r.insTraded)
	}
	b.WriteString(".\n\n")
	return b.String()
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func medianShare(xs []float64) string {
	if len(xs) == 0 {
		return "—"
	}
	sort.Float64s(xs)
	return fmt.Sprintf("%.2f٪ (p90 %.2f٪)", 100*xs[len(xs)/2], 100*xs[int(.9*float64(len(xs)-1))])
}

func avgGap(r *result) string {
	if r.polls < 2 {
		return "—"
	}
	return (r.last.Sub(r.first) / time.Duration(r.polls-1)).Round(time.Second).String()
}
