package model

import "time"

// IndexSnapshot is one observation of a market index (شاخص). Index levels are points, not money:
// they are stored as int64 thousandths of a point (Milli) so no float reaches the bus.
type IndexSnapshot struct {
	Index               string    `json:"index"` // "bourse_total" (شاخص کل بورس)
	Source              string    `json:"source"`
	SourceTime          time.Time `json:"source_time"`
	IngestTime          time.Time `json:"ingest_time"`
	SourceTimeEstimated bool      `json:"source_time_estimated"` // the vendor sent no time: ingest time used
	State               string    `json:"state,omitempty"`       // vendor's market state text (e.g. "open", «بسته»)

	ValueMilli             int64 `json:"value_milli"`        // index level × 1000
	ChangeMilli            int64 `json:"change_milli"`       // change vs yesterday × 1000
	EqualWeightMilli       int64 `json:"equal_weight_milli"` // شاخص هم‌وزن × 1000; 0 with Missing
	EqualWeightChangeMilli int64 `json:"equal_weight_change_milli"`

	Missing []string `json:"missing,omitempty"` // "equal_weight" when the vendor omitted it
}
