// collector polls a data source and publishes canonical snapshots.
//
//	SOURCE=replay REPLAY_FILE=testdata/synthetic_day.ndjson collector > snaps.ndjson
//	SOURCE=sourcearena SOURCEARENA_TOKEN=… POLL_INTERVAL=5s collector
//	SOURCE=brsapi BRSAPI_KEY=… POLL_INTERVAL=…  # BRSAPI_TYPES=1[,4], BRSAPI_DAILY_LIMIT=100, BRSAPI_5MIN_LIMIT=300:
//	                                         # refuses to start if the interval exceeds the plan's quota
//	BUS=nats NATS_URL=nats://… collector     # publish to JetStream instead of stdout
//	                                         # (SYN* refused unless ALLOW_SYNTHETIC_ON_BUS=1)
//	SESSIONS_FILE=path.json                  # session calendar; live sources poll only while a class is in session
//	REPLAY_REBASE=today|now                  # DEV ONLY: re-time a recording to the present, paced in real time
//	                                         #   today: onto today's date (REPLAY_DATE=YYYY-MM-DD for another day)
//	                                         #   now: recording time REPLAY_AT (default 10:00) = the current instant
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"bourse/internal/bus"
	"bourse/internal/calendar"
	"bourse/internal/config"
	"bourse/internal/model"
	"bourse/internal/source"
	"bourse/internal/tehran"
)

func main() { os.Exit(run()) }

