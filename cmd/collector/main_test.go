package main

import (
	"strings"
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/flow"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/source"
	"bourse/internal/tehran"
)

func TestBusGuardRefusesSynthetic(t *testing.T) {
	cases := []struct {
		s    model.Snapshot
		want bool // refused
	}{
		{model.Snapshot{InsCode: "IRO1FOLD0001", Symbol: "فولاد", Source: "sourcearena"}, false},
		{model.Snapshot{InsCode: "SYNTHETIC0001", Symbol: "SYN-ACCUM", Source: "synthetic"}, true},
		{model.Snapshot{InsCode: "IRO1X", Symbol: "x", Source: "synthetic"}, true},
		{model.Snapshot{InsCode: "SYNTEST0001", Symbol: "x", Source: "replay"}, true},
	}
	for _, c := range cases {
		if got := busGuard(&c.s, false) != nil; got != c.want {
			t.Errorf("%+v: refused=%v, want %v", c.s, got, c.want)
		}
		if busGuard(&c.s, true) != nil {
			t.Errorf("%+v: refused although explicitly allowed", c.s)
		}
	}
}

func at(day, hms string) time.Time {
	x, err := time.ParseInLocation("2006-01-02 15:04:05", day+" "+hms, tehran.Loc)
	if err != nil {
		panic(err)
	}
	return x
}

// The polling window is the union of the classes' sessions: 08:25–18:00 on a Saturday
// (2026-09-26), nothing on a Thursday (2026-10-01).
func TestPollingWindowIsSessionUnion(t *testing.T) {
	cal := calendar.Default()
	u, open := cal.Union(at("2026-09-26", "10:00:00"))
	for hms, want := range map[string]bool{"08:24:59": false, "08:25:00": true, "12:30:00": true, "17:59:59": true, "18:00:00": false} {
		if got := open && u.Contains(at("2026-09-26", hms)); got != want {
			t.Errorf("Saturday %s: polling=%v, want %v", hms, got, want)
		}
	}
	if _, open := cal.Union(at("2026-10-01", "10:00:00")); open {
		t.Error("polling on a Thursday")
	}
}

func snapAt(ins string, ingest time.Time, vol int64) model.Snapshot {
	return model.Snapshot{InsCode: ins, Symbol: "x", Source: "sourcearena", SourceTime: ingest, IngestTime: ingest,
		SourceTimeEstimated: true, PriceLast: 10_000, Volume: vol, Value: vol * 10_000}
}

// Owner rule: the first snapshot of an instrument's day is ALWAYS published, even unchanged
// (the vendor still shows yesterday's totals pre-open) so the engine's late-start rule judges it;
// unchanged snapshots outside the instrument's own [pre_open, close) are skipped; changed ones
// never are (a misclassified instrument trading out of "its" hours still flows).
func TestPublishFilter(t *testing.T) {
	f := newPublishFilter(calendar.Default().WithInstruments(map[string]string{"IRSTOCK": "stock"}))
	step := func(s model.Snapshot) bool {
		k := f.keep(&s)
		if k {
			f.published(&s)
		}
		return k
	}
	yesterday := snapAt("IRSTOCK", at("2026-09-26", "12:29:55"), 5_000_000)
	for _, c := range []struct {
		name string
		s    model.Snapshot
		want bool
	}{
		{"yesterday's last snapshot", yesterday, true},
		{"after yesterday's close, unchanged", snapAt("IRSTOCK", at("2026-09-26", "13:00:00"), 5_000_000), false},
		{"first of today at 08:26, still yesterday's totals", snapAt("IRSTOCK", at("2026-09-27", "08:26:00"), 5_000_000), true},
		{"08:30 unchanged, before the stock pre-open (08:45)", snapAt("IRSTOCK", at("2026-09-27", "08:30:00"), 5_000_000), false},
		{"08:45 unchanged, in its pre-open", snapAt("IRSTOCK", at("2026-09-27", "08:45:00"), 5_000_000), true},
		{"10:00 unchanged, in session (STALE needs repeats)", snapAt("IRSTOCK", at("2026-09-27", "10:00:00"), 5_000_000), true},
		{"14:00 changed although 'stock' hours ended", snapAt("IRSTOCK", at("2026-09-27", "14:00:00"), 5_100_000), true},
		{"14:00:05 unchanged after hours", snapAt("IRSTOCK", at("2026-09-27", "14:00:05"), 5_100_000), false},
		{"unmapped instrument at 17:00 (union session)", snapAt("IRX", at("2026-09-27", "17:00:00"), 0), true},
		{"unmapped instrument at 17:00:05, unchanged, still in the union", snapAt("IRX", at("2026-09-27", "17:00:05"), 0), true},
	} {
		if got := step(c.s); got != c.want {
			t.Errorf("%s: published=%v, want %v", c.name, got, c.want)
		}
	}
}

