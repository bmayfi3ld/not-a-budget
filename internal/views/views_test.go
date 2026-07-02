package views

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/bmayfield/mcpb-budget/internal/budget"
	"github.com/bmayfield/mcpb-budget/internal/store"
)

func setup(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "v.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBuildQuarterCumulativeAndCredit(t *testing.T) {
	s := setup(t)
	// Q2 2026 (Apr-Jun). Spend 100 on day1, 50 on day2; credit 7000 on May 16 (day 46).
	mustInsert(t, s, "2026-04-01", -100, budget.TypeExpense)
	mustInsert(t, s, "2026-04-02", -50, budget.TypeExpense)
	if _, err := s.InsertCredit(store.Credit{Date: "2026-05-16", Amount: 7000}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	q := budget.QuarterOf(now)
	v, err := BuildQuarter(s, q, now)
	if err != nil {
		t.Fatalf("BuildQuarter: %v", err)
	}

	// Summary: gross 150, credits 7000, net -6850.
	if math.Abs(v.Summary.GrossSpend-150) > 1e-6 {
		t.Errorf("gross = %v want 150", v.Summary.GrossSpend)
	}
	if math.Abs(v.Summary.Credits-7000) > 1e-6 {
		t.Errorf("credits = %v want 7000", v.Summary.Credits)
	}
	if math.Abs(v.Summary.NetTotal-(-6850)) > 1e-6 {
		t.Errorf("net = %v want -6850", v.Summary.NetTotal)
	}
	if v.Summary.CurrentDay != q.DayIndex(now) || v.Summary.CurrentDay == 0 {
		t.Errorf("current day = %d", v.Summary.CurrentDay)
	}
	if v.RenderInstructions == "" {
		t.Error("missing render instructions")
	}

	// Day 1 cum = 100; day 2 cum = 150; day 46 cum drops by 7000 credit.
	if math.Abs(v.Days[0].CumTotal-100) > 1e-6 {
		t.Errorf("day1 cum = %v want 100", v.Days[0].CumTotal)
	}
	if math.Abs(v.Days[1].CumTotal-150) > 1e-6 {
		t.Errorf("day2 cum = %v want 150", v.Days[1].CumTotal)
	}
	// day index 46 (0-based 45)
	if math.Abs(v.Days[45].DayCredit-(-7000)) > 1e-6 {
		t.Errorf("day46 credit = %v want -7000", v.Days[45].DayCredit)
	}
	if math.Abs(v.Days[45].CumTotal-(-6850)) > 1e-6 {
		t.Errorf("day46 cum = %v want -6850", v.Days[45].CumTotal)
	}
	// Diff = goal - cum. Day 46 goal = 15000*46/90 = 7666.67; diff huge positive.
	wantDiff := 15000.0*46/90 - (-6850)
	if math.Abs(v.Days[45].Diff-round2(wantDiff)) > 0.02 {
		t.Errorf("day46 diff = %v want ~%v", v.Days[45].Diff, wantDiff)
	}
}

func TestBuildYear(t *testing.T) {
	s := setup(t)
	mustInsert(t, s, "2025-01-15", -1000, budget.TypeExpense)
	mustInsert(t, s, "2025-04-15", -2000, budget.TypeExpense)
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	v, err := BuildYear(s, now)
	if err != nil {
		t.Fatalf("BuildYear: %v", err)
	}
	if len(v.Quarters) != 2 {
		t.Fatalf("want 2 quarters, got %d", len(v.Quarters))
	}
	if v.Quarters[0].Label != "Q1 2025" || math.Abs(v.Quarters[0].NetTotal-1000) > 1e-6 {
		t.Errorf("Q1 wrong: %+v", v.Quarters[0])
	}
	if v.Quarters[1].Label != "Q2 2025" || math.Abs(v.Quarters[1].NetTotal-2000) > 1e-6 {
		t.Errorf("Q2 wrong: %+v", v.Quarters[1])
	}
	// avg monthly = gross/3
	if math.Abs(v.Quarters[1].AvgMonthly-2000.0/3) > 0.01 {
		t.Errorf("avg monthly = %v", v.Quarters[1].AvgMonthly)
	}
}

func TestCategoryBreakdown(t *testing.T) {
	s := setup(t)
	mustInsertCat(t, s, "2026-04-01", -100, budget.TypeExpense, "Groceries")
	mustInsertCat(t, s, "2026-04-02", -300, budget.TypeExpense, "Shopping")
	mustInsertCat(t, s, "2026-04-03", 50, budget.TypeRefund, "Shopping") // reduces shopping
	q := budget.QuarterOf(time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC))
	v, err := BuildCategoryBreakdown(s, q)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(v.GrossSpend-350) > 1e-6 { // 100 + (300-50)
		t.Errorf("gross = %v want 350", v.GrossSpend)
	}
	if v.Categories[0].Category != "Shopping" || math.Abs(v.Categories[0].Spend-250) > 1e-6 {
		t.Errorf("top category = %+v want Shopping 250", v.Categories[0])
	}
}

func mustInsert(t *testing.T, s *store.Store, date string, amount float64, typ string) {
	mustInsertCat(t, s, date, amount, typ, "Other")
}

func mustInsertCat(t *testing.T, s *store.Store, date string, amount float64, typ, cat string) {
	t.Helper()
	_, err := s.InsertTransaction(store.Transaction{
		TxnDate: date, Description: date + typ, Amount: amount, Category: cat, TxnType: typ,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}