// run returns the exit code; deferred cleanup (bus drain) runs before the process exits.
func run() int {
	log.SetOutput(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var src source.Source
	var brsPer5 int64 // SOURCE=brsapi: the plan's 5-minute quota (budget check below)
	switch kind := config.Str("SOURCE", "replay"); kind {
	case "replay":
		r, err := source.NewReplay(config.Str("REPLAY_FILE", "testdata/synthetic_day.ndjson"))
		if err != nil {
			log.Fatalf("replay: %v", err)
		}
		src = r
	case "sourcearena":
		src = source.NewSourceArena(
			config.Str("SOURCEARENA_URL", "https://apis.sourcearena.ir/api/"),
			os.Getenv("SOURCEARENA_TOKEN"),
			config.Dur("HTTP_TIMEOUT", 10*time.Second))
	case "brsapi":
		cfg, per5, err := brsapiConfig()
		if err != nil {
			log.Fatalf("collector: %v", err)
		}
		src, brsPer5 = source.NewBrsApi(cfg), per5
	default:
		log.Fatalf("unknown SOURCE %q", kind)
	}

	allowSynthetic := config.Str("ALLOW_SYNTHETIC_ON_BUS", "") == "1"
	if allowSynthetic {
		log.Printf("collector: WARNING: ALLOW_SYNTHETIC_ON_BUS=1: synthetic (SYN*) data may be published to the bus; " +
			"only acceptable on a disposable local stack, never where the gateway or ClickHouse writer serve users (rule 5)")
	}
	var pub bus.Publisher
	var retry []time.Duration // NDJSON: a failed stdout write is not retried (it could tear a line)
	var guard func(*model.Snapshot) error
	switch kind := config.Str("BUS", "ndjson"); kind {
	case "ndjson":
		pub = bus.NewNDJSON(os.Stdout)
	case "nats":
		guard = func(s *model.Snapshot) error { return busGuard(s, allowSynthetic) }
		js, err := bus.ConnectJetStream(ctx, config.Str("NATS_URL", "nats://127.0.0.1:4222"), "collector", bus.DefaultStreams())
		if err != nil {
			log.Fatalf("collector: %v", err)
		}
		defer js.Close()
		go js.WatchLimits(ctx, 30*time.Second)
		pub = js
		retry = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}
	default:
		log.Fatalf("collector: unknown BUS %q (want ndjson or nats)", kind)
	}
	interval := config.Dur("POLL_INTERVAL", 5*time.Second)
	replayDelay := config.Dur("REPLAY_DELAY", 0)
	backoff := time.Second
	log.Printf("collector: source=%s interval=%s bus=%s", src.Name(), interval, config.Str("BUS", "ndjson"))
	_, isReplay := src.(*source.Replay)
	cal, err := calendar.Load(config.Str("SESSIONS_FILE", ""))
	if err != nil {
		log.Printf("collector: %v", err)
		return 1
	}
	if n := cal.Unverified(); n > 0 {
		log.Printf("collector: WARNING: session calendar has %d rules/holidays not verified against an official source (docs/sessions.md)", n)
	}
	if now := time.Now(); cal.HolidaysBetween(now, now.AddDate(1, 0, 0)) == 0 {
		log.Printf("collector: WARNING: no official holiday listed in the session calendar for the next 12 months; " +
			"on an unlisted holiday the vendor's previous-day data would be collected as today's (docs/sessions.md)")
	}
	if b, ok := src.(*source.BrsApi); ok {
		cfg := b.Config()
		if err := brsapiBudget(len(cfg.Types), interval, cal.MaxDailySpan(), int64(cfg.DailyLimit), brsPer5); err != nil {
			log.Printf("collector: %v", err)
			return 1
		}
	}
	if span := cal.MaxDailySpan(); span > bus.SizedSessionSpan {
		log.Printf("collector: WARNING: calendar sessions span up to %s a day, more than the %s the bus streams are sized for (contracts/subjects.md)",
			span, bus.SizedSessionSpan)
	}
	if mode := config.Str("REPLAY_REBASE", ""); mode != "" {
		if !isReplay {
			log.Printf("collector: REPLAY_REBASE needs SOURCE=replay")
			return 1
		}
		rb, err := rebase(src, source.RebaseMode(mode), cal, time.Now())
		if err != nil {
			log.Printf("collector: %v", err)
			return 1
		}
		log.Printf("collector: WARNING: REPLAY_REBASE=%s: recording re-timed to the present and paced in real time (local development only)", mode)
		src, replayDelay = rb, 0
	}
	var filter *publishFilter
	if !isReplay { // a recording is replayed as recorded, whatever the wall clock says
		filter = newPublishFilter(cal)
	}
	outside := false

	for {
		if !isReplay {
			now := time.Now()
			if u, open := cal.Union(now); !open || !u.Contains(now) {
				if !outside {
					log.Printf("collector: outside every instrument class's session today (Tehran); not polling")
					outside = true
				}
				select {
				case <-ctx.Done():
					return 0
				case <-time.After(interval):
				}
				continue
			}
			if outside {
				log.Printf("collector: a session is open; polling")
				outside = false
			}
		}
		batch, err := src.Fetch(ctx)
		if errors.Is(err, source.ErrDone) {
			log.Printf("collector: source exhausted")
			return 0
		}
		if err != nil {
			log.Printf("collector: fetch error: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if filter != nil {
			filter.warnUnmapped(batch)
		}
		for i := range batch {
			if filter != nil && !filter.keep(&batch[i]) {
				continue
			}
			if guard != nil {
				if err := guard(&batch[i]); err != nil {
					log.Printf("collector: %v", err)
					return 1
				}
			}
			subj := bus.SubjSnapshot(batch[i].InsCode)
			if err := bus.Retry(ctx, retry, nil, func() error { return publishSnapshot(pub, subj, &batch[i]) }); err != nil {
				log.Printf("collector: publish: %v", err)
				return 1
			}
			if filter != nil {
				filter.published(&batch[i])
			}
		}
		wait := interval
		if isReplay {
			wait = replayDelay
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(wait):
			}
		} else if ctx.Err() != nil {
			return 0
		}
	}
}

// publishSnapshot publishes s; on JetStream with a message ID built from the instrument, source
// time and ingest time, so a retried publish that was in fact stored is not stored twice, while
// a re-poll of an unchanged snapshot (new ingest time) still reaches the engine.
func publishSnapshot(pub bus.Publisher, subj string, s *model.Snapshot) error {
	if idp, ok := pub.(interface {
		PublishID(subject, id string, v any) error
	}); ok {
		return idp.PublishID(subj, fmt.Sprintf("snap:%s:%d:%d", s.InsCode, s.SourceTime.UnixNano(), s.IngestTime.UnixNano()), s)
	}
	return pub.Publish(subj, s)
}

// busGuard keeps SYNTHETIC data off the shared bus (rule 5: SYN* never reaches users, and the
// bus feeds the gateway and the ClickHouse writer). Local experiments opt in explicitly with
// ALLOW_SYNTHETIC_ON_BUS=1, on a disposable local stack only.
func busGuard(s *model.Snapshot, allowSynthetic bool) error {
	if allowSynthetic {
		return nil
	}
	if model.IsSynthetic(s) {
		return fmt.Errorf("refusing to publish synthetic instrument %s to the bus (set ALLOW_SYNTHETIC_ON_BUS=1 only on a disposable local stack)", s.InsCode)
	}
	return nil
}

