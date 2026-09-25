package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeSnapshot(t *testing.T) {
	full := Snapshot{InsCode: "IRO1", SourceTime: time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC), Volume: 7}
	fullJSON, _ := json.Marshal(full)
	cases := []struct {
		name, in string
		wantErr  string // "" = accepted
	}{
		{"go-encoded snapshot", string(fullJSON), ""},
		{"zero values present are data", `{"ins_code":"A","source_time":"2026-09-23T09:00:00+03:30","price_last":0,"volume":0,"value":0,"ind_buy_vol":0,"ind_sell_vol":0,"ind_buy_count":0,"ind_sell_count":0}`, ""},
		{"absent field listed in missing", `{"ins_code":"A","source_time":"2026-09-23T09:00:00+03:30","price_last":1,"volume":1,"value":1,"ind_buy_vol":1,"ind_sell_vol":1,"ind_buy_count":1,"missing":["ind_sell_count"]}`, ""},
		{"null", `null`, "null snapshot"},
		{"array", `[1]`, "not a JSON object"},
		{"garbage", `{"ins_code": 12`, "not a JSON object"},
		{"empty object", `{}`, "empty ins_code"},
		{"no source time", `{"ins_code":"A"}`, "missing source_time"},
		{"flow field absent", `{"ins_code":"A","source_time":"2026-09-23T09:00:00+03:30","price_last":1,"volume":1,"value":1,"ind_buy_vol":1,"ind_sell_vol":1,"ind_buy_count":1}`, "field ind_sell_count absent"},
		{"flow field null", `{"ins_code":"A","source_time":"2026-09-23T09:00:00+03:30","price_last":1,"volume":null,"value":1,"ind_buy_vol":1,"ind_sell_vol":1,"ind_buy_count":1,"ind_sell_count":1}`, "field volume absent"},
		{"wrong type", `{"ins_code":"A","source_time":"2026-09-23T09:00:00+03:30","volume":"7"}`, "cannot unmarshal"},
	}
	for _, c := range cases {
		s, err := DecodeSnapshot([]byte(c.in))
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err %v, want containing %q", c.name, err, c.wantErr)
		case c.wantErr == "" && s.InsCode == "":
			t.Errorf("%s: decoded empty snapshot", c.name)
		}
	}
}
