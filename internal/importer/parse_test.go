package importer

import (
	"math"
	"strings"
	"testing"
)

func TestParseAmount(t *testing.T) {
	cases := map[string]float64{
		"-290.30":    -290.30,
		"$1,234.56":  1234.56,
		"(2,911.76)": -2911.76,
		"($42.70)":   -42.70,
		"  100 ":     100,
	}
	for in, want := range cases {
		got, err := ParseAmount(in)
		if err != nil {
			t.Errorf("ParseAmount(%q) error: %v", in, err)
			continue
		}
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("ParseAmount(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseAmount(""); err == nil {
		t.Error("expected error for empty amount")
	}
	if _, err := ParseAmount("abc"); err == nil {
		t.Error("expected error for non-numeric amount")
	}
}

func TestReadCSV(t *testing.T) {
	content := "Amount,Txn Date,Description,Category\n" +
		"-290.30,2026-04-01,KROGER,Groceries\n\n" +
		"-42.70,2026-04-05,PHILLIPS 66,Gas\n"
	rows, err := ReadCSV(strings.NewReader(content))
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].TxnDate != "2026-04-01" || rows[0].Description != "KROGER" || math.Abs(rows[0].Amount+290.30) > 1e-9 {
		t.Errorf("row0 = %+v", rows[0])
	}
	if _, err := ReadCSV(strings.NewReader("date,desc\n2026-01-01,x\n")); err == nil {
		t.Error("expected error for missing required columns")
	}
}

func TestReadJSON(t *testing.T) {
	rows, src, err := ReadJSON(strings.NewReader(`[{"txn_date":"2026-04-01","description":"K","amount":-10}]`))
	if err != nil || len(rows) != 1 || src != "" {
		t.Fatalf("array: rows=%d src=%q err=%v", len(rows), src, err)
	}
	rows, src, err = ReadJSON(strings.NewReader(`{"source":"b7","rows":[{"txn_date":"2026-04-01","description":"K","amount":-10}]}`))
	if err != nil || len(rows) != 1 || src != "b7" {
		t.Fatalf("wrapper: rows=%d src=%q err=%v", len(rows), src, err)
	}
}
