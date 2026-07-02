// Package views builds the canonical, structured budget views (quarter status,
// year summary, category breakdown) from a Store. Each view carries explicit
// render_instructions so the agent renders results consistently even when it
// has ignored the bundle-level guidance.
package views

import (
	"sort"
	"time"

	"github.com/bmayfield/mcpb-budget/internal/budget"
	"github.com/bmayfield/mcpb-budget/internal/store"
)

// Render instructions are intentionally verbose and embedded in every view
// result. They are the authoritative copy of the charting guidance.
const (
	QuarterRenderInstructions = "Render this as the canonical 'Spend vs Budget' chart. " +
		"Create an artifact (HTML+canvas or SVG line chart). X axis = day of quarter (1..days). " +
		"Plot two series over the `days` array: (1) `cum_total` as a solid line = cumulative net spend, " +
		"(2) `goal` as a dashed line = the flat budget curve. Draw a vertical marker at `summary.current_day` " +
		"labeled 'today' when it is > 0. Title: 'Q# YYYY — Spend vs Budget'. " +
		"Below the chart state: 'Spent $<net_total> of $<goal> this quarter — $<abs over_under> <over|under> the flat pace.' " +
		"over_under > 0 means OVER budget (color red); <= 0 means under/on pace (color green). " +
		"If artifacts are unavailable, output a compact markdown table of every ~10th day plus that summary line."

	YearRenderInstructions = "Render this as the canonical quarter-comparison chart. " +
		"Create an artifact bar chart: one bar per element of `quarters` with height = `net_total`, " +
		"category label = `label`. Overlay a horizontal line at each quarter's `goal`. " +
		"Color a bar red when `over_under` > 0 (over budget) and green when <= 0. " +
		"Title: 'Quarterly Net Spend vs Goal'. If artifacts are unavailable, output a markdown table with columns " +
		"Quarter, Gross Spend, Credits, Net Total, Goal, Over/Under, Avg Monthly."

	CategoryRenderInstructions = "Render as a markdown table sorted by spend descending (Category, Spend, % of gross). " +
		"Optionally add a horizontal bar artifact. Note that budgeting is tracked as a single quarterly total; " +
		"categories are for breakdown only and are not individually budgeted."
)

// DayPoint is one day in the quarter tracking series.
type DayPoint struct {
	Day       int     `json:"day"`
	DaySpend  float64 `json:"day_spend"`
	DayCredit float64 `json:"day_credit"` // negative when a credit lands (offsets spend)
	CumTotal  float64 `json:"cum_total"`  // cumulative net spend
	Goal      float64 `json:"goal"`       // flat-curve target at this day
	Diff      float64 `json:"diff"`       // goal - cum_total; >0 = under pace, <0 = over pace
}

// QuarterView is the "how much have I spent this quarter vs budget" result.
type QuarterView struct {
	Summary            QuarterSummary `json:"summary"`
	Days               []DayPoint     `json:"days"`
	RenderInstructions string         `json:"render_instructions"`
}

// QuarterSummary is the headline rollup for a quarter.
type QuarterSummary struct {
	Quarter      string  `json:"quarter"`
	Year         int     `json:"year"`
	QuarterNum   int     `json:"quarter_num"`
	Start        string  `json:"start"`
	End          string  `json:"end"`
	Days         int     `json:"days"`
	GrossSpend   float64 `json:"gross_spend"`
	Credits      float64 `json:"credits"`
	NetTotal     float64 `json:"net_total"`
	Goal         float64 `json:"goal"`
	OverUnder    float64 `json:"over_under"`    // net_total - goal; >0 = over budget
	CurrentDay   int     `json:"current_day"`   // 0 if the quarter is not the current one
	ProjectedEnd float64 `json:"projected_end"` // linear projection to end of quarter (0 if N/A)
}

// QuarterRow is one quarter in the year comparison.
type QuarterRow struct {
	Label      string  `json:"label"`
	Year       int     `json:"year"`
	QuarterNum int     `json:"quarter_num"`
	GrossSpend float64 `json:"gross_spend"`
	Credits    float64 `json:"credits"`
	NetTotal   float64 `json:"net_total"`
	Goal       float64 `json:"goal"`
	OverUnder  float64 `json:"over_under"`
	AvgMonthly float64 `json:"avg_monthly"`
}

// YearView is the multi-quarter comparison.
type YearView struct {
	Quarters           []QuarterRow `json:"quarters"`
	RenderInstructions string       `json:"render_instructions"`
}

// CategoryRow is one category's spend within a quarter.
type CategoryRow struct {
	Category string  `json:"category"`
	Spend    float64 `json:"spend"`
	Pct      float64 `json:"pct_of_gross"`
}

// CategoryBreakdown is per-category spend for a quarter.
type CategoryBreakdown struct {
	Quarter            string        `json:"quarter"`
	GrossSpend         float64       `json:"gross_spend"`
	Categories         []CategoryRow `json:"categories"`
	RenderInstructions string        `json:"render_instructions"`
}

func toTxns(rows []store.Transaction) []budget.Txn {
	out := make([]budget.Txn, len(rows))
	for i, r := range rows {
		out[i] = budget.Txn{Date: r.TxnDate, Amount: r.Amount, Type: r.TxnType}
	}
	return out
}

func toCreds(rows []store.Credit) []budget.Cred {
	out := make([]budget.Cred, len(rows))
	for i, r := range rows {
		out[i] = budget.Cred{Date: r.Date, Amount: r.Amount}
	}
	return out
}

