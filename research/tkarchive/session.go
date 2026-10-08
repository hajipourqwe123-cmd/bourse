package tkarchive

import (
	"strings"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/tehran"
)

// Session-completeness states (docs/market_session_calendar.md). They describe when a
// capture was taken relative to the market day's session for an instrument class.
// They never come from the shape of a payload (e.g. its interval count).
const (
	// PostSessionDay: captured on a later Tehran day; every class's session for the
	// market day had ended (sessions never cross midnight). Provider finality is
	// still not proven.
	PostSessionDay = "POST_SESSION_DAY"
	// SessionClosedAtCapture: same-day capture after the class's verified close.
	SessionClosedAtCapture = "SESSION_CLOSED_AT_CAPTURE"
	// PartialIntraday: same-day capture before the class's verified close.
	PartialIntraday = "PARTIAL_INTRADAY"
	// SessionUnknown: the session cannot be established (class unmapped, rule
	// unverified, no rule for the day, or capture time unknown).
	SessionUnknown = "SESSION_UNKNOWN"
)

// Closed reports whether a completeness state allows the item to count as a
// whole-day observation.
func Closed(state string) bool { return state == PostSessionDay || state == SessionClosedAtCapture }

// SessionCompleteness classifies a capture of marketDate (YYYY-MM-DD, Tehran) taken
// at captured for an instrument class. A multi-class dataset without per-row class
// passes calendar.Unknown, whose session is the union of all classes.
func SessionCompleteness(cal *calendar.Calendar, class, marketDate string, captured time.Time) string {
	if captured.IsZero() {
		return SessionUnknown
	}
	capDay := tehran.TradingDay(captured)
	switch {
	case capDay > marketDate:
		return PostSessionDay
	case capDay < marketDate:
		return SessionUnknown // captured before the market day: not an observation of it
	}
	s, ok := cal.ClassSession(class, captured)
	if !ok || !s.Verified {
		return SessionUnknown
	}
	if captured.Before(s.Close) {
		return PartialIntraday
	}
	return SessionClosedAtCapture
}

// EntryCompleteness is the completeness of an archived item: an explicit
// annotation wins, otherwise it is derived from retrieved_at with the embedded
// calendar. Both tablokhani datasets mix instrument classes without a class field.
func EntryCompleteness(cal *calendar.Calendar, e *Entry) string {
	if e.Completeness != "" {
		return e.Completeness
	}
	t, err := time.Parse(time.RFC3339, e.RetrievedAt)
	if err != nil {
		return SessionUnknown
	}
	return SessionCompleteness(cal, calendar.Unknown, strings.SplitN(e.MarketDate, "@", 2)[0], t)
}
