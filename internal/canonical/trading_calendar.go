package canonical

import (
	"sort"
	"time"
)

// The trading calendar answers "was the market open on this date". It is
// separate from internal/calendar, which answers "when within a day may this
// instrument class trade" (session hours). Both are needed and neither
// substitutes for the other (docs/trading_calendar.md).
//
// A weekday pattern is never sufficient evidence. Sat-Wed is the ordinary
// Iranian trading week, but public holidays fall on those days and the holiday
// list is unverified, so a Saturday is not a trading day until something shows
// it was.

// Day status values.
const (
	// ObservedTrading: at least one instrument printed a trade. This is proof
	// the market was open and needs no external holiday list.
	ObservedTrading = "OBSERVED_TRADING"
	// PossibleMarketClosure: no provider has data and no instrument traded.
	// A closure is the likely explanation but is NOT confirmed.
	PossibleMarketClosure = "POSSIBLE_MARKET_CLOSURE"
	// ConfirmedClosure: an independent, verified source states the market was shut.
	ConfirmedClosure = "CONFIRMED_CLOSURE"
	// ProviderOutage: the market demonstrably traded but a given provider has
	// no data. This is a provider defect, never a market closure.
	ProviderOutage = "PROVIDER_OUTAGE"
	// WeekendByRule: Thursday or Friday under the weekly rule, with no trading observed.
	WeekendByRule = "WEEKEND_BY_RULE"
	// StatusUndetermined: no evidence either way (outside every archive's range).
	StatusUndetermined = "UNDETERMINED"
)

// Closure reasons. HolidayReason stays empty unless a verified source names it.
const (
	ReasonUnverifiedNoData = "no data in any observed source; reason not established"
	ReasonWeeklyRule       = "Thursday/Friday under the weekly trading rule (rule unverified)"
)

// TradingDay is one calendar date's status.
type TradingDay struct {
	TradeDate     string `json:"trade_date"`
	IsTradingDay  bool   `json:"is_trading_day"`
	ClosureType   string `json:"closure_type"`
	HolidayReason string `json:"holiday_reason,omitempty"`
	Source        string `json:"source"`
	Verified      bool   `json:"verified"`
	// InstrumentsTraded is the evidence behind ObservedTrading.
	InstrumentsTraded int `json:"instruments_traded"`
}

// TradingCalendar is a date-indexed calendar built from evidence.
type TradingCalendar struct {
	days map[string]*TradingDay
}

func NewTradingCalendar() *TradingCalendar {
	return &TradingCalendar{days: map[string]*TradingDay{}}
}

// IsWeekendByRule reports Thursday or Friday. The weekly rule itself is
// unverified, so callers must not treat a false result as "market open".
func IsWeekendByRule(date string) bool {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false
	}
	return t.Weekday() == time.Thursday || t.Weekday() == time.Friday
}

// ObserveTrading records that instrumentsTraded instruments printed trades on a
// date. This is the strongest evidence available and overrides any weaker
// status, because a trade cannot happen on a closed market.
func (c *TradingCalendar) ObserveTrading(date, source string, instrumentsTraded int) {
	if instrumentsTraded <= 0 {
		return
	}
	d := c.get(date)
	d.IsTradingDay = true
	d.ClosureType = ObservedTrading
	d.HolidayReason = ""
	d.Source = source
	d.Verified = true // verified by observation of actual trades
	d.InstrumentsTraded += instrumentsTraded
}

// ObserveNoData records that a source had no data for a date. It never
// downgrades a date on which trading was observed.
func (c *TradingCalendar) ObserveNoData(date, source string) {
	d := c.get(date)
	if d.ClosureType == ObservedTrading || d.ClosureType == ConfirmedClosure {
		return
	}
	d.IsTradingDay = false
	d.Source = source
	d.Verified = false
	if IsWeekendByRule(date) {
		d.ClosureType, d.HolidayReason = WeekendByRule, ReasonWeeklyRule
		return
	}
	d.ClosureType, d.HolidayReason = PossibleMarketClosure, ReasonUnverifiedNoData
}

// MarkProviderOutage records that a provider lacks data for a date on which
// another source observed trading. The market stays open.
func (c *TradingCalendar) MarkProviderOutage(date, provider string) {
	d := c.get(date)
	if d.ClosureType != ObservedTrading {
		return
	}
	d.Source = d.Source + "; outage:" + provider
}

// ConfirmClosure records a verified closure from an independent source. Only
// this promotes a date to a confirmed holiday; archive gaps never do.
func (c *TradingCalendar) ConfirmClosure(date, reason, source string) {
	d := c.get(date)
	d.IsTradingDay = false
	d.ClosureType = ConfirmedClosure
	d.HolidayReason = reason
	d.Source = source
	d.Verified = true
	d.InstrumentsTraded = 0
}

func (c *TradingCalendar) get(date string) *TradingDay {
	if d, ok := c.days[date]; ok {
		return d
	}
	d := &TradingDay{TradeDate: date, ClosureType: StatusUndetermined, Source: "none"}
	c.days[date] = d
	return d
}

// Day returns a date's status.
func (c *TradingCalendar) Day(date string) (TradingDay, bool) {
	d, ok := c.days[date]
	if !ok {
		return TradingDay{}, false
	}
	return *d, true
}

// Days returns every recorded date in ascending order.
func (c *TradingCalendar) Days() []TradingDay {
	out := make([]TradingDay, 0, len(c.days))
	for _, d := range c.days {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TradeDate < out[j].TradeDate })
	return out
}

// TradingDates returns the dates proven to be trading days.
func (c *TradingCalendar) TradingDates() []string {
	var out []string
	for _, d := range c.Days() {
		if d.IsTradingDay {
			out = append(out, d.TradeDate)
		}
	}
	return out
}

// CountsByStatus summarizes the calendar.
func (c *TradingCalendar) CountsByStatus() map[string]int {
	out := map[string]int{}
	for _, d := range c.days {
		out[d.ClosureType]++
	}
	return out
}

// UnverifiedClosures lists dates treated as closed without verification.
// Every one is an open question for the owner, not a holiday.
func (c *TradingCalendar) UnverifiedClosures() []string {
	var out []string
	for _, d := range c.Days() {
		if d.ClosureType == PossibleMarketClosure {
			out = append(out, d.TradeDate)
		}
	}
	return out
}
