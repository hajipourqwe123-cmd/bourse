package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type row map[string]json.RawMessage

var (
	reInt          = regexp.MustCompile(`^-?\d+$`)
	reDec          = regexp.MustCompile(`^-?\d+\.\d+$`)
	rePersianDigit = regexp.MustCompile(`[۰-۹]`)
)

func parseRows(body []byte) ([]row, error) {
	var rows []row
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("not a JSON array of objects: %w", err)
	}
	return rows, nil
}

// text returns a field as trimmed text ("" when absent or null).
func (r row) text(k string) string {
	raw, ok := r[k]
	if !ok || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

type valState int

const (
	valAbsent valState = iota // absent, null, "" or "-"
	valBad                    // present but not an integer (fraction, overflow, text)
	valOK
)

// int reads an integer given as a JSON number or string; thousands separators and a zero
// fraction ("71690.00") are accepted, any other fraction, overflow or text is not an integer.
func (r row) int(k string) (int64, bool) {
	v, st := r.parseInt(k)
	return v, st == valOK
}

func (r row) parseInt(k string) (int64, valState) {
	s := strings.ReplaceAll(r.text(k), ",", "")
	if s == "" || s == "-" {
		return 0, valAbsent
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && i > 0 && strings.Trim(s[i+1:], "0") == "" {
		s = s[:i]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, valBad
	}
	return v, valOK
}

// value is a field as comparable text: normalised text, or the integer in decimal.
func (r row) value(k string, text bool) (string, valState) {
	if text {
		if t := normName(r.text(k)); t != "" && t != "-" {
			return t, valOK
		}
		return "", valAbsent
	}
	v, st := r.parseInt(k)
	return strconv.FormatInt(v, 10), st
}

// format classifies how a vendor encodes a field: number, numeric string, decimal string, …
func (r row) format(k string) string {
	raw, ok := r[k]
	switch {
	case !ok:
		return "absent"
	case string(raw) == "null":
		return "null"
	case len(raw) > 0 && raw[0] == '"':
		s := r.text(k)
		switch {
		case s == "":
			return "empty string"
		case reInt.MatchString(s):
			return "numeric string"
		case reInt.MatchString(strings.ReplaceAll(s, ",", "")):
			return "numeric string with separators"
		case reDec.MatchString(s):
			return "decimal string"
		case rePersianDigit.MatchString(s):
			return "Persian digits"
		}
		return "text"
	case strings.ContainsAny(string(raw), ".eE"):
		return "decimal number"
	}
	return "integer number"
}

// normName folds Arabic yeh/kaf and removes spaces and ZWNJ so symbols compare across vendors.
func normName(s string) string {
	return strings.NewReplacer("ي", "ی", "ى", "ی", "ك", "ک", "\u200c", "", " ", "", "\u200f", "").Replace(strings.TrimSpace(s))
}

// field pairs one canonical field with each vendor's key.
type field struct {
	Group, Name, Brs, Sa string
	Text                 bool // compare normalised text, not integers
}

func fields() []field {
	f := []field{
		{"شناسه", "symbol", "l18", "name", true},
		{"شناسه", "isin", "isin", "namad_code", true},
		{"شناسه", "sector_code", "cs_id", "industry_code", false},
		{"قیمت", "price_last", "pl", "close_price", false},
		{"قیمت", "price_close", "pc", "final_price", false},
		{"قیمت", "price_first", "pf", "first_price", false},
		{"قیمت", "price_yesterday", "py", "yesterday_price", false},
		{"قیمت", "price_min", "pmin", "lowest_price", false},
		{"قیمت", "price_max", "pmax", "highest_price", false},
		{"دامنه", "price_limit_min", "tmin", "daily_price_low", false},
		{"دامنه", "price_limit_max", "tmax", "daily_price_high", false},
		{"جمع روز", "trade_count", "tno", "trade_number", false},
		{"جمع روز", "volume", "tvol", "trade_volume", false},
		{"جمع روز", "value", "tval", "trade_value", false},
		{"حقیقی/حقوقی", "ind_buy_vol", "Buy_I_Volume", "real_buy_volume", false},
		{"حقیقی/حقوقی", "inst_buy_vol", "Buy_N_Volume", "co_buy_volume", false},
		{"حقیقی/حقوقی", "ind_sell_vol", "Sell_I_Volume", "real_sell_volume", false},
		{"حقیقی/حقوقی", "inst_sell_vol", "Sell_N_Volume", "co_sell_volume", false},
		{"حقیقی/حقوقی", "ind_buy_count", "Buy_CountI", "real_buy_count", false},
		{"حقیقی/حقوقی", "inst_buy_count", "Buy_CountN", "co_buy_count", false},
		{"حقیقی/حقوقی", "ind_sell_count", "Sell_CountI", "real_sell_count", false},
		{"حقیقی/حقوقی", "inst_sell_count", "Sell_CountN", "co_sell_count", false},
	}
	for i := 1; i <= 5; i++ {
		n := strconv.Itoa(i)
		f = append(f,
			field{"دفتر", "bid" + n + "_price", "pd" + n, n + "_buy_price", false},
			field{"دفتر", "bid" + n + "_vol", "qd" + n, n + "_buy_volume", false},
			field{"دفتر", "bid" + n + "_count", "zd" + n, n + "_buy_count", false},
			field{"دفتر", "ask" + n + "_price", "po" + n, n + "_sell_price", false},
			field{"دفتر", "ask" + n + "_vol", "qo" + n, n + "_sell_volume", false},
			field{"دفتر", "ask" + n + "_count", "zo" + n, n + "_sell_count", false})
	}
	return append(f,
		field{"سایر", "market_value", "mv", "market_value", false},
		field{"سایر", "eps", "eps", "eps", false})
}

type pair struct {
	Brs, Sa row
	ByName  bool // joined by symbol: one side had no instrument code
}

// FieldStat is the comparison of one field over the joined rows. Every pair lands in exactly
// one of Match, Mismatch, OnlyBrs, OnlySa, Neither or BothBad.
type FieldStat struct {
	field
	Both         int      // both sides parsed
	Match        int      // both parsed and equal
	NonZeroMatch int      // equal and not 0 (0 is also the vendors' "nothing": empty book level, untraded)
	Mismatch     int      // both parsed and different, or one side unparseable next to a parsed value
	OnlyBrs      int      // BrsApi has a value, SourceArena none (absent, null, "" or "-")
	OnlySa       int      // the reverse
	Neither      int      // no value on either side
	BothBad      int      // unparseable on at least one side and no parsed value on the other
	BadBrs       int      // BrsApi value present but not an integer (fraction, overflow, text)
	BadSa        int      // the same for SourceArena
	Examples     []string // up to 3 mismatches
	BrsFmt       map[string]int
	SaFmt        map[string]int
}

// Report is everything the markdown shows.
type Report struct {
	BrsAt, SaAt, Note       string // file or fetch time, and optional context
	BrsDataAt, SaDataAt     string // latest time found in the payload itself
	BrsRows, SaRows         int
	Pairs                   []pair
	OnlyBrs, OnlySa         []row
	NameTried               int      // rows for which the symbol fallback was attempted
	Ambiguous               []string // symbols matching several unjoined rows
	NameIDConflict          []string // same symbol, both sides coded, different codes (not joined)
	DupCodeBrs, DupCodeSa   []string // instrument codes on more than one row (not joined by code)
	DupSymBrs, DupSymSa     int      // normalised symbols on more than one row, per side
	Stats                   []FieldStat
	Classes                 []classCount
	ClassDiff               map[string]int // "BrsApi class → SourceArena class" of joined pairs that differ
	OnlyBrsKeys, OnlySaKeys []string
}

type classCount struct {
	Class               string
	Brs, Sa, JoinedSame int // JoinedSame: joined pairs both vendors put in this class
}

func brsCode(r row) string { return code(r.text("id")) }
func saCode(r row) string  { return code(r.text("instance_code")) }

// code keeps a digits-only instrument code ("" otherwise).
func code(s string) string {
	if reInt.MatchString(s) && !strings.HasPrefix(s, "-") {
		return s
	}
	return ""
}

// compare joins BrsApi rows (id) and SourceArena rows (instance_code). Pass 1 joins by code,
// codes repeated on a side are reported and not used. Pass 2 joins the rows left by normalised
// symbol, only when the symbol is unique among the rows left on both sides and at least one of
// the two rows has no code: two different codes are two instruments (NameIDConflict).
func compare(brs, sa []row) *Report {
	rep := &Report{BrsRows: len(brs), SaRows: len(sa), ClassDiff: map[string]int{}}
	usedB, usedS := make([]bool, len(brs)), make([]bool, len(sa))
	idxB, dupB := index(brs, brsCode)
	idxS, dupS := index(sa, saCode)
	rep.DupCodeBrs, rep.DupCodeSa = dupB, dupS
	for c, i := range idxB {
		if j, ok := idxS[c]; ok {
			rep.Pairs = append(rep.Pairs, pair{Brs: brs[i], Sa: sa[j]})
			usedB[i], usedS[j] = true, true
		}
	}
	sort.Slice(rep.Pairs, func(a, b int) bool { return brsCode(rep.Pairs[a].Brs) < brsCode(rep.Pairs[b].Brs) })
	leftB, leftS := map[string][]int{}, map[string][]int{}
	for i, r := range brs {
		if !usedB[i] {
			n := normName(r.text("l18"))
			leftB[n] = append(leftB[n], i)
		}
	}
	for j, r := range sa {
		if !usedS[j] {
			n := normName(r.text("name"))
			leftS[n] = append(leftS[n], j)
		}
	}
	names := make([]string, 0, len(leftB))
	for n := range leftB {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		bs, ss := leftB[n], leftS[n]
		if n == "" || len(ss) == 0 {
			continue
		}
		rep.NameTried += len(bs)
		if len(bs) > 1 || len(ss) > 1 {
			rep.Ambiguous = append(rep.Ambiguous, brs[bs[0]].text("l18"))
			continue
		}
		i, j := bs[0], ss[0]
		if cb, cs := brsCode(brs[i]), saCode(sa[j]); cb != "" && cs != "" {
			rep.NameIDConflict = append(rep.NameIDConflict, fmt.Sprintf("%s (%s ≠ %s)", brs[i].text("l18"), cb, cs))
			continue
		}
		rep.Pairs = append(rep.Pairs, pair{Brs: brs[i], Sa: sa[j], ByName: true})
		usedB[i], usedS[j] = true, true
	}
	for i, r := range brs {
		if !usedB[i] {
			rep.OnlyBrs = append(rep.OnlyBrs, r)
		}
	}
	for j, r := range sa {
		if !usedS[j] {
			rep.OnlySa = append(rep.OnlySa, r)
		}
	}
	rep.DupSymBrs, rep.DupSymSa = dupSymbols(brs, "l18"), dupSymbols(sa, "name")
	for _, f := range fields() {
		rep.Stats = append(rep.Stats, stat(f, rep.Pairs))
	}
	rep.Classes = classes(brs, sa, rep.Pairs, rep.ClassDiff)
	rep.OnlyBrsKeys, rep.OnlySaKeys = extraKeys(brs, sa)
	rep.BrsDataAt, rep.SaDataAt = latestBrs(brs), latestSa(sa)
	return rep
}

// index maps each code found on exactly one row to that row; repeated codes are returned apart.
func index(rows []row, key func(row) string) (map[string]int, []string) {
	n := map[string]int{}
	for _, r := range rows {
		if c := key(r); c != "" {
			n[c]++
		}
	}
	idx := map[string]int{}
	var dup []string
	for i, r := range rows {
		c := key(r)
		switch {
		case c == "":
		case n[c] == 1:
			idx[c] = i
		case n[c] > 1:
			dup = append(dup, c)
			n[c] = -1 // list once
		}
	}
	sort.Strings(dup)
	return idx, dup
}

func dupSymbols(rows []row, k string) int {
	n := map[string]int{}
	for _, r := range rows {
		n[normName(r.text(k))]++
	}
	d := 0
	for _, c := range n {
		if c > 1 {
			d++
		}
	}
	return d
}

// latestBrs is the latest BrsApi last-event time (time of day only: the payload has no date).
func latestBrs(rows []row) string {
	max := ""
	for _, r := range rows {
		if t := r.text("time"); t > max {
			max = t
		}
	}
	return max
}

// latestSa is the latest SourceArena last-trade date and time (Jalali), compared numerically.
func latestSa(rows []row) string {
	best, bestKey := "", ""
	for _, r := range rows {
		d := strings.Split(r.text("last_trade_date"), "/")
		if len(d) != 3 {
			continue
		}
		key := fmt.Sprintf("%04s%02s%02s %8s", d[0], d[1], d[2], r.text("last_trade_time"))
		if key > bestKey {
			bestKey, best = key, r.text("last_trade_date")+" "+r.text("last_trade_time")
		}
	}
	return best
}

func stat(f field, pairs []pair) FieldStat {
	s := FieldStat{field: f, BrsFmt: map[string]int{}, SaFmt: map[string]int{}}
	for _, p := range pairs {
		s.BrsFmt[p.Brs.format(f.Brs)]++
		s.SaFmt[p.Sa.format(f.Sa)]++
		a, sa := p.Brs.value(f.Brs, f.Text)
		b, sb := p.Sa.value(f.Sa, f.Text)
		if sa == valBad {
			s.BadBrs++
		}
		if sb == valBad {
			s.BadSa++
		}
		switch {
		case sa == valOK && sb == valOK:
			s.Both++
			if a == b {
				s.Match++
				if a != "0" {
					s.NonZeroMatch++
				}
			} else {
				s.Mismatch++
				s.example(p, a, b)
			}
		case sa == valOK && sb == valBad, sa == valBad && sb == valOK:
			s.Mismatch++
			s.example(p, p.Brs.text(f.Brs), p.Sa.text(f.Sa))
		case sa == valOK:
			s.OnlyBrs++
		case sb == valOK:
			s.OnlySa++
		case sa == valAbsent && sb == valAbsent:
			s.Neither++
		default:
			s.BothBad++
		}
	}
	return s
}

func (s *FieldStat) example(p pair, a, b string) {
	if len(s.Examples) < 3 {
		s.Examples = append(s.Examples, fmt.Sprintf("%s: %s ≠ %s", p.Brs.text("l18"), a, b))
	}
}

// class is the proposed instrument class of a row (docs/source-mapping.md): board by ISIN
// suffix, funds (sector 68) by name, bonds (69), rights, energy, else stock by market.
func class(isin, sector, name string) string {
	switch {
	case len(isin) == 12 && (isin[8:] == "0002" || isin[8:] == "0003" || isin[8:] == "0004"):
		return "تابلو غیرعادی (…" + isin[8:] + ")"
	case strings.HasPrefix(isin, "IRE9"):
		return "انرژی (IRE9)"
	case strings.HasPrefix(isin, "IRR"):
		return "حق تقدم"
	case sector == "69" || strings.HasPrefix(isin, "IRB"):
		return "اوراق"
	case strings.HasPrefix(isin, "IRTK") && strings.Contains(name, "نقره"):
		return "صندوق نقره"
	case strings.HasPrefix(isin, "IRTK") || strings.HasPrefix(isin, "IRTE"):
		return "صندوق طلا/کالا"
	case sector == "68":
		n := strings.TrimSpace(name)
		switch {
		case strings.HasSuffix(n, "-د") || strings.HasSuffix(n, "ثابت") || strings.Contains(n, "درآمد ثابت") || strings.Contains(n, "درآمدثابت"):
			return "صندوق درآمد ثابت"
		case strings.HasSuffix(n, "-س") || strings.HasSuffix(n, "-ب") || strings.Contains(n, "سهام") || strings.Contains(n, "شاخصی") || strings.Contains(n, "بخشی") || strings.Contains(n, "اهرم"):
			return "صندوق سهامی"
		}
		return "سایر صندوق‌ها"
	case strings.HasPrefix(isin, "IRO1"):
		return "سهام بورس"
	case strings.HasPrefix(isin, "IRO3"):
		return "سهام فرابورس"
	case strings.HasPrefix(isin, "IRO7") || strings.HasPrefix(isin, "IRO5"):
		return "بازار پایه/دیگر"
	}
	return "نامشخص"
}

func brsClass(r row) string { return class(r.text("isin"), r.text("cs_id"), r.text("l30")) }
func saClass(r row) string {
	return class(r.text("namad_code"), strings.TrimLeft(r.text("industry_code"), "0"), r.text("full_name"))
}

func classes(brs, sa []row, pairs []pair, diff map[string]int) []classCount {
	m := map[string]*classCount{}
	get := func(c string) *classCount {
		if m[c] == nil {
			m[c] = &classCount{Class: c}
		}
		return m[c]
	}
	for _, r := range brs {
		get(brsClass(r)).Brs++
	}
	for _, r := range sa {
		get(saClass(r)).Sa++
	}
	for _, p := range pairs {
		if cb, cs := brsClass(p.Brs), saClass(p.Sa); cb == cs {
			get(cb).JoinedSame++
		} else {
			diff[cb+" → "+cs]++
		}
	}
	out := make([]classCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Brs+out[i].Sa, out[j].Brs+out[j].Sa; a != b {
			return a > b
		}
		return out[i].Class < out[j].Class
	})
	return out
}

// extraKeys lists keys one vendor has that no compared field maps (for the report).
func extraKeys(brs, sa []row) (onlyBrs, onlySa []string) {
	mapped := map[string]bool{}
	for _, f := range fields() {
		mapped["b:"+f.Brs], mapped["s:"+f.Sa] = true, true
	}
	collect := func(rows []row, p string) []string {
		seen := map[string]bool{}
		for _, r := range rows {
			for k := range r {
				if !mapped[p+k] && k != "id" && k != "instance_code" {
					seen[k] = true
				}
			}
		}
		out := make([]string, 0, len(seen))
		for k := range seen {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return collect(brs, "b:"), collect(sa, "s:")
}