// publishFilter decides which polled snapshots of a live source reach the bus. It keeps the bus
// volume inside what the stream sizing assumes without ever hiding data:
//   - an instrument's FIRST snapshot of a trading day is always published, even if unchanged
//     (e.g. the vendor still shows yesterday's totals before the open): it is the day baseline the
//     engine's late-start rule must judge;
//   - inside the instrument's own session [pre_open, close) every snapshot is published (STALE
//     detection needs the repeats);
//   - outside it, a snapshot is published only if its market data changed, so a misclassified
//     instrument that trades out of "its" hours still flows.
type publishFilter struct {
	cal      *calendar.Calendar
	last     map[string]model.Snapshot // last published, per instrument
	day      map[string]string         // trading day (ingest time) of last published, per instrument
	warnedOn string                    // day the unmapped-instrument warning was last logged
}

func newPublishFilter(cal *calendar.Calendar) *publishFilter {
	return &publishFilter{cal: cal, last: map[string]model.Snapshot{}, day: map[string]string{}}
}

func (f *publishFilter) keep(s *model.Snapshot) bool {
	last, seen := f.last[s.InsCode]
	if !seen || f.day[s.InsCode] != tehran.TradingDay(s.IngestTime) {
		return true // first of the (ingest) day
	}
	if tehran.TradingDay(s.SourceTime) != tehran.TradingDay(last.SourceTime) {
		return true // the source moved to a new trading day: that is the day's baseline
	}
	if sess, ok := f.cal.Session(s.InsCode, s.IngestTime); ok && sess.Contains(s.IngestTime) {
		return true
	}
	return marketChanged(last, *s)
}

func (f *publishFilter) published(s *model.Snapshot) {
	f.last[s.InsCode] = *s
	f.day[s.InsCode] = tehran.TradingDay(s.IngestTime)
}

// marketChanged compares everything but the timestamps.
func marketChanged(a, b model.Snapshot) bool {
	a.SourceTime, a.IngestTime, a.SourceTimeEstimated = time.Time{}, time.Time{}, false
	b.SourceTime, b.IngestTime, b.SourceTimeEstimated = time.Time{}, time.Time{}, false
	return !reflect.DeepEqual(a, b)
}

// warnUnmapped logs, once per trading day, how many instruments have no class in the calendar
// (class "unknown": session = union of all classes, excluded from per-class aggregates).
func (f *publishFilter) warnUnmapped(batch []model.Snapshot) {
	if len(batch) == 0 {
		return
	}
	day := tehran.TradingDay(batch[0].IngestTime)
	if day == f.warnedOn {
		return
	}
	f.warnedOn = day
	n := 0
	for i := range batch {
		if !f.cal.Mapped(batch[i].InsCode) {
			n++
		}
	}
	if n > 0 {
		log.Printf("collector: WARNING: %d of %d instruments have no class in the session calendar (class %q; see docs/sessions.md)", n, len(batch), calendar.Unknown)
	}
}

// rebase wraps a replay for REPLAY_REBASE. "today" is refused on a day no class trades (the
// dashboard would show closed sessions) unless REPLAY_DATE names the target day.
func rebase(src source.Source, mode source.RebaseMode, cal *calendar.Calendar, now time.Time) (source.Source, error) {
	target := now
	if d := config.Str("REPLAY_DATE", ""); d != "" {
		t, err := time.ParseInLocation("2006-01-02", d, tehran.Loc)
		if err != nil {
			return nil, fmt.Errorf("REPLAY_DATE %q: want YYYY-MM-DD", d)
		}
		target = t
	} else if mode == source.RebaseToday {
		if _, open := cal.Union(now); !open {
			return nil, fmt.Errorf("REPLAY_REBASE=today: no class trades today (%s); set REPLAY_DATE=YYYY-MM-DD or use REPLAY_REBASE=now", tehran.TradingDay(now))
		}
	}
	at, err := time.Parse("15:04", config.Str("REPLAY_AT", "10:00"))
	if err != nil {
		return nil, fmt.Errorf("REPLAY_AT: want HH:MM")
	}
	return source.NewRebase(src, mode, target, time.Duration(at.Hour())*time.Hour+time.Duration(at.Minute())*time.Minute)
}