// End to end with the engine rule: the pre-open baseline that still carries yesterday's totals
// is published, and the engine marks that day partial instead of silently skipping it.
func TestYesterdaysTotalsPreOpenBaselineReachesLateStartRule(t *testing.T) {
	cal := calendar.Default().WithInstruments(map[string]string{"IRSTOCK": "stock"})
	f := newPublishFilter(cal)
	y := snapAt("IRSTOCK", at("2026-09-26", "12:29:55"), 5_000_000)
	f.published(&y)
	pre := snapAt("IRSTOCK", at("2026-09-27", "08:26:00"), 5_000_000)
	if !f.keep(&pre) {
		t.Fatal("pre-open baseline with yesterday's totals was not published")
	}
	pre.IndBuyVol, pre.IndSellVol, pre.InstBuyVol, pre.InstSellVol = 3_000_000, 3_000_000, 2_000_000, 2_000_000
	cfg := flow.DefaultConfig()
	cfg.Sessions = cal
	r := flow.New(cfg).Process(pre)
	found := false
	for _, i := range r.Issues {
		found = found || i.Code == quality.DayStartMissed
	}
	if !found {
		t.Fatalf("engine did not flag the day: %+v", r.Issues)
	}
}

// A source with real timestamps: at 08:26 it still reports yesterday's source time (first
// ingest of the day, published); when its source time moves to today at 08:40 (before the
// stock pre-open, values unchanged) that is today's baseline and is published too.
func TestPublishFilterSourceDayChange(t *testing.T) {
	f := newPublishFilter(calendar.Default().WithInstruments(map[string]string{"IRSTOCK": "stock"}))
	step := func(src, ingest time.Time) bool {
		s := snapAt("IRSTOCK", ingest, 5_000_000)
		s.SourceTime, s.SourceTimeEstimated = src, false
		k := f.keep(&s)
		if k {
			f.published(&s)
		}
		return k
	}
	y := at("2026-09-26", "12:29:59")
	if !step(y, at("2026-09-27", "08:26:00")) {
		t.Fatal("first ingest of the day not published")
	}
	if step(y, at("2026-09-27", "08:30:00")) {
		t.Fatal("unchanged snapshot outside the session published")
	}
	if !step(at("2026-09-27", "08:40:00"), at("2026-09-27", "08:40:01")) {
		t.Fatal("source moved to today (the day baseline) but was not published")
	}
}

func TestRebaseRefusesClosedTodayUnlessDateGiven(t *testing.T) {
	cal := calendar.Default()
	fri, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-25 10:00", tehran.Loc)
	src, err := source.NewReplay("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebase(src, source.RebaseToday, cal, fri); err == nil || !strings.Contains(err.Error(), "REPLAY_DATE") {
		t.Errorf("today on a Friday = %v, want refusal naming REPLAY_DATE", err)
	}
	if _, err := rebase(src, source.RebaseNow, cal, fri); err != nil {
		t.Errorf("now on a Friday: %v", err)
	}
	t.Setenv("REPLAY_DATE", "2026-09-23")
	if _, err := rebase(src, source.RebaseToday, cal, fri); err != nil {
		t.Errorf("today with REPLAY_DATE: %v", err)
	}
	t.Setenv("REPLAY_DATE", "23/09/2026")
	if _, err := rebase(src, source.RebaseToday, cal, fri); err == nil {
		t.Error("bad REPLAY_DATE accepted")
	}
}
