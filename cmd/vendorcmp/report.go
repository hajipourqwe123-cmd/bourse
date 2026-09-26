package main

import (
	"fmt"
	"sort"
	"strings"
)

// Markdown renders the report in Persian (human docs are Persian; field and key names stay Latin).
func (r *Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	byName := 0
	for _, p := range r.Pairs {
		if p.ByName {
			byName++
		}
	}
	w("# مقایسه فروشندگان داده: BrsApi و سورس‌آرنا\n\n")
	w("خروجی `go run ./cmd/vendorcmp` (فقط محلی). این گزارش فقط آمار و چند مقدار نمونه دارد: هیچ کلید، توکن یا خروجی خامی در آن نیست.\n\n")
	w("| | BrsApi (`AllSymbols.php?type=1`) | سورس‌آرنا (`all&type=0`) |\n| --- | --- | --- |\n")
	w("| زمان داده | %s | %s |\n| تعداد ردیف | %d | %d |\n\n", r.BrsAt, r.SaAt, r.BrsRows, r.SaRows)
	if r.Note != "" {
		w("%s\n\n", r.Note)
	}
	w("**پیوند ردیف‌ها:** %d ردیف مشترک (%d با کد داخلی TSETMC و %d با نماد). %d ردیف فقط در BrsApi و %d ردیف فقط در سورس‌آرنا.",
		len(r.Pairs), len(r.Pairs)-byName, byName, len(r.OnlyBrs), len(r.OnlySa))
	if len(r.Ambiguous) > 0 {
		w(" نمادهای مبهم (بیش از یک ردیف با همان نماد): %s.", list(r.Ambiguous, 20))
	} else {
		w(" نماد مبهمی نبود.")
	}
	if len(r.NameIDConflict) > 0 {
		w(" **ناسازگاری کد:** این نمادها در دو فروشنده کد داخلی متفاوت دارند: %s.", list(r.NameIDConflict, 20))
	}
	w("\n\n## تعداد ردیف به تفکیک کلاس (قاعده پیشنهادی `docs/source-mapping.md`)\n\n")
	w("| کلاس | BrsApi | سورس‌آرنا | مشترک |\n| --- | ---: | ---: | ---: |\n")
	for _, c := range r.Classes {
		w("| %s | %d | %d | %d |\n", c.Class, c.Brs, c.Sa, c.Joined)
	}
	w("\n## مقایسه فیلدها روی ردیف‌های مشترک\n\n")
	w("«پوشش» یعنی ردیف‌هایی که هر دو فروشنده مقدار عددی (یا متن) دارند. «تطابق» یعنی برابری دقیق (متن‌ها پس از یکسان‌سازی ی/ک و فاصله). قالب هر فروشنده جداگانه آمده است.\n\n")
	w("| گروه | فیلد | کلید BrsApi | کلید سورس‌آرنا | پوشش | تطابق | فقط BrsApi | فقط سورس‌آرنا | قالب BrsApi | قالب سورس‌آرنا | نمونه ناهمخوانی (BrsApi ≠ سورس‌آرنا) |\n")
	w("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |\n")
	for _, s := range r.Stats {
		rate := "—"
		if s.Both > 0 {
			rate = fmt.Sprintf("%.1f٪", 100*float64(s.Match)/float64(s.Both))
		}
		w("| %s | %s | `%s` | `%s` | %d | %s | %d | %d | %s | %s | %s |\n", s.Group, s.Name, s.Brs, s.Sa, s.Both, rate,
			s.OnlyBrs, s.OnlySa, formats(s.BrsFmt), formats(s.SaFmt), strings.Join(s.Examples, "؛ "))
	}
	w("\n## فیلدهایی که فقط یکی از فروشنده‌ها دارد\n\n")
	w("- **فقط BrsApi:** %s\n- **فقط سورس‌آرنا:** %s\n", keys(r.OnlyBrsKeys), keys(r.OnlySaKeys))
	w("\n## ردیف‌های بی‌جفت (نمونه)\n\n")
	w("- **فقط BrsApi** (%d): %s\n", len(r.OnlyBrs), sample(r.OnlyBrs, "l18", brsClass))
	w("- **فقط سورس‌آرنا** (%d): %s\n", len(r.OnlySa), sample(r.OnlySa, "name", saClass))
	return b.String()
}

func list(s []string, n int) string {
	if len(s) > n {
		return strings.Join(s[:n], "، ") + fmt.Sprintf(" و %d مورد دیگر", len(s)-n)
	}
	return strings.Join(s, "، ")
}

func keys(k []string) string {
	if len(k) == 0 {
		return "—"
	}
	q := make([]string, len(k))
	for i, s := range k {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, "، ")
}

// formats summarises a format histogram, most frequent first ("numeric string ×14, …").
func formats(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v || (s[i].v == s[j].v && s[i].k < s[j].k) })
	parts := make([]string, len(s))
	for i, x := range s {
		parts[i] = fmt.Sprintf("%s ×%d", x.k, x.v)
	}
	return strings.Join(parts, "، ")
}

// sample lists up to 12 unmatched symbols with their class, and the count per class.
func sample(rows []row, nameKey string, cls func(row) string) string {
	per := map[string]int{}
	var names []string
	for _, r := range rows {
		c := cls(r)
		per[c]++
		if len(names) < 12 {
			names = append(names, r.text(nameKey))
		}
	}
	var cs []string
	for c, n := range per {
		cs = append(cs, fmt.Sprintf("%s: %d", c, n))
	}
	sort.Strings(cs)
	if len(names) == 0 {
		return "—"
	}
	return strings.Join(names, "، ") + " — به تفکیک کلاس: " + strings.Join(cs, "؛ ")
}
