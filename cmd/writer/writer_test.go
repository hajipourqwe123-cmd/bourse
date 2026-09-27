package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bourse/internal/bus"
	"bourse/internal/market"
	"bourse/internal/model"
	"bourse/internal/quality"
	"bourse/internal/tehran"
)

// fakeSink records inserted rows; fail makes the next n inserts fail.
type fakeSink struct {
	rows map[string][]map[string]any
	fail int
}

func (f *fakeSink) insert(_ context.Context, table string, rows [][]byte) error {
	if f.fail > 0 {
		f.fail--
		return errors.New("clickhouse down")
	}
	if f.rows == nil {
		f.rows = map[string][]map[string]any{}
	}
	for _, r := range rows {
		var m map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			panic(err)
		}
		f.rows[table] = append(f.rows[table], m)
	}
	return nil
}

func tehranAt(hm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-23 "+hm, tehran.Loc)
	return t
}

func msg(t *testing.T, subject string, seq uint64, v any) bus.Msg {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bus.Msg{Subject: subject, Data: b, StreamSeq: seq}
}

func TestRowsCarryPartialClassAndDedupKeys(t *testing.T) {
	at := tehranAt("09:00")
	s := &fakeSink{}
	w := &writer{sink: s}
	msgs := []bus.Msg{
		msg(t, "md.snap.S1", 1, model.Snapshot{InsCode: "S1", Symbol: "ش۱", Source: "sourcearena", SourceTime: at, IngestTime: at.Add(time.Second),
			PriceLast: 1000, Volume: 5, Missing: []string{"book"}}),
		msg(t, "flow.event.S1", 2, model.FlowEvent{InsCode: "S1", Class: market.Stock, Side: model.Buy, Band: model.BandHot,
			Attribution: model.Attributed, IntervalFrom: at, IntervalTo: at.Add(5 * time.Second), Value: 3e9}),
		msg(t, "flow.game.S1", 3, model.GameTotals{InsCode: "S1", Class: market.Gold, Day: "2026-09-23", AsOf: at, NetHot: -42, Partial: true}),
		msg(t, "flow.10m.S1", 4, model.TenMinute{InsCode: "S1", Class: market.Stock, WindowStart: at, NetHot: 7, Partial: true}),
		msg(t, "quality.S1", 5, model.QualityIssue{InsCode: "S1", Code: quality.DayStartMissed, Detail: "late", At: at.Add(time.Hour)}),
		msg(t, "quality.S1", 6, model.QualityIssue{InsCode: "S1", Code: quality.Stale, Detail: "40s", At: at}),
	}
	if err := w.handle(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	g := s.rows[tGame][0]
	if g["partial"] != true || g["class"] != "gold" || g["day"] != "2026-09-23" || g["net_hot"] != -42.0 || g["bus_seq"] != 3.0 {
		t.Errorf("game row %v", g)
	}
	if r := s.rows[t10m][0]; r["partial"] != true || r["class"] != "stock" || r["window_start"] != "2026-09-23 05:30:00" {
		t.Errorf("10m row %v (window_start must be UTC)", r)
	}
	if r := s.rows[tFlow][0]; r["class"] != "stock" || r["side"] != "buy" || r["band"] != "hot" {
		t.Errorf("flow row %v", r)
	}
	if r := s.rows[tSnapshots][0]; r["source_time"] != "2026-09-23 05:30:00.000" || r["missing"].([]any)[0] != "book" {
		t.Errorf("snapshot row %v", r)
	}
	q := s.rows[tQuality]
	// DAY_START_MISSED: dedup key is (code, ins_code, day) — Tehran midnight, no detail.
	if q[0]["day"] != "2026-09-23" || q[0]["dedup_at"] != "2026-09-22 20:30:00.000" || q[0]["dedup_detail"] != "" || q[0]["detail"] != "late" {
		t.Errorf("DAY_START_MISSED row %v", q[0])
	}
	if q[1]["dedup_at"] != q[1]["at"] || q[1]["dedup_detail"] != "40s" {
		t.Errorf("STALE row %v: every other issue is keyed on its own time and detail", q[1])
	}
}

func TestDayStartMissedReemissionSameKey(t *testing.T) {
	a := qualityRow(model.QualityIssue{InsCode: "S1", Code: quality.DayStartMissed, Detail: "baseline 09:02", At: tehranAt("09:02")}, 10)
	b := qualityRow(model.QualityIssue{InsCode: "S1", Code: quality.DayStartMissed, Detail: "baseline 09:07 (replay)", At: tehranAt("09:07")}, 99)
	if a.Code != b.Code || a.InsCode != b.InsCode || a.Day != b.Day || a.DedupAt != b.DedupAt || a.DedupDetail != b.DedupDetail {
		t.Errorf("re-emitted DAY_START_MISSED has a different key:\n%+v\n%+v", a, b)
	}
	c := qualityRow(model.QualityIssue{InsCode: "S1", Code: quality.DayStartMissed, At: tehranAt("09:02").AddDate(0, 0, 1)}, 11)
	if c.DedupAt == a.DedupAt || c.Day == a.Day {
		t.Error("the next day's DAY_START_MISSED must be its own row")
	}
}

func TestSyntheticNeverStored(t *testing.T) {
	s := &fakeSink{}
	w := &writer{sink: s}
	at := tehranAt("09:00")
	msgs := []bus.Msg{
		msg(t, "md.snap.SYN1", 1, model.Snapshot{InsCode: "SYN1", Source: "synthetic", SourceTime: at}),
		msg(t, "md.snap.123", 2, model.Snapshot{InsCode: "123", Source: "rebase:replay", SourceTime: at}),
		msg(t, "flow.game.SYN1", 3, model.GameTotals{InsCode: "SYN1", Day: "2026-09-23"}),
	}
	if err := w.handle(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if len(s.rows) != 0 || w.stats.synthetic != 3 {
		t.Errorf("stored %v, synthetic %d", s.rows, w.stats.synthetic)
	}
}

func TestUndecodableRecordedNotBlocking(t *testing.T) {
	s := &fakeSink{}
	w := &writer{sink: s}
	at := tehranAt("09:00")
	msgs := []bus.Msg{
		{Subject: "flow.game.S1", Data: []byte("{not json"), StreamSeq: 1},
		{Subject: "quality.S1", Data: []byte("null"), StreamSeq: 2},
		msg(t, "flow.event.S1", 3, model.FlowEvent{InsCode: "S1", Side: "up", Band: model.BandHot, Attribution: model.Attributed}),
		msg(t, "flow.10m.S2", 4, model.TenMinute{InsCode: "S1", WindowStart: at}),               // subject mismatch
		msg(t, "flow.game.S1", 5, model.GameTotals{InsCode: "S1", Day: "1405/07/01", AsOf: at}), // not a Gregorian day
		msg(t, "flow.10m.S1", 6, model.TenMinute{InsCode: "S1", Class: market.Stock, WindowStart: at}),
	}
	if err := w.handle(context.Background(), msgs); err != nil {
		t.Fatal(err)
	}
	if len(s.rows[t10m]) != 1 || len(s.rows[tQuality]) != 5 || w.stats.undecodable != 5 {
		t.Fatalf("rows %v", s.rows)
	}
	for _, q := range s.rows[tQuality] {
		if q["code"] != quality.Undecodable || !strings.HasPrefix(q["detail"].(string), "writer: ") {
			t.Errorf("row %v", q)
		}
	}
}

func TestInsertFailureRetriedThenLeftUnacked(t *testing.T) {
	at := tehranAt("09:00")
	m := msg(t, "flow.10m.S1", 1, model.TenMinute{InsCode: "S1", WindowStart: at})
	s := &fakeSink{fail: 2}
	w := &writer{sink: s, retry: []time.Duration{time.Millisecond, time.Millisecond}}
	if err := w.handle(context.Background(), []bus.Msg{m}); err != nil || len(s.rows[t10m]) != 1 {
		t.Fatalf("two failures then success: %v %v", err, s.rows)
	}
	s = &fakeSink{fail: 3}
	w = &writer{sink: s, retry: []time.Duration{time.Millisecond, time.Millisecond}}
	if err := w.handle(context.Background(), []bus.Msg{m}); err == nil {
		t.Fatal("persistent failure must return an error (batch stays unacked)")
	}
}
