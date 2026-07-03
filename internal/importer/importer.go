// Package importer validates, filters, dedups, and inserts pre-normalized
// transaction rows produced by the agent. The tool that calls it is the final
// safety net: even if the agent forgets to drop transfers/credit-card payments,
// they are filtered here.
package importer

import (
	"fmt"
	"strings"
	"time"

	"github.com/bmayfield/mcpb-budget/internal/budget"
	"github.com/bmayfield/mcpb-budget/internal/store"
)

// Row is one pre-normalized transaction supplied to the import tool.
type Row struct {
	TxnDate     string  `json:"txn_date" jsonschema:"transaction date, ISO YYYY-MM-DD"`
	PostDate    string  `json:"post_date,omitempty" jsonschema:"posting date if different, ISO YYYY-MM-DD"`
	Description string  `json:"description" jsonschema:"merchant/description text"`
	Amount      float64 `json:"amount" jsonschema:"signed amount; negative = money out, positive = money in"`
	Category    string  `json:"category,omitempty" jsonschema:"category; will be normalized to the canonical set"`
	TxnType     string  `json:"txn_type,omitempty" jsonschema:"one of expense, refund, payment, transfer, other"`
	Memo        string  `json:"memo,omitempty" jsonschema:"optional note"`
}

// Report summarizes an import run.
type Report struct {
	Inserted          int      `json:"inserted"`
	SkippedDuplicates int      `json:"skipped_duplicates"`
	FilteredOut       int      `json:"filtered_out"`
	Warnings          []string `json:"warnings"`
}

// ClassifyType normalizes a free-form transaction type string, inferring from
// the description and amount sign when empty. Exported for the update tools,
// which re-normalize an edited type the same way an import would.
func ClassifyType(raw, description string, amount float64) string {
	return classifyType(raw, description, amount)
}

// classifyType normalizes a free-form type string. If empty, it infers from the
// amount sign and description (credit-card payments / transfers are detected by
// common keywords).
func classifyType(raw, description string, amount float64) string {
	t := strings.ToLower(strings.TrimSpace(raw))
	switch t {
	case budget.TypeExpense, budget.TypeRefund, budget.TypePayment, budget.TypeTransfer, budget.TypeOther:
		return t
	}
	// Map common source type labels.
	switch t {
	case "sale", "debit", "misc_debit", "quickpay_debit", "loan_pmt", "atm", "fee":
		return budget.TypeExpense
	case "return", "refund", "misc_credit", "refunds & reimbursements":
		return budget.TypeRefund
	case "payment", "credit card payment", "credit_card_payment", "quickpay_credit":
		return budget.TypePayment
	case "transfer", "transfers", "ach_credit", "deposit", "deposits":
		return budget.TypeTransfer
	}
	// Infer from description keywords.
	dl := strings.ToLower(description)
	for _, kw := range []string{"automatic payment", "autopay", "payment - thank", "online payment", "e-payment"} {
		if strings.Contains(dl, kw) {
			return budget.TypePayment
		}
	}
	for _, kw := range []string{"transfer", "xfer", "to savings", "from savings"} {
		if strings.Contains(dl, kw) {
			return budget.TypeTransfer
		}
	}
	// Fallback by sign: money out = expense, money in = refund/other.
	if amount < 0 {
		return budget.TypeExpense
	}
	return budget.TypeOther
}

// Import validates and inserts rows into the store, filtering out
// payments/transfers and skipping duplicates.
func Import(s *store.Store, rows []Row, source string) (Report, error) {
	var rep Report
	seen := map[string]bool{} // dedup within this batch too
	for i, r := range rows {
		if r.TxnDate == "" {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("row %d: missing txn_date, skipped", i))
			continue
		}
		if _, err := time.Parse("2006-01-02", r.TxnDate); err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("row %d: bad txn_date %q (need YYYY-MM-DD), skipped", i, r.TxnDate))
			continue
		}
		if r.Description == "" {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("row %d: missing description, skipped", i))
			continue
		}
		typ := classifyType(r.TxnType, r.Description, r.Amount)
		if typ == budget.TypePayment || typ == budget.TypeTransfer {
			rep.FilteredOut++
			continue
		}
		hash := store.DedupHash(r.TxnDate, r.Amount, r.Description)
		if seen[hash] {
			rep.SkippedDuplicates++
			continue
		}
		seen[hash] = true

		txn := store.Transaction{
			TxnDate:     r.TxnDate,
			PostDate:    r.PostDate,
			Description: r.Description,
			Amount:      r.Amount,
			Category:    NormalizeCategory(r.Category),
			RawCategory: strings.TrimSpace(r.Category),
			TxnType:     typ,
			Memo:        r.Memo,
			Source:      source,
		}
		inserted, err := s.InsertTransaction(txn)
		if err != nil {
			return rep, fmt.Errorf("insert row %d: %w", i, err)
		}
		if inserted {
			rep.Inserted++
		} else {
			rep.SkippedDuplicates++
		}
	}
	return rep, nil
}
