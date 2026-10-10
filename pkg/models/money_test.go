package models

import (
	"encoding/json"
	"testing"
)

func TestApplyPercent(t *testing.T) {
	tests := []struct {
		amount  Cents
		percent int
		want    Cents
	}{
		{10000, 10, 1000},
		{650, 10, 65},
		{655, 10, 66}, // 65.5 rounds up
		{654, 10, 65}, // 65.4 rounds down
		{1, 10, 0},
		{5, 10, 1}, // 0.5 rounds up
		{0, 10, 0},
		{1999, 0, 0},
		{1999, 100, 1999},
	}
	for _, tt := range tests {
		if got := tt.amount.ApplyPercent(tt.percent); got != tt.want {
			t.Errorf("Cents(%d).ApplyPercent(%d) = %d, want %d", tt.amount, tt.percent, got, tt.want)
		}
	}
}

func TestMarshalJSON(t *testing.T) {
	tests := map[Cents]string{
		0:      "0",
		650:    "6.5",
		700:    "7",
		1330:   "13.3",
		1999:   "19.99",
		123456: "1234.56",
	}
	for amount, want := range tests {
		got, err := json.Marshal(amount)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("json.Marshal(%d) = %s, want %s", amount, got, want)
		}
	}
}
