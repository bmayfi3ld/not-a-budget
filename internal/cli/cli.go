// Package cli implements the script-facing command-line interface. The same
// binary that serves MCP over stdio (with no arguments) also exposes these
// subcommands, so an agent can bulk-import or query a budget from a script via
// subprocess without going through MCP tool calls. All commands reuse the exact
// import/dedup/aggregation logic used by the MCP tools.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bmayfield/mcpb-budget/internal/budget"
	"github.com/bmayfield/mcpb-budget/internal/importer"
	"github.com/bmayfield/mcpb-budget/internal/store"
	"github.com/bmayfield/mcpb-budget/internal/views"
)

// Version is the release version, set from main (which receives it via ldflags).
var Version = "dev"

const usage = `not-a-budget (NAB) — quarterly cash-flow budget

With no arguments the binary runs as an MCP server over stdio. The subcommands
below let a script operate on a budget file directly. All output is JSON on stdout.

Usage:
  not-a-budget <command> [flags]

Commands:
  import              Bulk import canonical transactions from CSV or JSON
  quarter-status      Print the spend-vs-budget view for a quarter
  year-summary        Print the quarter-over-quarter comparison
  category-breakdown  Print per-category spend for a quarter
  add-credit          Add an external-fund credit
  version             Print the version
  help                Show this help

Run "mcpb-budget <command> -h" for command flags.

Import input formats:
  CSV  header row with any of: txn_date,post_date,description,amount,category,txn_type,memo
       (txn_date, description, amount required; amount signed, negative = money out)
  JSON either a top-level array of row objects, or {"rows":[...],"source":"..."}
       row objects use the same field names as the CSV columns.

Deduplication and payment/transfer filtering are applied exactly as by the
import_transactions MCP tool.`

// Run dispatches a subcommand. args is os.Args[1:]. Returns a process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "import":
		return cmdImport(rest)
	case "quarter-status":
		return cmdQuarterStatus(rest)
	case "year-summary":
		return cmdYearSummary(rest)
	case "category-breakdown":
		return cmdCategoryBreakdown(rest)
	case "add-credit":
		return cmdAddCredit(rest)
	case "version", "--version", "-v":
		fmt.Fprintln(os.Stdout, Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprintln(os.Stdout, usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s\n", cmd, usage)
		return 2
	}
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "mcpb-budget: "+format+"\n", a...)
	return 1
}

func openBudget(path string, create bool) (*store.Store, int) {
	if path == "" {
		return nil, fail("--budget is required")
	}
	s, err := store.Open(path, create)
	if err != nil {
		return nil, fail("%v (pass --create to make a new budget)", err)
	}
	return s, 0
}

func emit(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("encode output: %v", err)
	}
	return 0
}

func cmdImport(args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	budgetPath := fs.String("budget", "", "path to the budget SQLite file")
	csvPath := fs.String("csv", "", "canonical CSV file to import (\"-\" for stdin)")
	jsonPath := fs.String("json", "", "canonical JSON file to import (\"-\" for stdin)")
	source := fs.String("source", "", "label for this import batch (defaults to the file name)")
	create := fs.Bool("create", false, "create the budget file if it does not exist")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*csvPath == "") == (*jsonPath == "") {
		return fail("provide exactly one of --csv or --json")
	}

	var rows []importer.Row
	var defaultSource string
	var err error
	if *csvPath != "" {
		rows, defaultSource, err = readCSV(*csvPath)
	} else {
		rows, defaultSource, err = readJSON(*jsonPath)
	}
	if err != nil {
		return fail("%v", err)
	}
	src := *source
	if src == "" {
		src = defaultSource
	}

	s, code := openBudget(*budgetPath, *create)
	if code != 0 {
		return code
	}
	defer s.Close()

	rep, err := importer.Import(s, rows, src)
	if err != nil {
		return fail("import: %v", err)
	}
	return emit(rep)
}

