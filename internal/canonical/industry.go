package canonical

import (
	"fmt"
	"sort"
)

// Temporal industry membership (docs/historical_industry_membership.md).
//
// Today's industry code is evidence about today only. Backfilling it over
// history would silently assert that an instrument was always in that sector,
// which invalidates any sector-rotation study: reclassifications and new
// sub-industries would be invisible, and the study would "know" a membership
// that was not knowable at the time.
//
// So a membership row is always dated, always carries its source and
// confidence, and Membership(id, date) returns false rather than guessing when
// the date is outside what was observed.

// Membership is one instrument's industry over a validity interval.
type IndustryMembership struct {
	InstrumentID string `json:"instrument_id"`
	IndustryID   string `json:"industry_id"`
	IndustryName string `json:"industry_name,omitempty"`
	ValidFrom    string `json:"valid_from"` // inclusive ISO date
	ValidTo      string `json:"valid_to"`   // exclusive ISO date; "" = open
	Source       string `json:"source"`
	Confidence   string `json:"confidence"` // observed | derived | assumed
}

// Covers reports whether this row applies on a date.
func (m IndustryMembership) Covers(date string) bool {
	if m.ValidFrom != "" && date < m.ValidFrom {
		return false
	}
	return m.ValidTo == "" || date < m.ValidTo
}

// IndustryTable holds dated memberships.
type IndustryTable struct {
	rows map[string][]IndustryMembership
}

func NewIndustryTable() *IndustryTable {
	return &IndustryTable{rows: map[string][]IndustryMembership{}}
}

// Add appends a dated membership. A row without a ValidFrom is rejected:
// an undated membership is exactly the backfill this table exists to prevent.
func (t *IndustryTable) Add(m IndustryMembership) error {
	if m.InstrumentID == "" || m.IndustryID == "" {
		return fmt.Errorf("membership needs instrument_id and industry_id")
	}
	if m.ValidFrom == "" {
		return fmt.Errorf("membership for %s has no valid_from: undated membership would be a backfill", m.InstrumentID)
	}
	if m.Source == "" || m.Confidence == "" {
		return fmt.Errorf("membership for %s needs a source and confidence", m.InstrumentID)
	}
	rs := append(t.rows[m.InstrumentID], m)
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].ValidFrom < rs[b].ValidFrom })
	t.rows[m.InstrumentID] = rs
	return nil
}

// At returns the membership that applied on a date. The false return means
// "not known for that date" and callers must exclude the instrument from
// sector aggregates rather than substituting a current value.
func (t *IndustryTable) At(instrumentID, date string) (IndustryMembership, bool) {
	for _, m := range t.rows[instrumentID] {
		if m.Covers(date) {
			return m, true
		}
	}
	return IndustryMembership{}, false
}

// Members returns the instrument_ids in an industry on a date.
func (t *IndustryTable) Members(industryID, date string) []string {
	var out []string
	for id, rs := range t.rows {
		for _, m := range rs {
			if m.IndustryID == industryID && m.Covers(date) {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Len is the number of instruments with at least one dated membership.
func (t *IndustryTable) Len() int { return len(t.rows) }

// KnownCoverage reports how many of the given instruments have a membership on
// a date. Sector research needs this number published alongside any result.
func (t *IndustryTable) KnownCoverage(instrumentIDs []string, date string) (known int, ratio float64) {
	for _, id := range instrumentIDs {
		if _, ok := t.At(id, date); ok {
			known++
		}
	}
	if len(instrumentIDs) == 0 {
		return 0, 0
	}
	return known, round4(float64(known) / float64(len(instrumentIDs)))
}
