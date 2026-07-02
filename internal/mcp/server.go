// Package mcpserver wires the budget domain to MCP tools. Tool descriptions are
// deliberately instruction-rich: agents frequently ignore bundle-level guidance,
// so each tool restates how to normalize input and render output.
//
// Tools fall into two categories:
//   - READ tools (readOnlyHint) never modify anything. They open the budget
//     read-only, so they work in stateless, read-only sessions (e.g. sandboxed
//     artifacts).
//   - WRITE tools create/modify a budget, creating the file if it does not exist.
//
// Every tool takes a "budget" file path. Resolution order is: explicit budget
// param -> a default budget configured via MCPB_BUDGET_DEFAULT (if any).
package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmayfield/mcpb-budget/internal/budget"
	"github.com/bmayfield/mcpb-budget/internal/importer"
	"github.com/bmayfield/mcpb-budget/internal/store"
	"github.com/bmayfield/mcpb-budget/internal/views"
)

const noBudgetMsg = "no budget selected — pass \"budget\": the path to the SQLite budget file to use"

// budgetHint is appended to tool descriptions so the model knows to pass the
// budget file path per call.
const budgetHint = " Pass \"budget\": the path to the SQLite budget file to operate on."

// Handlers holds optional server state. A default budget (from
// MCPB_BUDGET_DEFAULT) may be opened at startup; otherwise every call supplies
// its own "budget" path.
type Handlers struct {
	mu            sync.Mutex
	active        *store.Store // the default budget opened at startup, if any
	defaultBudget string       // configured fallback path (opened read-only on demand)
	budgetDir     string
	now           func() time.Time
}

// New creates a Handlers. If a default budget is configured it is opened
// read-write when possible; if that fails (e.g. a read-only filesystem), the
// path is still remembered and read tools open it read-only on demand.
func New(defaultBudget, budgetDir string) *Handlers {
	h := &Handlers{defaultBudget: strings.TrimSpace(defaultBudget), budgetDir: budgetDir, now: time.Now}
	if h.defaultBudget != "" {
		if s, err := store.Open(h.defaultBudget, true); err == nil {
			h.active = s
		} else {
			fmt.Fprintf(os.Stderr, "not-a-budget: default budget not opened read-write (%v); read tools will open it read-only on demand\n", err)
		}
	}
	return h
}

// Annotation helpers. Clients (e.g. Claude Desktop) use these hints to group
// tools in the permission UI: readOnlyHint splits Read-only vs Write/delete;
// tools with no annotations fall into a generic "Other tools" bucket, so every
// tool here carries annotations. This is a closed system (a local SQLite file),
// hence openWorldHint=false throughout.

func readOnly() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &no}
}

// write returns annotations for a non-read-only tool. None of this server's
// write tools delete or overwrite data (imports de-duplicate, adds are
// additive, updates are in place), so destructiveHint is always false.
func write(idempotent bool) *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: idempotent, OpenWorldHint: &no}
}

