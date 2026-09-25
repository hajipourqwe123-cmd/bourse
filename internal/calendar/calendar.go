// Package calendar is the instrument-level trading-session calendar (DL-01). Sessions, holidays
// and the instrument → class mapping are DATA (sessions.json, embedded; SESSIONS_FILE overrides);
// nothing here hard-codes an hour. Rules are dated (effective_from), so a replay or backtest of
// an old day uses the calendar that applied on that day.
//
// All times are Tehran local. A session never crosses midnight, so a trading day is the Tehran
// calendar date. Instruments without a mapping are class Unknown: their session is the union of
// every class trading that day (earliest pre-open/open, latest close).
package calendar

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"bourse/internal/tehran"
)

// Unknown is the class of instruments that are not mapped in the calendar.
const Unknown = "unknown"

//go:embed sessions.json
var embedded []byte

var weekdays = map[string]time.Weekday{"sat": time.Saturday, "sun": time.Sunday, "mon": time.Monday,
	"tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday}

type fileRule struct {
	EffectiveFrom string   `json:"effective_from"`
	Days          []string `json:"days"`
	PreOpen       string   `json:"pre_open"`
	Open          string   `json:"open"`
	Close         string   `json:"close"`
	Verified      bool     `json:"verified"`
	Source        string   `json:"source"`
}

type fileClass struct {
	LabelFa string     `json:"label_fa"`
	Rules   []fileRule `json:"rules"`
}

type fileHoliday struct {
	Date     string `json:"date"`
	Name     string `json:"name"`
	Verified bool   `json:"verified"`
}

type file struct {
	DefaultClass string               `json:"default_class"`
	Classes      map[string]fileClass `json:"classes"`
	Instruments  map[string]string    `json:"instruments"`
	Holidays     []fileHoliday        `json:"holidays"`
}

type rule struct {
	from                 string // YYYY-MM-DD, inclusive
	days                 map[time.Weekday]bool
	preOpen, open, close time.Duration // since Tehran midnight
	verified             bool
}

// Calendar answers "is this instrument's market open, and when" for any date.
type Calendar struct {
	classes      map[string][]rule // sorted by from, ascending
	instruments  map[string]string
	holidays     map[string]string
	defaultClass string
	unverified   int
}

// Session is one instrument's (or class's) session on one trading day, as absolute times.
type Session struct {
	Class                string
	Day                  string // Tehran trading day, YYYY-MM-DD
	PreOpen, Open, Close time.Time
	Verified             bool // every rule it is built from is verified against an official source
}

// Contains reports t in [PreOpen, Close): the instrument may produce or change data.
func (s Session) Contains(t time.Time) bool { return !t.Before(s.PreOpen) && t.Before(s.Close) }

// Trading reports t in [Open, Close): continuous trading.
func (s Session) Trading(t time.Time) bool { return !t.Before(s.Open) && t.Before(s.Close) }

// WindowStart returns the start of the session-relative 10-minute window containing t:
// Open + ⌊(t − Open) / 10m⌋ · 10m (windows before the open are allowed and count backwards).
func WindowStart(s Session, t time.Time) time.Time {
	d := t.Sub(s.Open)
	k := d / (10 * time.Minute)
	if d < 0 && d%(10*time.Minute) != 0 {
		k--
	}
	return s.Open.Add(k * 10 * time.Minute)
}

// Default returns the embedded calendar. It panics if the embedded file is invalid (a test
// guards that).
func Default() *Calendar {
	c, err := Parse(embedded)
	if err != nil {
		panic("calendar: embedded sessions.json: " + err.Error())
	}
	return c
}

// Load reads path, or returns the embedded calendar when path is empty.
func Load(path string) (*Calendar, error) {
	if path == "" {
		return Default(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}
	return Parse(b)
}

func parseHM(v string) (time.Duration, error) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return 0, fmt.Errorf("time %q: want HH:MM", v)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// Parse validates and builds a calendar from JSON.
func Parse(b []byte) (*Calendar, error) {
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("calendar: %w", err)
	}
	c := &Calendar{classes: map[string][]rule{}, instruments: map[string]string{}, holidays: map[string]string{},
		defaultClass: f.DefaultClass}
	if len(f.Classes) == 0 {
		return nil, fmt.Errorf("calendar: no classes")
	}
	for name, fc := range f.Classes {
		if name == Unknown {
			return nil, fmt.Errorf("calendar: class name %q is reserved", Unknown)
		}
		if len(fc.Rules) == 0 {
			return nil, fmt.Errorf("calendar: class %s has no rules", name)
		}
		seen := map[string]bool{}
		for _, fr := range fc.Rules {
			if _, err := time.Parse("2006-01-02", fr.EffectiveFrom); err != nil {
				return nil, fmt.Errorf("calendar: class %s: effective_from %q: want YYYY-MM-DD", name, fr.EffectiveFrom)
			}
			if seen[fr.EffectiveFrom] {
				return nil, fmt.Errorf("calendar: class %s: two rules effective from %s", name, fr.EffectiveFrom)
			}
			seen[fr.EffectiveFrom] = true
			r := rule{from: fr.EffectiveFrom, days: map[time.Weekday]bool{}, verified: fr.Verified}
			for _, d := range fr.Days {
				wd, ok := weekdays[strings.ToLower(d)]
				if !ok {
					return nil, fmt.Errorf("calendar: class %s: day %q", name, d)
				}
				r.days[wd] = true
			}
			var err error
			if r.preOpen, err = parseHM(fr.PreOpen); err != nil {
				return nil, fmt.Errorf("calendar: class %s: pre_open: %w", name, err)
			}
			if r.open, err = parseHM(fr.Open); err != nil {
				return nil, fmt.Errorf("calendar: class %s: open: %w", name, err)
			}
			if r.close, err = parseHM(fr.Close); err != nil {
				return nil, fmt.Errorf("calendar: class %s: close: %w", name, err)
			}
			if r.preOpen > r.open || r.open >= r.close {
				return nil, fmt.Errorf("calendar: class %s from %s: want pre_open <= open < close (same day)", name, fr.EffectiveFrom)
			}
			if !r.verified {
				c.unverified++
			}
			c.classes[name] = append(c.classes[name], r)
		}
		sort.Slice(c.classes[name], func(i, j int) bool { return c.classes[name][i].from < c.classes[name][j].from })
	}
	if c.defaultClass == "" {
		c.defaultClass = Unknown
	}
	if _, ok := c.classes[c.defaultClass]; !ok && c.defaultClass != Unknown {
		return nil, fmt.Errorf("calendar: default_class %q is not a class", c.defaultClass)
	}
	for ins, cl := range f.Instruments {
		if _, ok := c.classes[cl]; !ok {
			return nil, fmt.Errorf("calendar: instrument %s: unknown class %q", ins, cl)
		}
		c.instruments[ins] = cl
	}
	for _, h := range f.Holidays {
		if _, err := time.Parse("2006-01-02", h.Date); err != nil {
			return nil, fmt.Errorf("calendar: holiday %q: want YYYY-MM-DD", h.Date)
		}
		c.holidays[h.Date] = h.Name
		if !h.Verified {
			c.unverified++
		}
	}
	return c, nil
}

// Unverified is the number of rules and holidays not yet checked against an official source.
func (c *Calendar) Unverified() int { return c.unverified }

// WithInstruments returns a copy with extra ins_code → class mappings (tests, tools).
func (c *Calendar) WithInstruments(m map[string]string) *Calendar {
	cp := *c
	cp.instruments = make(map[string]string, len(c.instruments)+len(m))
	for k, v := range c.instruments {
		cp.instruments[k] = v
	}
	for k, v := range m {
		cp.instruments[k] = v
	}
	return &cp
}

// Class returns the instrument's class (Unknown when it is not mapped and no default applies).
func (c *Calendar) Class(ins string) string {
	if cl, ok := c.instruments[ins]; ok {
		return cl
	}
	return c.defaultClass
}

// Mapped reports whether the instrument has an explicit class.
func (c *Calendar) Mapped(ins string) bool {
	_, ok := c.instruments[ins]
	return ok
}

// Session returns the instrument's session on the Tehran trading day of t; false when its market
// is closed that day (weekday or holiday).
func (c *Calendar) Session(ins string, t time.Time) (Session, bool) {
	return c.ClassSession(c.Class(ins), t)
}

// ClassSession is Session for a class; Unknown gives the union of all classes trading that day.
func (c *Calendar) ClassSession(class string, t time.Time) (Session, bool) {
	day := tehran.DayStart(t)
	date := day.Format("2006-01-02")
	if _, holiday := c.holidays[date]; holiday {
		return Session{}, false
	}
	if class == Unknown {
		return c.union(day, date)
	}
	r, ok := c.ruleOn(class, date, day.Weekday())
	if !ok {
		return Session{}, false
	}
	return Session{Class: class, Day: date, PreOpen: day.Add(r.preOpen), Open: day.Add(r.open), Close: day.Add(r.close),
		Verified: r.verified}, true
}

func (c *Calendar) ruleOn(class, date string, wd time.Weekday) (rule, bool) {
	rules := c.classes[class]
	i := sort.Search(len(rules), func(i int) bool { return rules[i].from > date }) - 1
	if i < 0 || !rules[i].days[wd] {
		return rule{}, false
	}
	return rules[i], true
}

func (c *Calendar) union(day time.Time, date string) (Session, bool) {
	var u Session
	found := false
	for name := range c.classes {
		r, ok := c.ruleOn(name, date, day.Weekday())
		if !ok {
			continue
		}
		s := Session{PreOpen: day.Add(r.preOpen), Open: day.Add(r.open), Close: day.Add(r.close), Verified: r.verified}
		if !found {
			u, found = s, true
			continue
		}
		if s.PreOpen.Before(u.PreOpen) {
			u.PreOpen = s.PreOpen
		}
		if s.Open.Before(u.Open) {
			u.Open = s.Open
		}
		if s.Close.After(u.Close) {
			u.Close = s.Close
		}
		u.Verified = u.Verified && s.Verified
	}
	u.Class, u.Day = Unknown, date
	return u, found
}

// Union returns the span in which any class may produce data on t's trading day (the collector's
// polling window); false when no class trades that day.
func (c *Calendar) Union(t time.Time) (Session, bool) {
	day := tehran.DayStart(t)
	date := day.Format("2006-01-02")
	if _, holiday := c.holidays[date]; holiday {
		return Session{}, false
	}
	return c.union(day, date)
}
