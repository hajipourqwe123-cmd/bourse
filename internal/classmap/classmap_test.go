package classmap

import (
	"testing"
	"time"

	"bourse/internal/calendar"
	"bourse/internal/tehran"
)

func TestGroupAndClass(t *testing.T) {
	cases := []struct {
		isin, sector, name, group, class string
	}{
		{"IRO1FOLD0001", "27", "فولاد مبارکه", GroupBourse, "stock"},
		{"IRO3ZOBZ0001", "27", "", GroupFarabourse, "stock"},
		{"IRO7ABCD0001", "44", "", GroupBase, "stock"},
		{"IRR1FOLD0101", "27", "", GroupRights, "stock"},
		{"IRO1FOLD0003", "27", "", GroupAbnormalPrefix + "0003)", ""},
		{"IRE9XXXX0001", "40", "", GroupEnergy, ""},
		{"IRT1ABCD0001", "068", "صندوق فلان-د", GroupFixedFund, "fixed_income"},
		{"IRT1ABCD0001", "68", "صندوق سهامی فلان", GroupEquityFund, "equity_etf"},
		{"IRT1ABCD0001", "68", "صندوق املاک", GroupOtherFund, "other_fund"},
		{"IRTKABCD0001", "68", "صندوق نقره فلان", GroupSilverFund, "silver"},
		{"IRTKABCD0001", "68", "صندوق طلا", GroupGoldFund, "gold"},
		{"IRB3ABCD0001", "69", "", GroupBond, "fixed_income"},
		{"", "", "", GroupUnknown, ""},
	}
	for _, c := range cases {
		g := Group(c.isin, c.sector, c.name)
		if g != c.group || Class(g) != c.class {
			t.Errorf("%s/%s/%s: got %q→%q, want %q→%q", c.isin, c.sector, c.name, g, Class(g), c.group, c.class)
		}
	}
}

// Every class Class can return must exist in the embedded calendar (it has a session on a Sunday).
func TestClassesExistInCalendar(t *testing.T) {
	cal := calendar.Default()
	sunday := time.Date(2026, 9, 27, 10, 0, 0, 0, tehran.Loc)
	for _, cl := range []string{"stock", "equity_etf", "fixed_income", "gold", "silver", "other_fund"} {
		if _, ok := cal.ClassSession(cl, sunday); !ok {
			t.Errorf("class %s not in calendar", cl)
		}
	}
}