// Register attaches all tools to the server.
func (h *Handlers) Register(s *mcp.Server) {
	// ---- WRITE tools (create the budget file if it does not exist) ----
	mcp.AddTool(s, &mcp.Tool{
		Name: "set_config",
		Description: "Update a budget's configuration. quarterly_goal is the flat-curve target per quarter (default 15000). " +
			"curve_days is the number of days the flat curve spans (default 90). Only provided fields are changed." + budgetHint,
		Annotations: write(true),
	}, h.SetConfig)

	mcp.AddTool(s, &mcp.Tool{
		Name: "import_file",
		Description: "PREFERRED way to import more than a handful of transactions. Write the normalized transactions to a " +
			"CSV or JSON file (you have filesystem tools for this), then pass the file PATH here. This avoids array-parameter " +
			"limits some clients impose and handles thousands of rows cheaply. " +
			"CSV needs a header row with columns from {txn_date,post_date,description,amount,category,txn_type,memo} " +
			"(txn_date, description, amount required). JSON is an array of row objects, or {\"rows\":[...],\"source\":\"...\"}. " +
			"Applies the SAME normalization, payment/transfer filtering, and dedup (sha1 of txn_date|amount|normalized description) " +
			"as import_transactions. DO NOT write to the budget's SQLite file directly — always import through this tool, " +
			"import_transactions, or the CLI. Returns {inserted, skipped_duplicates, filtered_out, warnings}; relay them." + budgetHint,
		Annotations: write(true),
	}, h.ImportFile)

	mcp.AddTool(s, &mcp.Tool{
		Name: "import_transactions",
		Description: "Import pre-normalized transactions. For MORE THAN ~50 rows, prefer import_file " +
			"(write a CSV/JSON file and pass its path) — it avoids client array-parameter limits. If this client cannot pass the " +
			"`rows` array, pass the rows as a JSON string in `rows_json` instead. NEVER work around a failure by writing to the " +
			"SQLite file directly; use import_file or the CLI (get_cli_info). " +
			"NORMALIZATION RULES: (1) Map columns to {txn_date (ISO YYYY-MM-DD), description, amount, category, txn_type, memo}. " +
			"(2) amount is SIGNED: negative = money out (spending), positive = money in. " +
			"(3) Set txn_type to one of: expense | refund | payment | transfer | other. " +
			"(4) DROP non-spending rows: credit-card payments ('AUTOMATIC PAYMENT', 'THANK YOU'), account transfers, " +
			"and deposits — mark them txn_type=payment or transfer and this tool will filter them out (they are NOT spending). " +
			"(5) Categories are normalized to a canonical set; unknown categories become 'Other'. " +
			"(6) Deduplication key is sha1(txn_date|amount|normalized description) — safe to re-import the same file. " +
			"Returns a report: {inserted, skipped_duplicates, filtered_out, warnings}. ALWAYS relay the filtered_out and " +
			"skipped_duplicates counts and any warnings to the user." + budgetHint,
		Annotations: write(true),
	}, h.ImportTransactions)

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_transaction",
		Description: "Add a single transaction. Use the same rules as import_transactions: signed amount (negative = spend), " +
			"txn_type in {expense,refund,payment,transfer,other}. Payments/transfers are filtered out (not spending)." + budgetHint,
		Annotations: write(true),
	}, h.AddTransaction)

	mcp.AddTool(s, &mcp.Tool{
		Name: "add_credit",
		Description: "Add an external-fund credit. Credits are transfers from savings/external accounts that fund large " +
			"one-off purchases (car work, vacation, etc.) so they do NOT consume the general quarterly budget. " +
			"amount is POSITIVE; the credit subtracts from net spend on its date. Set transferred=false to track a pending transfer." + budgetHint,
		Annotations: write(false), // each call inserts a new credit — not idempotent
	}, h.AddCredit)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mark_credit_transferred",
		Description: "Mark an external-fund credit as transferred (or not), by credit id." + budgetHint,
		Annotations: write(true),
	}, h.MarkCreditTransferred)

	// ---- READ tools (read-only; safe for stateless / sandboxed sessions) ----
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_budget_info",
		Description: "Return a budget's path and configuration (quarterly_goal, curve_days, budget_name)." + budgetHint,
		Annotations: readOnly(),
	}, h.GetBudgetInfo)

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_budgets",
		Description: "List *.db/*.sqlite budget files found in the configured budgets folder. " +
			"Use this to help the user pick which budget to use.",
		Annotations: readOnly(),
	}, h.ListBudgets)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_transactions",
		Description: "List transactions, optionally filtered by quarter (e.g. \"2026-Q2\") or a date range." + budgetHint,
		Annotations: readOnly(),
	}, h.ListTransactions)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_credits",
		Description: "List external-fund credits, optionally filtered by quarter (e.g. \"2026-Q2\")." + budgetHint,
		Annotations: readOnly(),
	}, h.ListCredits)

	mcp.AddTool(s, &mcp.Tool{
		Name: "quarter_status",
		Description: "THE headline view: 'how much have I spent this quarter vs budget'. " +
			"Defaults to the current quarter; pass quarter like \"2026-Q2\" for another. " +
			"Returns cumulative net spend per day, the flat budget curve, and a summary. " +
			"The result includes render_instructions — FOLLOW THEM to draw the canonical 'Spend vs Budget' chart artifact." + budgetHint,
		Annotations: readOnly(),
	}, h.QuarterStatus)

	mcp.AddTool(s, &mcp.Tool{
		Name: "year_summary",
		Description: "Quarter-over-quarter comparison across all quarters with data: net spend vs goal per quarter. " +
			"The result includes render_instructions — FOLLOW THEM to draw the canonical comparison bar chart." + budgetHint,
		Annotations: readOnly(),
	}, h.YearSummary)

	mcp.AddTool(s, &mcp.Tool{
		Name: "category_breakdown",
		Description: "Per-category spend for a quarter (default current). Categories are analytical only; budgeting is a single " +
			"quarterly total. The result includes render_instructions." + budgetHint,
		Annotations: readOnly(),
	}, h.CategoryBreakdown)

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_cli_info",
		Description: "Return the absolute path to this server's binary and usage for its command-line mode. " +
			"Use this when you want to write a SCRIPT (e.g. Python) that bulk-imports many transactions or queries a budget " +
			"WITHOUT making one MCP tool call per operation. The same binary runs as a CLI when given a subcommand " +
			"(import / quarter-status / year-summary / category-breakdown / add-credit); it applies the identical dedup, " +
			"payment/transfer filtering, and aggregation logic as these tools. Prefer this for large CSV imports.",
		Annotations: readOnly(),
	}, h.GetCLIInfo)
}

