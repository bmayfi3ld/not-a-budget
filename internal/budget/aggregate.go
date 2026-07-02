package budget

import "time"

// Transaction type classification used by the aggregation rules. Only Expense
// and Refund contribute to spending; Payment/Transfer/Other are excluded.
const (
	TypeExpense  = "expense"
	TypeRefund   = "refund"
	TypePayment  = "payment"
	TypeTransfer = "transfer"
	TypeOther    = "other"
)

// Txn is the minimal transaction shape the domain math needs.
type Txn struct {
	Date   string  // ISO YYYY-MM-DD
	Amount float64 // signed; negative = outflow
	Type   string  // one of the Type* constants
}

// Cred is the minimal external-fund credit shape.
type Cred struct {
	Date   string
	Amount float64 // positive
}

// SpendContribution returns how much a transaction adds to gross spend.
// Expenses (negative amounts) add positive spend; refunds (positive amounts)
// reduce spend. All other types contribute zero.
func SpendContribution(t Txn) float64 {
	switch t.Type {
	case TypeExpense, TypeRefund:
		return -t.Amount
	default:
		return 0
	}
}

// GrossSpend sums the spend contributions of all transactions.
func GrossSpend(txns []Txn) float64 {
	var sum float64
	for _, t := range txns {
		sum += SpendContribution(t)
	}
	return sum
}

// SumCredits totals external-fund credit amounts.
func SumCredits(credits []Cred) float64 {
	var sum float64
	for _, c := range credits {
		sum += c.Amount
	}
	return sum
}

// FlatGoal returns the target cumulative spend at a given 1-based day of the
// quarter under the flat spending curve: goal(day) = quarterlyGoal * day / curveDays.
func FlatGoal(day int, quarterlyGoal float64, curveDays int) float64 {
	if curveDays <= 0 {
		curveDays = DefaultCurveDaysFallback
	}
	return quarterlyGoal * float64(day) / float64(curveDays)
}

// DefaultCurveDaysFallback guards against a zero curveDays.
const DefaultCurveDaysFallback = 90

// dateToTime parses an ISO date; invalid dates return the zero time.
func dateToTime(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}
