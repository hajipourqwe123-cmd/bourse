package canonical

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Identity rules (docs/security_master.md):
//
//  1. InstrumentID is the canonical internal identity. It is derived from the
//     TSETMC instrument code (BrsApi `id`), which is stable across renames.
//  2. Ticker is a LABEL. It is reused by the exchange after a delisting and is
//     rewritten on a rename, so it is never an identity key. Resolving a
//     ticker requires a date.
//  3. Attribute changes are versioned (valid_from/valid_to), so a backtest
//     reads the attributes that applied on the day it is simulating.

// Status of an instrument over a validity interval.
const (
	StatusListed    = "listed"
	StatusDelisted  = "delisted"
	StatusRenamed   = "renamed" // identity continues under a new ticker
	StatusMerged    = "merged"  // identity ends; successor carries on
	StatusSuspended = "suspended_long_term"
	StatusInactive  = "inactive"
	StatusUnknown   = "unknown"
)

// Instrument classes derived from the ISIN instrument-type letter.
const (
	ClassOrdinaryShare = "ordinary_share"
	ClassRightsIssue   = "rights_issue"
	ClassFundUnit      = "fund_unit"
	ClassEnergy        = "energy_certificate"
	ClassDebt          = "debt_instrument" // sukuk, treasury bills, participation bonds
	ClassOtherClass    = "other"
)

// Confidence of a derived or externally sourced attribute.
const (
	ConfidenceObserved = "observed" // read directly from a provider field
	ConfidenceDerived  = "derived"  // computed from an observed field by a documented rule
	ConfidenceAssumed  = "assumed"  // placeholder; must be reviewed before research use
)

// Instrument is one version of one instrument's identity. A new version is
// appended whenever a versioned attribute changes; the previous version's
// ValidTo is closed. ValidTo "" means "still current as far as we know".
type Instrument struct {
	InstrumentID    string            `json:"instrument_id"`
	ProviderIDs     map[string]string `json:"provider_ids"` // provider -> its own id for this instrument
	InsCode         string            `json:"ins_code"`
	ISIN            string            `json:"isin"`
	Ticker          string            `json:"ticker"`
	Name            string            `json:"name"`
	CompanyID       string            `json:"company_id"`
	InstrumentClass string            `json:"instrument_class"`
	Market          string            `json:"market"`
	Board           string            `json:"board"`
	IndustryID      string            `json:"industry_id"`
	ValidFrom       string            `json:"valid_from"` // ISO date, inclusive
	ValidTo         string            `json:"valid_to"`   // ISO date, exclusive; "" = open
	ListingDate     string            `json:"listing_date"`
	DelistingDate   string            `json:"delisting_date"`
	PreviousTicker  string            `json:"previous_ticker"`
	Status          string            `json:"status"`
	IdentitySource  string            `json:"identity_source"`
	Confidence      string            `json:"confidence"`
	// Ambiguities records identity questions this record could not settle.
	// It is never empty-by-assumption: an unresolved question stays visible.
	Ambiguities []string `json:"ambiguities,omitempty"`
}

// Covers reports whether this version applies on an ISO date.
func (i Instrument) Covers(date string) bool {
	if i.ValidFrom != "" && date < i.ValidFrom {
		return false
	}
	return i.ValidTo == "" || date < i.ValidTo
}

// ISIN layout for Iranian instruments: "IR" + instrument-type letter +
// board/market character + 4-char company code + 4-char serial. Equities use a
// numeric serial (IRO1FOLD0001, IRR3ARFZ0101); debt instruments may use an
// alphanumeric one (IRB3TR3307B1), so the serial is not restricted to digits.
var isinRe = regexp.MustCompile(`^IR([A-Z])([0-9A-Z])([A-Z0-9]{4})([0-9A-Z]{4})$`)

// ISINParts is the decomposition of an Iranian ISIN. Every field is derived,
// not observed: the provider supplies only the ISIN string.
type ISINParts struct {
	TypeLetter  string
	BoardDigit  string
	CompanyCode string
	Serial      string
	Class       string
}