// resolve returns the store for a budget, resolving the path from (in order) the
// explicit param, the active session budget, or the configured default. Read
// resolutions open the file read-only (no disk side effects); write resolutions
// open read-write. The returned release func must be called by the caller (it
// closes any freshly opened store and is a no-op for the shared active store).
func (h *Handlers) resolve(budgetParam string, write bool) (*store.Store, func(), error) {
	h.mu.Lock()
	active := h.active
	def := h.defaultBudget
	h.mu.Unlock()

	path := strings.TrimSpace(budgetParam)
	if path == "" {
		if active != nil {
			return active, func() {}, nil
		}
		path = def
	}
	if path == "" {
		return nil, nil, fmt.Errorf(noBudgetMsg)
	}
	// Reuse the shared active (read-write) handle when it matches the target.
	if active != nil && active.Path() == path {
		return active, func() {}, nil
	}
	var (
		s   *store.Store
		err error
	)
	if write {
		// Write tools create the budget file if it does not yet exist.
		s, err = store.Open(path, true)
	} else {
		s, err = store.OpenReadOnly(path)
	}
	if err != nil {
		return nil, nil, err
	}
	return s, func() { s.Close() }, nil
}

// ---- WRITE tool I/O and handlers ----

type MetaOut struct {
	Budget store.Meta `json:"budget"`
}

