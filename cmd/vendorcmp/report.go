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
	w("خروجی `go run ./cmd/vendorcmp` (فقط محلی). این گزارش فقط آمار و چند مقدار نمونه دارد: هیچ کلید، توکن یا خروجی خامی در آن نیست. ")
	w("این **هم‌خوانی دو خوراک** است (که احتمالاً هر دو از TSETMC می‌آیند)، نه اثبات درستی داده، و فقط برای همین دو پاسخ معتبر است.\n\n")
	w("| | BrsApi (`AllSymbols.php?type=1`) | سورس‌آرنا (`all&type=0`) |\n| --- | --- | --- |\n")
	w("| زمان فایل یا دریافت | %s | %s |\n", r.BrsAt, r.SaAt)
	w("| آخرین زمان درون داده | %s (`time`، بدون تاریخ) | %s (`last_trade_date/time`) |\n", dash(r.BrsDataAt), dash(r.SaDataAt))
	w("| تعداد ردیف | %d | %d |\n\n", r.BrsRows, r.SaRows)
	if r.Note != "" {
		w("%s\n\n", r.Note)
	}
	w("**پیوند ردیف‌ها:** %d ردیف مشترک (%d با کد داخلی TSETMC و %d با نماد، وقتی یک طرف کد نداشت). %d ردیف فقط در BrsApi و %d ردیف فقط در سورس‌آرنا.",
		len(r.Pairs), len(r.Pairs)-byName, byName, len(r.OnlyBrs), len(r.OnlySa))
	w(" کد تکراری: BrsApi %d و سورس‌آرنا %d. نماد تکراری (پس از یکسان‌سازی): BrsApi %d و سورس‌آرنا %d.",
		len(r.DupCodeBrs), len(r.DupCodeSa), r.DupSymBrs, r.DupSymSa)
	if r.NameTried > 0 {
		w(" پیوند با نماد برای %d ردیف امتحان شد؛ مبهم: %s.", r.NameTried, dash(list(r.Ambiguous, 20)))
	}
	if len(r.NameIDConflict) > 0 {
		w(" **ناسازگاری کد** (نماد یکسان، کد متفاوت؛ پیوند داده نشد): %s.", list(r.NameIDConflict, 20))
	}
	w("\n\n## تعداد ردیف به تفکیک کلاس (قاعده پیشنهادی `docs/source-mapping.md`)\n\n")
	w("«مشترک هم‌کلاس» یعنی ردیف‌های مشترکی که هر دو فروشنده در همین کلاس می‌گذارند. کلاس صندوق‌ها از نام است و نام دو فروشنده فرق دارد.\n\n")
	w("| کلاس | BrsApi | سورس‌آرنا | مشترک هم‌کلاس |\n| --- | ---: | ---: | ---: |\n")
	for _, c := range r.Classes {
		w("| %s | %d | %d | %d |\n", c.Class, c.Brs, c.Sa, c.JoinedSame)
	}
	if len(r.ClassDiff) > 0 {
		var d []string
		for k, n := range r.ClassDiff {
			d = append(d, fmt.Sprintf("%s: %d", k, n))
		}
		sort.Strings(d)
		w("\nردیف‌های مشترک با کلاس متفاوت (BrsApi → سورس‌آرنا): %s.\n", strings.Join(d, "؛ "))
	}
	w("\n## مقایسه فیلدها روی ردیف‌های مشترک\n\n")
	w("هر ردیف مشترک در یکی از این ستون‌ها شمرده می‌شود: «تطابق» (برابری دقیق؛ متن پس از یکسان‌سازی ی/ک و فاصله)، «ناهمخوان» (دو مقدار متفاوت، یا مقدار غیرقابل‌تجزیه کنار مقدار سالم)، «فقط یک طرف»، «هیچ‌کدام» (نبود، null، \"\" یا «-» در هر دو) و «هر دو نامعتبر». ")
	w("«تطابق غیرصفر» تطابق‌های صفر را کنار می‌گذارد، چون صفر برای هر دو فروشنده «هیچ» هم هست (سطح خالی دفتر، نماد بی‌معامله). نرخ = تطابق ÷ ردیف‌هایی که هر دو مقدار سالم دارند.\n\n")
	w("| گروه | فیلد | کلید BrsApi | کلید سورس‌آرنا | هر دو سالم | تطابق | نرخ | تطابق غیرصفر | ناهمخوان | فقط BrsApi | فقط سورس‌آرنا | هیچ‌کدام | نامعتبر B/S | قالب BrsApi | قالب سورس‌آرنا | نمونه ناهمخوانی (BrsApi ≠ سورس‌آرنا) |\n")
	w("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |\n")
	for _, s := range r.Stats {
		rate := "—"
		if s.Both > 0 {
			rate = fmt.Sprintf("%.1f٪", 100*float64(s.Match)/float64(s.Both))
		}
		w("| %s | %s | `%s` | `%s` | %d | %d | %s | %d | %d | %d | %d | %d | %d/%d | %s | %s | %s |\n", s.Group, s.Name, s.Brs, s.Sa,
			s.Both, s.Match, rate, s.NonZeroMatch, s.Mismatch, s.OnlyBrs, s.OnlySa, s.Neither, s.BadBrs, s.BadSa,
			formats(s.BrsFmt), formats(s.SaFmt), strings.Join(s.Examples, "؛ "))
	}
	w("\n## فیلدهایی که فقط یکی از فروشنده‌ها دارد\n\n")
	w("- **فقط BrsApi:** %s\n- **فقط سورس‌آرنا:** %s\n", keys(r.OnlyBrsKeys), keys(r.OnlySaKeys))
	w("\n## ردیف‌های بی‌جفت (نمونه)\n\n")
	w("- **فقط BrsApi** (%d): %s\n", len(r.OnlyBrs), sample(r.OnlyBrs, "l18", brsClass))
	w("- **فقط سورس‌آرنا** (%d): %s\n", len(r.OnlySa), sample(r.OnlySa, "name", saClass))
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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
