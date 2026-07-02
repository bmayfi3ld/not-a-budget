package budget

import (
	"math"
	"testing"
	"time"
)

func mustDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestQuarterOf(t *testing.T) {
	cases := []struct {
		date          string
		year, q, days int
		start, end    string
	}{
		{"2026-04-01", 2026, 2, 91, "2026-04-01", "2026-06-30"},
		{"2026-06-30", 2026, 2, 91, "2026-04-01", "2026-06-30"},
		{"2024-01-02", 2024, 1, 91, "2024-01-01", "2024-03-31"}, // leap year Q1 = 91 days
		{"2025-01-02", 2025, 1, 90, "2025-01-01", "2025-03-31"},
		{"2024-12-31", 2024, 4, 92, "2024-10-01", "2024-12-31"},
	}
	for _, c := range cases {
		q := QuarterOf(mustDate(c.date))
		if q.Year != c.year || q.Quarter != c.q || q.Days != c.days || q.Start != c.start || q.End != c.end {
			t.Errorf("QuarterOf(%s) = %+v, want Q%d %d days=%d [%s..%s]",
				c.date, q, c.q, c.year, c.days, c.start, c.end)
		}
	}
}

func TestParseQuarter(t *testing.T) {
	now := mustDate("2026-05-10")
	cases := map[string]struct{ year, q int }{
		"":        {2026, 2},
		"2026-Q2": {2026, 2},
		"2026Q2":  {2026, 2},
		"Q2 2026": {2026, 2},
		"q2-2026": {2026, 2},
		"Q4 2024": {2024, 4},
	}
	for spec, want := range cases {
		q, err := ParseQuarter(spec, now)
		if err != nil {
			t.Errorf("ParseQuarter(%q) error: %v", spec, err)
			continue
		}
		if q.Year != want.year || q.Quarter != want.q {
			t.Errorf("ParseQuarter(%q) = Q%d %d, want Q%d %d", spec, q.Quarter, q.Year, want.q, want.year)
		}
	}
	if _, err := ParseQuarter("nonsense", now); err == nil {
		t.Error("expected error for invalid spec")
	}
	if _, err := ParseQuarter("2026-Q9", now); err == nil {
		t.Error("expected error for quarter 9")
	}
}

func TestDayIndex(t *testing.T) {
	q := QuarterOf(mustDate("2026-04-15"))
	if got := q.DayIndex(mustDate("2026-04-01")); got != 1 {
		t.Errorf("day 1 = %d", got)
	}
	if got := q.DayIndex(mustDate("2026-05-04")); got != 34 { // Apr has 30 days
		t.Errorf("May 4 should be day 34, got %d", got)
	}
	if got := q.DayIndex(mustDate("2026-05-16")); got != 46 {
		t.Errorf("May 16 should be day 46, got %d", got)
	}
	if got := q.DayIndex(mustDate("2026-07-01")); got != 0 {
		t.Errorf("out-of-quarter should be 0, got %d", got)
	}
}

func TestFlatGoal(t *testing.T) {
	// day 90 with goal 15000 over 90 days = 15000
	if g := FlatGoal(90, 15000, 90); math.Abs(g-15000) > 1e-9 {
		t.Errorf("FlatGoal(90) = %v, want 15000", g)
	}
	if g := FlatGoal(1, 15000, 90); math.Abs(g-166.6666666667) > 1e-6 {
		t.Errorf("FlatGoal(1) = %v, want ~166.67", g)
	}
	if g := FlatGoal(45, 15000, 90); math.Abs(g-7500) > 1e-9 {
		t.Errorf("FlatGoal(45) = %v, want 7500", g)
	}
}

func TestSpendAndCredits(t *testing.T) {
	txns := []Txn{
		{Date: "2026-04-01", Amount: -100, Type: TypeExpense},  // +100 spend
		{Date: "2026-04-02", Amount: -50, Type: TypeExpense},   // +50 spend
		{Date: "2026-04-03", Amount: 20, Type: TypeRefund},     // -20 spend
		{Date: "2026-04-04", Amount: 3000, Type: TypePayment},  // ignored
		{Date: "2026-04-05", Amount: -500, Type: TypeTransfer}, // ignored
	}
	if got := GrossSpend(txns); math.Abs(got-130) > 1e-9 {
		t.Errorf("GrossSpend = %v, want 130", got)
	}
	creds := []Cred{{Date: "2026-04-01", Amount: 200}, {Date: "2026-05-16", Amount: 7000}}
	if got := SumCredits(creds); math.Abs(got-7200) > 1e-9 {
		t.Errorf("SumCredits = %v, want 7200", got)
	}
	// Net = 130 - 7200
	if net := GrossSpend(txns) - SumCredits(creds); math.Abs(net-(-7070)) > 1e-9 {
		t.Errorf("net = %v, want -7070", net)
	}
}