type SetConfigIn struct {
	Budget        string   `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	QuarterlyGoal *float64 `json:"quarterly_goal,omitempty" jsonschema:"flat-curve target per quarter"`
	CurveDays     *int     `json:"curve_days,omitempty" jsonschema:"days the flat curve spans (default 90)"`
	BudgetName    *string  `json:"budget_name,omitempty" jsonschema:"display name for this budget"`
}

func (h *Handlers) SetConfig(ctx context.Context, _ *mcp.CallToolRequest, in SetConfigIn) (*mcp.CallToolResult, MetaOut, error) {
	s, release, err := h.resolve(in.Budget, true)
	if err != nil {
		return nil, MetaOut{}, err
	}
	defer release()
	if in.QuarterlyGoal != nil {
		if err := s.SetMetaValue("quarterly_goal", fmt.Sprintf("%g", *in.QuarterlyGoal)); err != nil {
			return nil, MetaOut{}, err
		}
	}
	if in.CurveDays != nil {
		if *in.CurveDays <= 0 {
			return nil, MetaOut{}, fmt.Errorf("curve_days must be positive")
		}
		if err := s.SetMetaValue("curve_days", fmt.Sprintf("%d", *in.CurveDays)); err != nil {
			return nil, MetaOut{}, err
		}
	}
	if in.BudgetName != nil {
		if err := s.SetMetaValue("budget_name", *in.BudgetName); err != nil {
			return nil, MetaOut{}, err
		}
	}
	meta, err := s.GetMeta()
	if err != nil {
		return nil, MetaOut{}, err
	}
	return nil, MetaOut{Budget: meta}, nil
}

type ImportIn struct {
	Budget   string         `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	Rows     []importer.Row `json:"rows,omitempty" jsonschema:"pre-normalized transaction rows"`
	RowsJSON string         `json:"rows_json,omitempty" jsonschema:"alternative to rows: the rows as a JSON string (array of row objects, or {\"rows\":[...]}). Use this if the client cannot pass an array parameter."`
	Source   string         `json:"source,omitempty" jsonschema:"optional label for this import batch (e.g. source file name)"`
}

type ImportOut struct {
	Report importer.Report `json:"report"`
}

func (h *Handlers) ImportTransactions(ctx context.Context, _ *mcp.CallToolRequest, in ImportIn) (*mcp.CallToolResult, ImportOut, error) {
	s, release, err := h.resolve(in.Budget, true)
	if err != nil {
		return nil, ImportOut{}, err
	}
	defer release()
	rows := in.Rows
	source := in.Source
	if strings.TrimSpace(in.RowsJSON) != "" {
		parsed, wrapSrc, err := importer.ReadJSON(strings.NewReader(in.RowsJSON))
		if err != nil {
			return nil, ImportOut{}, fmt.Errorf("rows_json: %w", err)
		}
		rows = append(rows, parsed...)
		if source == "" {
			source = wrapSrc
		}
	}
	rep, err := importer.Import(s, rows, source)
	if err != nil {
		return nil, ImportOut{}, err
	}
	return nil, ImportOut{Report: rep}, nil
}

type ImportFileIn struct {
	Budget string `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	Path   string `json:"path" jsonschema:"absolute path to a normalized CSV or JSON file to import"`
	Format string `json:"format,omitempty" jsonschema:"csv or json; inferred from the file extension when omitted"`
	Source string `json:"source,omitempty" jsonschema:"optional batch label (defaults to the file name)"`
}

func (h *Handlers) ImportFile(ctx context.Context, _ *mcp.CallToolRequest, in ImportFileIn) (*mcp.CallToolResult, ImportOut, error) {
	if in.Path == "" {
		return nil, ImportOut{}, fmt.Errorf("path is required")
	}
	f, err := os.Open(in.Path)
	if err != nil {
		return nil, ImportOut{}, err
	}
	defer f.Close()

	format := strings.ToLower(strings.TrimSpace(in.Format))
	if format == "" {
		switch strings.ToLower(filepath.Ext(in.Path)) {
		case ".json":
			format = "json"
		default:
			format = "csv"
		}
	}
	source := in.Source
	if source == "" {
		source = filepath.Base(in.Path)
	}

	var rows []importer.Row
	switch format {
	case "csv":
		rows, err = importer.ReadCSV(f)
	case "json":
		var wrapSrc string
		rows, wrapSrc, err = importer.ReadJSON(f)
		if err == nil && in.Source == "" && wrapSrc != "" {
			source = wrapSrc
		}
	default:
		return nil, ImportOut{}, fmt.Errorf("unknown format %q (use csv or json)", format)
	}
	if err != nil {
		return nil, ImportOut{}, err
	}

	s, release, err := h.resolve(in.Budget, true)
	if err != nil {
		return nil, ImportOut{}, err
	}
	defer release()
	rep, err := importer.Import(s, rows, source)
	if err != nil {
		return nil, ImportOut{}, err
	}
	return nil, ImportOut{Report: rep}, nil
}

type AddTxnIn struct {
	Budget      string  `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	TxnDate     string  `json:"txn_date" jsonschema:"ISO YYYY-MM-DD"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount" jsonschema:"signed; negative = spend"`
	Category    string  `json:"category,omitempty"`
	TxnType     string  `json:"txn_type,omitempty" jsonschema:"expense|refund|payment|transfer|other"`
	Memo        string  `json:"memo,omitempty"`
}

