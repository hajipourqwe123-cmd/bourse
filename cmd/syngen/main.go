// syngen writes one SYNTHETIC trading day of canonical snapshots (NDJSON) for local development
// and demos. The data is invented: instruments are named SYN*, source is "synthetic", and it must
// never be shown to users or used for backtests.
package main

import (
	"encoding/json"
	"flag"
	"math/rand"
	"os"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
)

type inst struct {
	s            model.Snapshot
	bigBuyEvery  int // inject one large new buyer every N steps (0 = never)
	bigSellEvery int
	price        float64
	drift        float64
}

func main() {
	day := flag.String("day", "2026-09-23", "Tehran trading day (YYYY-MM-DD)")
	step := flag.Duration("step", 5*time.Second, "snapshot interval")
	seed := flag.Int64("seed", 1405, "random seed (deterministic output)")
	flag.Parse()
	d, err := time.ParseInLocation("2006-01-02", *day, tehran.Loc)
	if err != nil {
		panic(err)
	}
	rng := rand.New(rand.NewSource(*seed))
	start := d.Add(9 * time.Hour)
	end := d.Add(12*time.Hour + 30*time.Minute)

	mk := func(code, sym string, price float64, drift float64, bb, bs int) *inst {
		return &inst{s: model.Snapshot{InsCode: code, Symbol: sym, Source: "synthetic",
			PriceYesterday: int64(price), PriceFirst: int64(price)}, price: price, drift: drift, bigBuyEvery: bb, bigSellEvery: bs}
	}
	insts := []*inst{
		mk("SYNTHETIC0001", "SYN-ACCUM", 12_000, 0.00004, 180, 0), // quiet accumulation by large buyers
		mk("SYNTHETIC0002", "SYN-RETAIL", 5_000, 0.0, 0, 0),       // retail noise only
		mk("SYNTHETIC0003", "SYN-DISTRIB", 30_000, -0.00003, 0, 150),
		mk("SYNTHETIC0004", "SYN-MIXED", 8_000, 0.00001, 300, 300),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	i := 0
	for t := start; !t.After(end); t = t.Add(*step) {
		for _, in := range insts {
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
		i++
	}
}
