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

// int reads an integer given as a JSON number or string; "71690.00" (zero fraction) is accepted,
// any other fraction is not an integer.
func (r row) int(k string) (int64, bool) {
	s := strings.ReplaceAll(r.text(k), ",", "")
	if i := strings.IndexByte(s, '.'); i >= 0 && strings.Trim(s[i+1:], "0") == "" {
		s = s[:i]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
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
	ByName  bool // joined by symbol, not by instrument code
}

// FieldStat is the comparison of one field over the joined rows.
type FieldStat struct {
	field
	Both, Match     int      // rows where both vendors have a value / where they are equal
	OnlyBrs, OnlySa int      // rows where only one vendor has a value
	Examples        []string // up to 3 mismatches
	BrsFmt, SaFmt   map[string]int
}

// Report is everything the markdown shows.
type Report struct {
	BrsAt, SaAt, Note       string
	BrsRows, SaRows         int
	Pairs                   []pair
	OnlyBrs, OnlySa         []row
	Ambiguous               []string // symbols matching several rows of the other vendor
	NameIDConflict          []string // same symbol, different instrument code
	Stats                   []FieldStat
	Classes                 []classCount
	OnlyBrsKeys, OnlySaKeys []string
}

type classCount struct {
	Class           string
	Brs, Sa, Joined int
}

// compare joins BrsApi rows (id) and SourceArena rows (instance_code); rows without a code
// match are joined by normalised symbol when that symbol is unique on both sides.
func compare(brs, sa []row) *Report {
	rep := &Report{BrsRows: len(brs), SaRows: len(sa)}
	saByID := map[string]row{}
	saByName := map[string][]row{}
	for _, r := range sa {
		if id := r.text("instance_code"); id != "" {
			saByID[id] = r
		}
		n := normName(r.text("name"))
		saByName[n] = append(saByName[n], r)
	}
	brsByName := map[string]int{}
	for _, r := range brs {
		brsByName[normName(r.text("l18"))]++
	}
	used := map[string]bool{} // SourceArena instance codes joined
	for _, r := range brs {
		if s, ok := saByID[r.text("id")]; ok && r.text("id") != "" {
			rep.Pairs = append(rep.Pairs, pair{Brs: r, Sa: s})
			used[s.text("instance_code")] = true
			continue
		}
		n := normName(r.text("l18"))
		cands := saByName[n]
		switch {
		case len(cands) == 1 && brsByName[n] == 1 && !used[cands[0].text("instance_code")]:
			if r.text("id") != "" && cands[0].text("instance_code") != "" {
				rep.NameIDConflict = append(rep.NameIDConflict, r.text("l18"))
			}
			rep.Pairs = append(rep.Pairs, pair{Brs: r, Sa: cands[0], ByName: true})
			used[cands[0].text("instance_code")] = true
		case len(cands) > 1 || (len(cands) == 1 && brsByName[n] > 1):
			rep.Ambiguous = append(rep.Ambiguous, r.text("l18"))
			rep.OnlyBrs = append(rep.OnlyBrs, r)
		default:
			rep.OnlyBrs = append(rep.OnlyBrs, r)
		}
	}
	for _, r := range sa {
		if !used[r.text("instance_code")] {
			rep.OnlySa = append(rep.OnlySa, r)
		}
	}
	for _, f := range fields() {
		rep.Stats = append(rep.Stats, stat(f, rep.Pairs))
	}
	rep.Classes = classes(brs, sa, rep.Pairs)
	rep.OnlyBrsKeys, rep.OnlySaKeys = extraKeys(brs, sa)
	return rep
}

func stat(f field, pairs []pair) FieldStat {
	s := FieldStat{field: f, BrsFmt: map[string]int{}, SaFmt: map[string]int{}}
	for _, p := range pairs {
		s.BrsFmt[p.Brs.format(f.Brs)]++
		s.SaFmt[p.Sa.format(f.Sa)]++
		var a, b string
		var okA, okB bool
		if f.Text {
			a, b = normName(p.Brs.text(f.Brs)), normName(p.Sa.text(f.Sa))
			okA, okB = a != "", b != ""
		} else {
			x, ox := p.Brs.int(f.Brs)
			y, oy := p.Sa.int(f.Sa)
			a, b, okA, okB = strconv.FormatInt(x, 10), strconv.FormatInt(y, 10), ox, oy
		}
		switch {
		case okA && okB:
			s.Both++
			if a == b {
				s.Match++
			} else if len(s.Examples) < 3 {
				s.Examples = append(s.Examples, fmt.Sprintf("%s: %s ≠ %s", p.Brs.text("l18"), a, b))
			}
		case okA:
			s.OnlyBrs++
		case okB:
			s.OnlySa++
		}
	}
	return s
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

func classes(brs, sa []row, pairs []pair) []classCount {
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
		get(brsClass(p.Brs)).Joined++
	}
	out := make([]classCount, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Brs+out[i].Sa > out[j].Brs+out[j].Sa })
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