type AddTxnOut struct {
	Report importer.Report `json:"report"`
}

func (h *Handlers) AddTransaction(ctx context.Context, _ *mcp.CallToolRequest, in AddTxnIn) (*mcp.CallToolResult, AddTxnOut, error) {
	s, release, err := h.resolve(in.Budget, true)
	if err != nil {
		return nil, AddTxnOut{}, err
	}
	defer release()
	rep, err := importer.Import(s, []importer.Row{{
		TxnDate: in.TxnDate, Description: in.Description, Amount: in.Amount,
		Category: in.Category, TxnType: in.TxnType, Memo: in.Memo,
	}}, "manual")
	if err != nil {
		return nil, AddTxnOut{}, err
	}
	return nil, AddTxnOut{Report: rep}, nil
}

type AddCreditIn struct {
	Budget      string  `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	Date        string  `json:"date" jsonschema:"ISO YYYY-MM-DD"`
	Amount      float64 `json:"amount" jsonschema:"positive; offsets net spend"`
	Note        string  `json:"note,omitempty"`
	Note2       string  `json:"note2,omitempty"`
	Transferred bool    `json:"transferred,omitempty" jsonschema:"whether the money has been transferred yet"`
}

type AddCreditOut struct {
	ID int64 `json:"id"`
}

func (h *Handlers) AddCredit(ctx context.Context, _ *mcp.CallToolRequest, in AddCreditIn) (*mcp.CallToolResult, AddCreditOut, error) {
	if in.Date == "" {
		return nil, AddCreditOut{}, fmt.Errorf("date is required")
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return nil, AddCreditOut{}, fmt.Errorf("bad date %q (need YYYY-MM-DD)", in.Date)
	}
	s, release, err := h.resolve(in.Budget, true)
	if err != nil {
		return nil, AddCreditOut{}, err
	}
	defer release()
	id, err := s.InsertCredit(store.Credit{
		Date: in.Date, Amount: in.Amount, Note: in.Note, Note2: in.Note2, Transferred: in.Transferred,
	})
	if err != nil {
		return nil, AddCreditOut{}, err
	}
	return nil, AddCreditOut{ID: id}, nil
}

type MarkCreditIn struct {
	Budget      string `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	ID          int64  `json:"id"`
	Transferred bool   `json:"transferred"`
}

type OKOut struct {
	OK bool `json:"ok"`
}

func (h *Handlers) MarkCreditTransferred(ctx context.Context, _ *mcp.CallToolRequest, in MarkCreditIn) (*mcp.CallToolResult, OKOut, error) {
	s, release, err := h.resolve(in.Budget, true)
	if err != nil {
		return nil, OKOut{}, err
	}
	defer release()
	if err := s.MarkCreditTransferred(in.ID, in.Transferred); err != nil {
		return nil, OKOut{}, err
	}
	return nil, OKOut{OK: true}, nil
}

// ---- READ tool I/O and handlers ----

