package cli

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestReadCSVHeaderMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.csv")
	// Header uses spaces + different order; missing optional columns.
	content := "Amount,Txn Date,Description,Category\n" +
		"-290.30,2026-04-01,KROGER,Groceries\n" +
		"\n" + // blank line skipped
		"-42.70,2026-04-05,PHILLIPS 66,Gas\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, src, err := readCSV(path)
	if err != nil {
		t.Fatalf("readCSV: %v", err)
	}
	if src != "t.csv" {
		t.Errorf("source = %q, want t.csv", src)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].TxnDate != "2026-04-01" || rows[0].Description != "KROGER" || math.Abs(rows[0].Amount-(-290.30)) > 1e-9 {
		t.Errorf("row0 = %+v", rows[0])
	}
	if rows[1].Category != "Gas" {
		t.Errorf("row1 category = %q", rows[1].Category)
	}
}

func TestReadCSVMissingRequired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(path, []byte("date,desc\n2026-01-01,x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCSV(path); err == nil {
		t.Error("expected error for CSV missing required columns")
	}
}

func TestReadJSONArrayAndWrapper(t *testing.T) {
	dir := t.TempDir()
	arr := filepath.Join(dir, "a.json")
	os.WriteFile(arr, []byte(`[{"txn_date":"2026-04-01","description":"K","amount":-10}]`), 0o644)
	rows, _, err := readJSON(arr)
	if err != nil || len(rows) != 1 {
		t.Fatalf("array: rows=%d err=%v", len(rows), err)
	}
	wrap := filepath.Join(dir, "w.json")
	os.WriteFile(wrap, []byte(`{"source":"batch7","rows":[{"txn_date":"2026-04-01","description":"K","amount":-10}]}`), 0o644)
	rows, src, err := readJSON(wrap)
	if err != nil || len(rows) != 1 || src != "batch7" {
		t.Fatalf("wrapper: rows=%d src=%q err=%v", len(rows), src, err)
	}
}