// BuildQuarter constructs the quarter tracking view for quarter q, using `now`
// to place the current-day marker.
func BuildQuarter(s *store.Store, q budget.Quarter, now time.Time) (QuarterView, error) {
	meta, err := s.GetMeta()
	if err != nil {
		return QuarterView{}, err
	}
	txnRows, err := s.TransactionsBetween(q.Start, q.End)
	if err != nil {
		return QuarterView{}, err
	}
	credRows, err := s.CreditsBetween(q.Start, q.End)
	if err != nil {
		return QuarterView{}, err
	}

	// Bucket spend and credits by day-of-quarter.
	daySpend := make([]float64, q.Days+1)  // 1-based
	dayCredit := make([]float64, q.Days+1) // stored as negative (offset)
	for _, t := range txnRows {
		d := q.DayIndex(dateToTimeParse(t.TxnDate))
		if d >= 1 && d <= q.Days {
			daySpend[d] += budget.SpendContribution(budget.Txn{Date: t.TxnDate, Amount: t.Amount, Type: t.TxnType})
		}
	}
	for _, c := range credRows {
		d := q.DayIndex(dateToTimeParse(c.Date))
		if d >= 1 && d <= q.Days {
			dayCredit[d] += -c.Amount
		}
	}

	days := make([]DayPoint, 0, q.Days)
	var cum float64
	for d := 1; d <= q.Days; d++ {
		cum += daySpend[d] + dayCredit[d]
		goal := budget.FlatGoal(d, meta.QuarterlyGoal, meta.CurveDays)
		days = append(days, DayPoint{
			Day:       d,
			DaySpend:  round2(daySpend[d]),
			DayCredit: round2(dayCredit[d]),
			CumTotal:  round2(cum),
			Goal:      round2(goal),
			Diff:      round2(goal - cum),
		})
	}

	gross := budget.GrossSpend(toTxns(txnRows))
	credits := budget.SumCredits(toCreds(credRows))
	net := gross - credits
	currentDay := 0
	if cur := budget.QuarterOf(now); cur.Year == q.Year && cur.Quarter == q.Quarter {
		currentDay = q.DayIndex(now)
	}
	var projected float64
	if currentDay > 0 {
		projected = round2(net / float64(currentDay) * float64(q.Days))
	}

	return QuarterView{
		Summary: QuarterSummary{
			Quarter:      q.Label(),
			Year:         q.Year,
			QuarterNum:   q.Quarter,
			Start:        q.Start,
			End:          q.End,
			Days:         q.Days,
			GrossSpend:   round2(gross),
			Credits:      round2(credits),
			NetTotal:     round2(net),
			Goal:         round2(meta.QuarterlyGoal),
			OverUnder:    round2(net - meta.QuarterlyGoal),
			CurrentDay:   currentDay,
			ProjectedEnd: projected,
		},
		Days:               days,
		RenderInstructions: QuarterRenderInstructions,
	}, nil
}

// BuildYear constructs the quarter-comparison view across all quarters that
// contain data (falling back to the current quarter if the budget is empty).
func BuildYear(s *store.Store, now time.Time) (YearView, error) {
	meta, err := s.GetMeta()
	if err != nil {
		return YearView{}, err
	}
	minD, maxD, err := s.DateBounds()
	if err != nil {
		return YearView{}, err
	}
	var from, to time.Time
	if minD == "" {
		from, to = now, now
	} else {
		from = dateToTimeParse(minD)
		to = dateToTimeParse(maxD)
	}
	quarters := budget.Enumerate(from, to)
	rows := make([]QuarterRow, 0, len(quarters))
	for _, q := range quarters {
		txnRows, err := s.TransactionsBetween(q.Start, q.End)
		if err != nil {
			return YearView{}, err
		}
		credRows, err := s.CreditsBetween(q.Start, q.End)
		if err != nil {
			return YearView{}, err
		}
		gross := budget.GrossSpend(toTxns(txnRows))
		credits := budget.SumCredits(toCreds(credRows))
		net := gross - credits
		rows = append(rows, QuarterRow{
			Label:      q.Label(),
			Year:       q.Year,
			QuarterNum: q.Quarter,
			GrossSpend: round2(gross),
			Credits:    round2(credits),
			NetTotal:   round2(net),
			Goal:       round2(meta.QuarterlyGoal),
			OverUnder:  round2(net - meta.QuarterlyGoal),
			AvgMonthly: round2(gross / 3),
		})
	}
	return YearView{Quarters: rows, RenderInstructions: YearRenderInstructions}, nil
}

// BuildCategoryBreakdown returns per-category gross spend for quarter q.
func BuildCategoryBreakdown(s *store.Store, q budget.Quarter) (CategoryBreakdown, error) {
	txnRows, err := s.TransactionsBetween(q.Start, q.End)
	if err != nil {
		return CategoryBreakdown{}, err
	}
	byCat := map[string]float64{}
	var gross float64
	for _, t := range txnRows {
		c := budget.SpendContribution(budget.Txn{Date: t.TxnDate, Amount: t.Amount, Type: t.TxnType})
		if c == 0 {
			continue
		}
		cat := t.Category
		if cat == "" {
			cat = "Other"
		}
		byCat[cat] += c
		gross += c
	}
	rows := make([]CategoryRow, 0, len(byCat))
	for cat, spend := range byCat {
		pct := 0.0
		if gross != 0 {
			pct = spend / gross * 100
		}
		rows = append(rows, CategoryRow{Category: cat, Spend: round2(spend), Pct: round2(pct)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Spend > rows[j].Spend })
	return CategoryBreakdown{
		Quarter:            q.Label(),
		GrossSpend:         round2(gross),
		Categories:         rows,
		RenderInstructions: CategoryRenderInstructions,
	}, nil
}

func dateToTimeParse(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func round2(v float64) float64 {
	// Avoid negative-zero and long float tails in JSON output.
	r := float64(int64(v*100+sign(v)*0.5)) / 100
	if r == 0 {
		return 0
	}
	return r
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