type BudgetIn struct {
	Budget string `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
}

func (h *Handlers) GetBudgetInfo(ctx context.Context, _ *mcp.CallToolRequest, in BudgetIn) (*mcp.CallToolResult, MetaOut, error) {
	s, release, err := h.resolve(in.Budget, false)
	if err != nil {
		return nil, MetaOut{}, err
	}
	defer release()
	meta, err := s.GetMeta()
	if err != nil {
		return nil, MetaOut{}, err
	}
	return nil, MetaOut{Budget: meta}, nil
}

type ListBudgetsOut struct {
	Dir   string   `json:"dir"`
	Files []string `json:"files"`
}

func (h *Handlers) ListBudgets(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ListBudgetsOut, error) {
	out := ListBudgetsOut{Dir: h.budgetDir}
	if h.budgetDir == "" {
		return nil, out, nil
	}
	entries, err := os.ReadDir(h.budgetDir)
	if err != nil {
		return nil, out, nil // no dir is not an error; just report empty
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" {
			out.Files = append(out.Files, filepath.Join(h.budgetDir, name))
		}
	}
	sort.Strings(out.Files)
	return nil, out, nil
}

type ListTxnIn struct {
	Budget  string `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	Quarter string `json:"quarter,omitempty" jsonschema:"e.g. 2026-Q2; overrides start/end if set"`
	Start   string `json:"start,omitempty" jsonschema:"ISO YYYY-MM-DD"`
	End     string `json:"end,omitempty" jsonschema:"ISO YYYY-MM-DD"`
}

type ListTxnOut struct {
	Transactions []store.Transaction `json:"transactions"`
	Count        int                 `json:"count"`
}

func (h *Handlers) resolveRange(in ListTxnIn) (string, string, error) {
	if in.Quarter != "" {
		q, err := budget.ParseQuarter(in.Quarter, h.now())
		if err != nil {
			return "", "", err
		}
		return q.Start, q.End, nil
	}
	start := in.Start
	end := in.End
	if start == "" {
		start = "0000-01-01"
	}
	if end == "" {
		end = "9999-12-31"
	}
	return start, end, nil
}

func (h *Handlers) ListTransactions(ctx context.Context, _ *mcp.CallToolRequest, in ListTxnIn) (*mcp.CallToolResult, ListTxnOut, error) {
	s, release, err := h.resolve(in.Budget, false)
	if err != nil {
		return nil, ListTxnOut{}, err
	}
	defer release()
	start, end, err := h.resolveRange(in)
	if err != nil {
		return nil, ListTxnOut{}, err
	}
	txns, err := s.TransactionsBetween(start, end)
	if err != nil {
		return nil, ListTxnOut{}, err
	}
	return nil, ListTxnOut{Transactions: txns, Count: len(txns)}, nil
}

