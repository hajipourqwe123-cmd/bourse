package bus

import (
	"fmt"
	"testing"
	"time"

	"bourse/internal/model"
)

// Stream sizing (DefaultStreams, contracts/subjects.md): full market ~1500 instruments every
// 5 s = 300 snapshots/s, over the collector's default 4.5 h window (COLLECT_WINDOW
// 08:30-13:00) = 4.86M snapshots per trading day.
const (
	sizingSnapsPerDay    = 300 * 16_200
	sizingMaxStoredBytes = 1_400 // assumed stored bytes per snapshot with a 5-level book
)

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
	daily := float64(sizingSnapsPerDay) * sizingMaxStoredBytes
	for _, s := range DefaultStreams() {
		if s.Name == StreamMD && float64(s.MaxBytes) < 1.2*daily {
			t.Fatalf("MD MaxBytes %d < 1.2 x one full day (%.0f B)", s.MaxBytes, daily)
		}
	}
}
