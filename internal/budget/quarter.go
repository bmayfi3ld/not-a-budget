// Package budget holds the quarterly cash-flow domain logic: quarter math, the
// flat spending curve, and spend/credit aggregation. It is storage-agnostic and
// operates on plain transaction/credit values.
package budget

import (
	"fmt"
	"strings"
	"time"
)

// Quarter identifies a single calendar quarter and its bounds.
type Quarter struct {
	Year    int    `json:"year"`
	Quarter int    `json:"quarter"` // 1..4
	Start   string `json:"start"`   // ISO YYYY-MM-DD (inclusive)
	End     string `json:"end"`     // ISO YYYY-MM-DD (inclusive)
	Days    int    `json:"days"`    // calendar days in the quarter
}

// Label returns a human label like "Q2 2026".
func (q Quarter) Label() string { return fmt.Sprintf("Q%d %d", q.Quarter, q.Year) }

// startMonth returns the first month (1,4,7,10) of the quarter.
func startMonth(quarter int) time.Month { return time.Month((quarter-1)*3 + 1) }

// QuarterOf returns the Quarter containing the given date.
func QuarterOf(date time.Time) Quarter {
	q := (int(date.Month())-1)/3 + 1
	return quarter(date.Year(), q)
}

func quarter(year, q int) Quarter {
	start := time.Date(year, startMonth(q), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 3, 0).AddDate(0, 0, -1) // last day of quarter
	days := int(end.Sub(start).Hours()/24) + 1
	return Quarter{
		Year:    year,
		Quarter: q,
		Start:   start.Format("2006-01-02"),
		End:     end.Format("2006-01-02"),
		Days:    days,
	}
}

// ParseQuarter resolves a quarter spec. Accepted forms:
//   - "" -> the quarter containing `now`
//   - "2026-Q2", "2026Q2", "Q2 2026", "Q2-2026"
func ParseQuarter(spec string, now time.Time) (Quarter, error) {
	if strings.TrimSpace(spec) == "" {
		return QuarterOf(now), nil
	}
	s := normalizeSpec(spec)
	var year, q int
	// Try "YYYY-QN"
	if _, err := fmt.Sscanf(s, "%d-Q%d", &year, &q); err == nil && validQ(year, q) {
		return quarter(year, q), nil
	}
	// Try "QN-YYYY"
	if _, err := fmt.Sscanf(s, "Q%d-%d", &q, &year); err == nil && validQ(year, q) {
		return quarter(year, q), nil
	}
	return Quarter{}, fmt.Errorf("invalid quarter spec %q (use e.g. \"2026-Q2\")", spec)
}

func validQ(year, q int) bool { return q >= 1 && q <= 4 && year > 1900 && year < 3000 }

func normalizeSpec(spec string) string {
	out := make([]rune, 0, len(spec))
	for _, r := range spec {
		switch {
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == 'q' || r == 'Q':
			out = append(out, 'Q')
		case r == ' ' || r == '_':
			out = append(out, '-')
		case r == '-':
			out = append(out, '-')
		}
	}
	// Collapse "Q2-2026" or "2026-Q2" forms; also handle "Q22026"/"2026Q2".
	return insertDash(string(out))
}

// insertDash ensures a single dash between the year and quarter tokens so the
// Sscanf patterns match "YYYY-QN" / "QN-YYYY".
func insertDash(s string) string {
	if s == "" {
		return s
	}
	// Already contains a dash: trust it.
	for _, r := range s {
		if r == '-' {
			return s
		}
	}
	// "2026Q2" -> "2026-Q2"; "Q22026" -> "Q2-2026"
	if s[0] == 'Q' && len(s) >= 3 {
		return s[:2] + "-" + s[2:]
	}
	if idx := indexRune(s, 'Q'); idx > 0 {
		return s[:idx] + "-" + s[idx:]
	}
	return s
}

func indexRune(s string, target rune) int {
	for i, r := range s {
		if r == target {
			return i
		}
	}
	return -1
}

// DayIndex returns the 1-based day of the quarter for date, or 0 if date is
// outside the quarter.
func (q Quarter) DayIndex(date time.Time) int {
	start, _ := time.Parse("2006-01-02", q.Start)
	end, _ := time.Parse("2006-01-02", q.End)
	d := date.Truncate(24 * time.Hour)
	if d.Before(start) || d.After(end) {
		return 0
	}
	return int(d.Sub(start).Hours()/24) + 1
}

// Enumerate returns all quarters from the quarter containing `from` through the
// quarter containing `to`, inclusive.
func Enumerate(from, to time.Time) []Quarter {
	cur := QuarterOf(from)
	last := QuarterOf(to)
	var out []Quarter
	for {
		out = append(out, cur)
		if cur.Year == last.Year && cur.Quarter == last.Quarter {
			break
		}
		if cur.Quarter == 4 {
			cur = quarter(cur.Year+1, 1)
		} else {
			cur = quarter(cur.Year, cur.Quarter+1)
		}
		if len(out) > 400 { // safety
			break
		}
	}
	return out
}