func cmdQuarterStatus(args []string) int {
	fs := flag.NewFlagSet("quarter-status", flag.ContinueOnError)
	budgetPath := fs.String("budget", "", "path to the budget SQLite file")
	quarter := fs.String("quarter", "", "quarter spec, e.g. 2026-Q2 (default: current)")
	fs.Bool("json", true, "output JSON (always on)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, code := openBudget(*budgetPath, false)
	if code != 0 {
		return code
	}
	defer s.Close()
	q, err := budget.ParseQuarter(*quarter, time.Now())
	if err != nil {
		return fail("%v", err)
	}
	v, err := views.BuildQuarter(s, q, time.Now())
	if err != nil {
		return fail("%v", err)
	}
	return emit(v)
}

func cmdYearSummary(args []string) int {
	fs := flag.NewFlagSet("year-summary", flag.ContinueOnError)
	budgetPath := fs.String("budget", "", "path to the budget SQLite file")
	fs.Bool("json", true, "output JSON (always on)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, code := openBudget(*budgetPath, false)
	if code != 0 {
		return code
	}
	defer s.Close()
	v, err := views.BuildYear(s, time.Now())
	if err != nil {
		return fail("%v", err)
	}
	return emit(v)
}

func cmdCategoryBreakdown(args []string) int {
	fs := flag.NewFlagSet("category-breakdown", flag.ContinueOnError)
	budgetPath := fs.String("budget", "", "path to the budget SQLite file")
	quarter := fs.String("quarter", "", "quarter spec, e.g. 2026-Q2 (default: current)")
	fs.Bool("json", true, "output JSON (always on)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	s, code := openBudget(*budgetPath, false)
	if code != 0 {
		return code
	}
	defer s.Close()
	q, err := budget.ParseQuarter(*quarter, time.Now())
	if err != nil {
		return fail("%v", err)
	}
	v, err := views.BuildCategoryBreakdown(s, q)
	if err != nil {
		return fail("%v", err)
	}
	return emit(v)
}

func cmdAddCredit(args []string) int {
	fs := flag.NewFlagSet("add-credit", flag.ContinueOnError)
	budgetPath := fs.String("budget", "", "path to the budget SQLite file")
	date := fs.String("date", "", "credit date, ISO YYYY-MM-DD")
	amount := fs.Float64("amount", 0, "credit amount (positive)")
	note := fs.String("note", "", "optional note")
	note2 := fs.String("note2", "", "optional second note")
	transferred := fs.Bool("transferred", false, "whether the transfer has happened")
	create := fs.Bool("create", false, "create the budget file if it does not exist")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *date == "" {
		return fail("--date is required")
	}
	if _, err := time.Parse("2006-01-02", *date); err != nil {
		return fail("bad --date %q (need YYYY-MM-DD)", *date)
	}
	s, code := openBudget(*budgetPath, *create)
	if code != 0 {
		return code
	}
	defer s.Close()
	id, err := s.InsertCredit(store.Credit{
		Date: *date, Amount: *amount, Note: *note, Note2: *note2, Transferred: *transferred,
	})
	if err != nil {
		return fail("add credit: %v", err)
	}
	return emit(map[string]any{"id": id})
}

// ---- input readers ----

func openInput(path string) (io.ReadCloser, string, error) {
	if path == "-" {
		return io.NopCloser(os.Stdin), "stdin", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	return f, baseName(path), nil
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func readJSON(path string) ([]importer.Row, string, error) {
	r, src, err := openInput(path)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	rows, wrapSrc, err := importer.ReadJSON(r)
	if err != nil {
		return nil, "", err
	}
	if wrapSrc != "" {
		src = wrapSrc
	}
	return rows, src, nil
}

func readCSV(path string) ([]importer.Row, string, error) {
	r, src, err := openInput(path)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	rows, err := importer.ReadCSV(r)
	if err != nil {
		return nil, "", err
	}
	return rows, src, nil
}
