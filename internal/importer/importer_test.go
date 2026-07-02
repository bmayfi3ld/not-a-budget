package importer

import (
	"path/filepath"
	"testing"

	"github.com/bmayfield/mcpb-budget/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(path, true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestImportFiltersAndDedups(t *testing.T) {
	s := newStore(t)
	rows := []Row{
		{TxnDate: "2026-04-01", Description: "KROGER", Amount: -100, Category: "Groceries", TxnType: "expense"},
		{TxnDate: "2026-04-02", Description: "AMZN Mktp", Amount: -50, Category: "Shopping", TxnType: "sale"},
		// credit-card payment: should be filtered out even though type omitted (keyword detection)
		{TxnDate: "2026-04-03", Description: "AUTOMATIC PAYMENT - THANK YOU", Amount: 3000},
		// explicit transfer: filtered
		{TxnDate: "2026-04-04", Description: "Transfer to savings", Amount: -500, TxnType: "transfer"},
		// duplicate of row 1 (same date/amount/description): skipped
		{TxnDate: "2026-04-01", Description: "KROGER", Amount: -100, TxnType: "expense"},
		// bad date: warning + skip
		{TxnDate: "04/05/2026", Description: "BadDate", Amount: -10},
	}
	rep, err := Import(s, rows, "batch1")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Inserted != 2 {
		t.Errorf("Inserted = %d, want 2", rep.Inserted)
	}
	if rep.FilteredOut != 2 {
		t.Errorf("FilteredOut = %d, want 2 (payment + transfer)", rep.FilteredOut)
	}
	if rep.SkippedDuplicates != 1 {
		t.Errorf("SkippedDuplicates = %d, want 1", rep.SkippedDuplicates)
	}
	if len(rep.Warnings) != 1 {
		t.Errorf("Warnings = %v, want 1 (bad date)", rep.Warnings)
	}

	// Re-import the same batch: everything already present -> 0 inserted.
	rep2, err := Import(s, rows, "batch1")
	if err != nil {
		t.Fatalf("reimport: %v", err)
	}
	if rep2.Inserted != 0 {
		t.Errorf("re-import Inserted = %d, want 0 (dedup across runs)", rep2.Inserted)
	}
}

func TestNormalizeCategory(t *testing.T) {
	cases := map[string]string{
		"Groceries":           "Groceries",
		"General Merchandise": "Shopping",
		"Restaurants":         "Food & Drink",
		"Gasoline/Fuel":       "Gas",
		"":                    "Other",
		"Wat":                 "Other",
	}
	for in, want := range cases {
		if got := NormalizeCategory(in); got != want {
			t.Errorf("NormalizeCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyType(t *testing.T) {
	cases := []struct {
		raw, desc string
		amount    float64
		want      string
	}{
		{"Sale", "KROGER", -50, "expense"},
		{"", "AUTOMATIC PAYMENT - THANK YOU", 300, "payment"},
		{"", "MERCHANT", -10, "expense"},
		{"Return", "REFUND", 10, "refund"},
		{"ACH_CREDIT", "PAYCHECK", 2000, "transfer"},
		{"", "wire transfer out", -100, "transfer"},
	}
	for _, c := range cases {
		if got := classifyType(c.raw, c.desc, c.amount); got != c.want {
			t.Errorf("classifyType(%q,%q,%v) = %q, want %q", c.raw, c.desc, c.amount, got, c.want)
		}
	}
}
