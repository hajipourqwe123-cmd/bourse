package main

import (
	"testing"

	"bourse/internal/model"
)

func TestBusGuardRefusesSynthetic(t *testing.T) {
	cases := []struct {
		s    model.Snapshot
		want bool // refused
	}{
		{model.Snapshot{InsCode: "IRO1FOLD0001", Symbol: "فولاد", Source: "sourcearena"}, false},
		{model.Snapshot{InsCode: "SYNTHETIC0001", Symbol: "SYN-ACCUM", Source: "synthetic"}, true},
		{model.Snapshot{InsCode: "IRO1X", Symbol: "x", Source: "synthetic"}, true},
		{model.Snapshot{InsCode: "SYNTEST0001", Symbol: "x", Source: "replay"}, true},
	}
	for _, c := range cases {
		if got := busGuard(&c.s, false) != nil; got != c.want {
			t.Errorf("%+v: refused=%v, want %v", c.s, got, c.want)
		}
		if busGuard(&c.s, true) != nil {
			t.Errorf("%+v: refused although explicitly allowed", c.s)
		}
	}
}
