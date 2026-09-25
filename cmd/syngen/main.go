// syngen writes one SYNTHETIC trading day of canonical snapshots (NDJSON) for local development
// and demos. The data is invented: instruments are named SYN*, source is "synthetic", and it must
// never be shown to users or used for backtests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/model"
	"bourse/internal/tehran"
)

type inst struct {
	s            model.Snapshot
	sess         calendar.Session // from the session calendar (class of its ins_code)
	step         int              // trading steps emitted so far
	bigBuyEvery  int              // inject one large new buyer every N steps (0 = never)
	bigSellEvery int
	price        float64
	drift        float64
}

func main() {
	day := flag.String("day", "2026-09-23", "Tehran trading day (YYYY-MM-DD)")
	step := flag.Duration("step", 5*time.Second, "snapshot interval")
	seed := flag.Int64("seed", 1405, "random seed (deterministic output)")
	n := flag.Int("n", 5, "number of instruments (>= 5; the first 5 are the fixed scenarios, more are load)")
	flag.Parse()
	d, err := time.ParseInLocation("2006-01-02", *day, tehran.Loc)
	if err != nil {
		panic(err)
	}
	cal, err := calendar.Load(os.Getenv("SESSIONS_FILE"))
	if err != nil {
		panic(err)
	}
	rng := rand.New(rand.NewSource(*seed))

	mk := func(code, sym string, price float64, drift float64, bb, bs int) *inst {
		return &inst{s: model.Snapshot{InsCode: code, Symbol: sym, Source: "synthetic",
			PriceYesterday: int64(price), PriceFirst: int64(price)}, price: price, drift: drift, bigBuyEvery: bb, bigSellEvery: bs}
	}
	insts := []*inst{
		mk("SYNTHETIC0001", "SYN-ACCUM", 12_000, 0.00004, 180, 0), // quiet accumulation by large buyers
		mk("SYNTHETIC0002", "SYN-RETAIL", 5_000, 0.0, 0, 0),       // retail noise only
		mk("SYNTHETIC0003", "SYN-DISTRIB", 30_000, -0.00003, 0, 150),
		mk("SYNTHETIC0004", "SYN-MIXED", 8_000, 0.00001, 300, 300),
		mk("SYNTHETICG001", "SYN-GOLDFUND", 45_000, 0.00002, 240, 360), // afternoon session (gold class)
	}
	for k := 4; len(insts) < *n; k++ { // load instruments (unmapped: class unknown, union session), varying the four stock scenarios
		base := insts[k%4]
		in := mk(fmt.Sprintf("SYNTHETIC%04d", k+1), fmt.Sprintf("%s-%d", base.s.Symbol, k+1),
			base.price*(0.5+float64(k%7)/4), base.drift, base.bigBuyEvery, base.bigSellEvery)
		insts = append(insts, in)
	}
	// Each instrument trades in its own session from the calendar; one zero-volume snapshot one
	// step before its open is its pre-open day baseline, so the demo day is complete.
	var start, end time.Time
	for _, in := range insts {
		sess, ok := cal.Session(in.s.InsCode, d.Add(12*time.Hour))
		if !ok {
			panic("syngen: " + *day + " is not a trading day for " + in.s.InsCode)
		}
		in.sess = sess
		if pre := sess.Open.Add(-*step); start.IsZero() || pre.Before(start) {
			start = pre
		}
		if sess.Close.After(end) {
			end = sess.Close
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	for t := start; !t.After(end); t = t.Add(*step) {
		for _, in := range insts {
			if t.Equal(in.sess.Open.Add(-*step)) {
				pre := in.s
				pre.PriceLast, pre.PriceClose = pre.PriceYesterday, pre.PriceYesterday
				pre.SourceTime, pre.IngestTime = t, t.Add(500*time.Millisecond)
				enc.Encode(map[string]any{"subject": "md.snap." + pre.InsCode, "data": pre})
				continue
			}
			if t.Before(in.sess.Open) || !t.Before(in.sess.Close) { // trading is [open, close)
				continue
			}
			i := in.step
			in.step++
			in.price *= 1 + in.drift + rng.NormFloat64()*0.0006
			p := int64(in.price)
			// retail flow: a few small trades
			vol := int64(rng.Intn(4000) + 500)
			indBuy := vol * int64(60+rng.Intn(30)) / 100
			indSell := vol * int64(60+rng.Intn(30)) / 100
			newBuyers := int64(rng.Intn(4))
			newSellers := int64(rng.Intn(4))
			if in.bigBuyEvery > 0 && i%in.bigBuyEvery == in.bigBuyEvery/2 {
				big := int64(2.5e9 / in.price) // ~250M toman by one new buyer
				vol += big
				indBuy += big
				indSell += big * 70 / 100
				newBuyers++
				newSellers += 20
			}
			if in.bigSellEvery > 0 && i%in.bigSellEvery == in.bigSellEvery/3 {
				big := int64(2.2e9 / in.price)
				vol += big
				indSell += big
				indBuy += big * 80 / 100
				newSellers++
				newBuyers += 25
			}
			s := &in.s
			s.PriceLast = p
			if s.PriceMin == 0 || p < s.PriceMin {
				s.PriceMin = p
			}
			if p > s.PriceMax {
				s.PriceMax = p
			}
			s.PriceClose = p // simplified: synthetic closing price tracks last
			s.TradeCount += int64(rng.Intn(20) + 1)
			s.Volume += vol
			s.Value += vol * p
			s.IndBuyVol += indBuy
			s.InstBuyVol += vol - indBuy
			s.IndSellVol += indSell
			s.InstSellVol += vol - indSell
			s.IndBuyCount += newBuyers
			s.IndSellCount += newSellers
			s.SourceTime = t
			s.IngestTime = t.Add(time.Duration(300+rng.Intn(900)) * time.Millisecond)
			enc.Encode(map[string]any{"subject": "md.snap." + s.InsCode, "data": s})
		}
	}
}
