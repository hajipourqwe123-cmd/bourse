// Package classmap proposes an instrument's session class from vendor identity fields (ISIN,
// sector code, full name). PROVISIONAL («تأییدنشده»): the rule (docs/source-mapping.md) is a
// heuristic on names and ISIN prefixes, not an official instrument list. Its output is stored by
// ins_code in a sessions file with "instruments_verified": false, never by name (fund names
// differ between vendors).
package classmap

import "strings"

// Group labels (Persian, as in docs/vendor-comparison.md). Groups are finer than classes.
const (
	GroupAbnormalPrefix = "تابلو غیرعادی (…" // + board suffix + ")"
	GroupEnergy         = "انرژی (IRE9)"
	GroupRights         = "حق تقدم"
	GroupBond           = "اوراق"
	GroupSilverFund     = "صندوق نقره"
	GroupGoldFund       = "صندوق طلا/کالا"
	GroupOtherCommodity = "صندوق کالایی دیگر (پلاتین، زعفران)"
	GroupFixedFund      = "صندوق درآمد ثابت"
	GroupEquityFund     = "صندوق سهامی"
	GroupOtherFund      = "سایر صندوق‌ها"
	GroupBourse         = "سهام بورس"
	GroupFarabourse     = "سهام فرابورس"
	GroupBase           = "بازار پایه/دیگر"
	GroupUnknown        = "نامشخص"
)

// Group is the proposed group of a row: board by ISIN suffix, funds (sector 68) by name, bonds
// (69), rights, energy, else stock by market prefix. symbol is the ticker (BrsApi l18,
// SourceArena name), name the full name (l30, full_name).
//
// Names are folded to Persian yeh/kaf first (SourceArena's full_name uses Arabic ي/ك). IRTE
// ("مبتنی بر کالا") funds are NOT gold: the one live example (2026-09-27) trades in the morning.
// Commodity funds (IRTK) are gold unless the name says «نقره» or the ticker starts with «نقر» or
// «سیلو» (silver: SourceArena's full names are «صندوق س. کالای …» without the metal), or the
// ticker is a known non-gold fund (پلاتا platinum, سافرون saffron: no class of their own, so
// unknown). Live check 2026-09-27: 10 of 48 IRTK funds were not gold by name alone.
func Group(isin, sector, name, symbol string) string {
	sector = strings.TrimLeft(strings.TrimSpace(sector), "0")
	fold := strings.NewReplacer("ي", "ی", "ى", "ی", "ك", "ک", "‌", "", " ", "")
	name = strings.NewReplacer("ي", "ی", "ى", "ی", "ك", "ک").Replace(name)
	symbol = fold.Replace(strings.TrimSpace(symbol))
	switch {
	case len(isin) == 12 && (isin[8:] == "0002" || isin[8:] == "0003" || isin[8:] == "0004"):
		return GroupAbnormalPrefix + isin[8:] + ")"
	case strings.HasPrefix(isin, "IRE9"):
		return GroupEnergy
	case strings.HasPrefix(isin, "IRR"):
		return GroupRights
	case sector == "69" || (len(isin) > 3 && strings.HasPrefix(isin, "IRB") && isin[3] >= '0' && isin[3] <= '9'):
		return GroupBond // not IRBZ… (capacity certificates)
	case strings.HasPrefix(isin, "IRTK") && (symbol == "پلاتا" || symbol == "سافرون" || strings.Contains(name, "زعفران")):
		return GroupOtherCommodity
	case strings.HasPrefix(isin, "IRTK") && (strings.Contains(name, "نقره") || strings.HasPrefix(symbol, "نقر") || strings.HasPrefix(symbol, "سیلو")):
		return GroupSilverFund
	case strings.HasPrefix(isin, "IRTK"):
		return GroupGoldFund
	case sector == "68":
		n := strings.TrimSpace(name)
		switch {
		case strings.HasSuffix(n, "-د") || strings.HasSuffix(n, "ثابت") || strings.Contains(n, "درآمد ثابت") || strings.Contains(n, "درآمدثابت"):
			return GroupFixedFund
		case strings.HasSuffix(n, "-س") || strings.HasSuffix(n, "-ب") || strings.Contains(n, "سهام") || strings.Contains(n, "شاخصی") || strings.Contains(n, "بخشی") || strings.Contains(n, "اهرم"):
			return GroupEquityFund
		}
		return GroupOtherFund
	case strings.HasPrefix(isin, "IRO1"):
		return GroupBourse
	case strings.HasPrefix(isin, "IRO3"):
		return GroupFarabourse
	case strings.HasPrefix(isin, "IRO7") || strings.HasPrefix(isin, "IRO5"):
		return GroupBase
	}
	return GroupUnknown
}

// Class maps a group to a session class of internal/calendar/sessions.json. "" means the
// instrument stays unmapped (class unknown, excluded from per-class aggregates): abnormal boards
// (fixed-price, issue/redemption, block), energy products and anything unrecognised must not be
// counted like a normal-board instrument.
func Class(group string) string {
	switch group {
	case GroupBourse, GroupFarabourse, GroupBase, GroupRights:
		return "stock"
	case GroupEquityFund:
		return "equity_etf"
	case GroupFixedFund, GroupBond:
		return "fixed_income"
	case GroupGoldFund:
		return "gold"
	case GroupSilverFund:
		return "silver"
	case GroupOtherFund:
		return "other_fund"
	}
	return ""
}
