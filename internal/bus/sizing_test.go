package bus

import (
	"fmt"
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/model"
)

// Stream sizing (DefaultStreams, contracts/subjects.md): full market ~1500 instruments every
// 5 s = 300 snapshots/s, published at worst across the whole union of the session calendar
// (08:25–18:00 on a normal trading day, derived from the calendar, not a literal).
const (
	sizingSnapsPerSecond = 1500 / 5
	sizingMaxStoredBytes = 1_400 // assumed stored bytes per snapshot with a 5-level book
)

func sizingSnapsPerDay(t *testing.T) float64 {
	t.Helper()
	u, open := calendar.Default().Union(time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)) // a Saturday
	if !open {
		t.Fatal("no session on a Saturday")
	}
	return sizingSnapsPerSecond * u.Close.Sub(u.PreOpen).Seconds()
}

// A realistic worst-case snapshot: 17-digit TSETMC code, Persian symbol, full-size numbers and
// a 5-level book, published with the collector's message-ID header. The measured stored size
// must stay within the assumption, and MD must hold >= 1.2 full days at that size.
func TestStreamSizingAssumption(t *testing.T) {
	j := connect(t, DefaultStreams())
	now := time.Date(2026, 9, 23, 6, 30, 0, 123456789, time.UTC)
	book := make([]model.Level, 5)
	for i := range book {
		book[i] = model.Level{BidPrice: 123_450 - int64(i)*10, BidVol: 12_345_678, BidCount: 1_234,
			AskPrice: 123_460 + int64(i)*10, AskVol: 12_345_678, AskCount: 1_234}
	}
	const n = 200
	for i := 0; i < n; i++ {
		s := model.Snapshot{InsCode: fmt.Sprintf("463485591932240%02d", i%100), Symbol: "فولادهرمز", Source: "sourcearena",
			SourceTime: now.Add(time.Duration(i) * time.Second), IngestTime: now.Add(time.Duration(i)*time.Second + 987654321),
			PriceLast: 123_456, PriceClose: 123_450, PriceFirst: 120_000, PriceYesterday: 119_990, PriceMin: 118_000, PriceMax: 125_000,
			TradeCount: 12_345, Volume: 987_654_321, Value: 121_932_631_112_635, IndBuyVol: 543_210_987, IndSellVol: 612_345_678,
			InstBuyVol: 444_443_334, InstSellVol: 375_308_643, IndBuyCount: 23_456, IndSellCount: 19_876, InstBuyCount: 123, InstSellCount: 98,
			Book: book}
		id := fmt.Sprintf("snap:%s:%d:%d", s.InsCode, s.SourceTime.UnixNano(), s.IngestTime.UnixNano())
		if err := j.PublishID(SubjSnapshot(s.InsCode), id, s); err != nil {
			t.Fatal(err)
		}
	}
	st := streamInfo(t, j, StreamMD).State
	perMsg := float64(st.Bytes) / float64(st.Msgs)
	t.Logf("measured %.0f stored bytes per full-size snapshot with a 5-level book", perMsg)
	if perMsg > sizingMaxStoredBytes {
		t.Fatalf("stored snapshot is %.0f B, above the %d B the sizing assumes: resize MD", perMsg, sizingMaxStoredBytes)
	}
	daily := sizingSnapsPerDay(t) * sizingMaxStoredBytes
	t.Logf("worst-case MD volume: %.2fM snapshots, %.1f GB per trading day", sizingSnapsPerDay(t)/1e6, daily/1e9)
	for _, s := range DefaultStreams() {
		if s.Name == StreamMD && float64(s.MaxBytes) < 1.2*daily {
			t.Fatalf("MD MaxBytes %d < 1.2 x one full day (%.0f B)", s.MaxBytes, daily)
		}
	}
}

// storedPerMsg publishes n copies of v (with an engine-style message ID) and returns the stored
// bytes per message.
func storedPerMsg(t *testing.T, j *JetStream, stream, subject string, v any, n int) float64 {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := j.PublishID(subject, fmt.Sprintf("eng:1790316029123456789:%d:%d", 1_000_000+i, i%9), v); err != nil {
			t.Fatal(err)
		}
	}
	st := streamInfo(t, j, stream).State
	return float64(st.Bytes) / float64(st.Msgs)
}

// FLOW and QUALITY are sized from the same worst-case day: at most 2 flow outputs per snapshot
// (measured ≈ 2.0 on the demo day) and, for QUALITY, one issue per snapshot for the whole day.
func TestFlowAndQualitySizing(t *testing.T) {
	j := connect(t, DefaultStreams())
	now := time.Date(2026, 9, 23, 6, 30, 0, 0, time.UTC)
	game := &model.GameTotals{InsCode: "46348559193224090", Class: "fixed_income", Day: "2026-09-23",
		NetHot: -123_456_789_012, NetHotPlus: 98_765_432_109, NetRetail: -12_345_678_901, NetUnattributed: 1_234_567_890, Partial: true}
	win := &model.TenMinute{InsCode: "46348559193224090", Class: "fixed_income", WindowStart: now, NetHot: -123_456_789_012,
		PriceOpen: 123_456, PriceLastV: 123_999, Partial: true}
	iss := model.QualityIssue{InsCode: "46348559193224090", Code: "DAY_START_MISSED", At: now,
		Detail: "first accepted snapshot of 2026-09-23 at 09:05:00 already has day volume 987654321: trades before it are not counted; day totals and this 10-minute window are partial"}
	storedPerMsg(t, j, StreamFlow, SubjGame("46348559193224090"), game, 100)
	flowPer := storedPerMsg(t, j, StreamFlow, SubjWindow("46348559193224090"), win, 100) // mean over both kinds
	qualPer := storedPerMsg(t, j, StreamQuality, SubjQuality("46348559193224090"), iss, 100)
	t.Logf("measured stored bytes: flow output %.0f, quality issue %.0f", flowPer, qualPer)
	const flowAssumed, qualAssumed = 400, 450
	if flowPer > flowAssumed || qualPer > qualAssumed {
		t.Fatalf("stored sizes exceed the sizing assumptions (%d / %d B): resize", flowAssumed, qualAssumed)
	}
	snaps := sizingSnapsPerDay(t)
	for _, s := range DefaultStreams() {
		var daily float64
		switch s.Name {
		case StreamFlow:
			daily = snaps * 2 * flowAssumed
		case StreamQuality:
			daily = snaps * qualAssumed
		default:
			continue
		}
		if float64(s.MaxBytes) < 1.2*daily {
			t.Errorf("%s MaxBytes %d < 1.2 x worst-case day (%.1f GB)", s.Name, s.MaxBytes, daily/1e9)
		}
	}
}
