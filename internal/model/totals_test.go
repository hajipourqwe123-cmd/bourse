package model

import "testing"

func TestTotals(t *testing.T) {
	s := Snapshot{Volume: 10, Value: 100, TradeCount: 3}
	a, ok := TotalsOf(&s)
	if !ok || !a.HasCount || !a.Active() {
		t.Fatalf("totals = %+v %v", a, ok)
	}
	s.Missing = []string{FTradeCount}
	b, _ := TotalsOf(&s)
	if b.HasCount || !a.Same(b) || !b.Same(a) {
		t.Errorf("a missing trade count is not compared: %+v vs %+v", a, b)
	}
	c := a
	c.TradeCount = 4
	if a.Same(c) {
		t.Error("different trade counts are different totals")
	}
	if (Totals{TradeCount: 5}).Active() || !(Totals{TradeCount: 5, HasCount: true}).Active() || !(Totals{Value: 1}).Active() {
		t.Error("activity")
	}
	s.Missing = []string{FValue}
	if _, ok := TotalsOf(&s); ok {
		t.Error("missing value: no totals")
	}
}