type ListCreditsIn struct {
	Budget  string `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	Quarter string `json:"quarter,omitempty" jsonschema:"e.g. 2026-Q2; omit for all credits"`
}

type ListCreditsOut struct {
	Credits []store.Credit `json:"credits"`
	Count   int            `json:"count"`
}

func (h *Handlers) ListCredits(ctx context.Context, _ *mcp.CallToolRequest, in ListCreditsIn) (*mcp.CallToolResult, ListCreditsOut, error) {
	s, release, err := h.resolve(in.Budget, false)
	if err != nil {
		return nil, ListCreditsOut{}, err
	}
	defer release()
	var credits []store.Credit
	if in.Quarter != "" {
		q, err := budget.ParseQuarter(in.Quarter, h.now())
		if err != nil {
			return nil, ListCreditsOut{}, err
		}
		credits, err = s.CreditsBetween(q.Start, q.End)
		if err != nil {
			return nil, ListCreditsOut{}, err
		}
	} else {
		credits, err = s.AllCredits()
		if err != nil {
			return nil, ListCreditsOut{}, err
		}
	}
	return nil, ListCreditsOut{Credits: credits, Count: len(credits)}, nil
}

type QuarterIn struct {
	Budget  string `json:"budget,omitempty" jsonschema:"budget file path (optional)"`
	Quarter string `json:"quarter,omitempty" jsonschema:"e.g. 2026-Q2; defaults to the current quarter"`
}

func (h *Handlers) QuarterStatus(ctx context.Context, _ *mcp.CallToolRequest, in QuarterIn) (*mcp.CallToolResult, views.QuarterView, error) {
	s, release, err := h.resolve(in.Budget, false)
	if err != nil {
		return nil, views.QuarterView{}, err
	}
	defer release()
	q, err := budget.ParseQuarter(in.Quarter, h.now())
	if err != nil {
		return nil, views.QuarterView{}, err
	}
	v, err := views.BuildQuarter(s, q, h.now())
	if err != nil {
		return nil, views.QuarterView{}, err
	}
	return nil, v, nil
}

func (h *Handlers) YearSummary(ctx context.Context, _ *mcp.CallToolRequest, in BudgetIn) (*mcp.CallToolResult, views.YearView, error) {
	s, release, err := h.resolve(in.Budget, false)
	if err != nil {
		return nil, views.YearView{}, err
	}
	defer release()
	v, err := views.BuildYear(s, h.now())
	if err != nil {
		return nil, views.YearView{}, err
	}
	return nil, v, nil
}

func (h *Handlers) CategoryBreakdown(ctx context.Context, _ *mcp.CallToolRequest, in QuarterIn) (*mcp.CallToolResult, views.CategoryBreakdown, error) {
	s, release, err := h.resolve(in.Budget, false)
	if err != nil {
		return nil, views.CategoryBreakdown{}, err
	}
	defer release()
	q, err := budget.ParseQuarter(in.Quarter, h.now())
	if err != nil {
		return nil, views.CategoryBreakdown{}, err
	}
	v, err := views.BuildCategoryBreakdown(s, q)
	if err != nil {
		return nil, views.CategoryBreakdown{}, err
	}
	return nil, v, nil
}

// CLIInfoOut describes how to invoke the bundled binary from a script.
type CLIInfoOut struct {
	BinaryPath    string   `json:"binary_path"`
	CSVColumns    []string `json:"csv_columns"`
	CSVRequired   []string `json:"csv_required_columns"`
	Examples      []string `json:"examples"`
	Notes         string   `json:"notes"`
	DefaultBudget string   `json:"default_budget,omitempty"`
}

func (h *Handlers) GetCLIInfo(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, CLIInfoOut, error) {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "mcpb-budget" // fall back to PATH lookup
	}
	out := CLIInfoOut{
		BinaryPath:  bin,
		CSVColumns:  importer.CanonicalCSVColumns,
		CSVRequired: importer.RequiredCSVColumns,
		Examples: []string{
			bin + ` import --budget /path/budget.db --csv normalized.csv --source chase_2026q2`,
			bin + ` import --budget /path/budget.db --json rows.json`,
			`cat rows.json | ` + bin + ` import --budget /path/budget.db --json -`,
			bin + ` quarter-status --budget /path/budget.db --quarter 2026-Q2`,
			bin + ` year-summary --budget /path/budget.db`,
			bin + ` category-breakdown --budget /path/budget.db --quarter 2026-Q2`,
		},
		Notes: "Amount is signed (negative = money out). txn_type is one of expense|refund|payment|transfer|other; " +
			"payments and transfers are filtered out. Rows are de-duplicated on sha1(txn_date|amount|normalized description), " +
			"so re-running an import is safe. All subcommands print JSON to stdout and exit non-zero on error. " +
			"Normalize your source data to these columns first (see the IMPORT_GUIDE). " +
			"Pass --create to import into a brand-new budget file. " +
			"This CLI (or the import_file / import_transactions tools) is the ONLY supported way to add records — " +
			"never write to the SQLite file directly, as that bypasses validation and the dedup invariants.",
	}
	h.mu.Lock()
	if h.active != nil {
		out.DefaultBudget = h.active.Path()
	} else {
		out.DefaultBudget = h.defaultBudget
	}
	h.mu.Unlock()
	return nil, out, nil
}