// ParseISIN decomposes an Iranian ISIN. The company code is what links a
// rights issue to its parent share and a secondary listing to its primary.
func ParseISIN(isin string) (ISINParts, error) {
	m := isinRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(isin)))
	if m == nil {
		return ISINParts{}, fmt.Errorf("ISIN %q does not match the Iranian layout", isin)
	}
	p := ISINParts{TypeLetter: m[1], BoardDigit: m[2], CompanyCode: m[3], Serial: m[4]}
	switch m[1] {
	case "O":
		p.Class = ClassOrdinaryShare
	case "R":
		p.Class = ClassRightsIssue
	case "T":
		p.Class = ClassFundUnit
	case "E":
		p.Class = ClassEnergy
	case "B":
		p.Class = ClassDebt
	default:
		p.Class = ClassOtherClass
	}
	return p, nil
}

// Master is a point-in-time security master.
type Master struct {
	versions map[string][]Instrument // instrument_id -> versions sorted by valid_from
}

func NewMaster() *Master { return &Master{versions: map[string][]Instrument{}} }

// Add appends an identity version. It rejects a record without a canonical
// identity, because an unidentified row must never silently become a universe
// member.
func (m *Master) Add(in Instrument) error {
	if in.InstrumentID == "" {
		return fmt.Errorf("instrument has no instrument_id (ticker %q)", in.Ticker)
	}
	vs := append(m.versions[in.InstrumentID], in)
	sort.SliceStable(vs, func(a, b int) bool { return vs[a].ValidFrom < vs[b].ValidFrom })
	m.versions[in.InstrumentID] = vs
	return nil
}

// At returns the identity version of an instrument that applied on a date.
func (m *Master) At(instrumentID, date string) (Instrument, bool) {
	for _, v := range m.versions[instrumentID] {
		if v.Covers(date) {
			return v, true
		}
	}
	return Instrument{}, false
}

// IDs returns every instrument_id, sorted.
func (m *Master) IDs() []string {
	out := make([]string, 0, len(m.versions))
	for id := range m.versions {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Len is the number of distinct instruments.
func (m *Master) Len() int { return len(m.versions) }

// ResolveTicker maps a ticker to instrument_ids that carried it on a date.
// It returns every match: more than one is a ticker collision and the caller
// must not guess. This is why price history is never keyed by ticker.
func (m *Master) ResolveTicker(ticker, date string) []string {
	var out []string
	for id, vs := range m.versions {
		for _, v := range vs {
			if v.Ticker == ticker && v.Covers(date) {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// TickerCollisions reports tickers held by more than one instrument on a date.
func (m *Master) TickerCollisions(date string) map[string][]string {
	byTicker := map[string][]string{}
	for id, vs := range m.versions {
		for _, v := range vs {
			if v.Covers(date) && v.Ticker != "" {
				byTicker[v.Ticker] = append(byTicker[v.Ticker], id)
				break
			}
		}
	}
	out := map[string][]string{}
	for t, ids := range byTicker {
		if len(ids) > 1 {
			sort.Strings(ids)
			out[t] = ids
		}
	}
	return out
}

// Universe returns the instrument_ids whose identity version on a date has one
// of the given statuses. A backtest samples this, never the current listing.
func (m *Master) Universe(date string, statuses ...string) []string {
	want := map[string]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	var out []string
	for id, vs := range m.versions {
		for _, v := range vs {
			if v.Covers(date) && (len(want) == 0 || want[v.Status]) {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// CompanyGroup returns the instrument_ids sharing a company code on a date:
// the parent share, its secondary listings and any rights issue.
func (m *Master) CompanyGroup(companyID, date string) []string {
	var out []string
	for id, vs := range m.versions {
		for _, v := range vs {
			if v.CompanyID == companyID && v.Covers(date) {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
