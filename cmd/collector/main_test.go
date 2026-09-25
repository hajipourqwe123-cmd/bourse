package main

import (
	"testing"
	"time"

	"bourse/internal/model"
	"bourse/internal/tehran"
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

func TestCollectWindow(t *testing.T) {
	w, err := parseWindow("08:30-13:00")
	if err != nil || w.String() != "08:30-13:00" {
		t.Fatalf("parse: %v %v", w, err)
	}
	at := func(hm string) time.Time {
		x, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-09-23 "+hm, tehran.Loc)
		return x
	}
	for hm, want := range map[string]bool{"08:29:59": false, "08:30:00": true, "12:59:59": true, "13:00:00": false, "23:00:00": false} {
		if got := w.contains(at(hm)); got != want {
			t.Errorf("%s: contains=%v, want %v", hm, got, want)
		}
	}
	// 05:00 UTC is 08:30 in Tehran (+03:30): the window follows Tehran time, not the host's.
	if !w.contains(time.Date(2026, 9, 23, 5, 0, 0, 0, time.UTC)) {
		t.Error("window not evaluated in Tehran time")
	}
	if a, _ := parseWindow("always"); !a.contains(at("03:00:00")) {
		t.Error("always")
	}
	for _, bad := range []string{"", "13:00-08:30", "8-13", "08:30"} {
		if _, err := parseWindow(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
