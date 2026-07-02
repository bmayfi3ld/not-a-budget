// Command not-a-budget (NAB) is the MCP server for the quarterly cash-flow
// budget bundle. It speaks MCP over stdio.
package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bmayfield/mcpb-budget/internal/cli"
	mcpserver "github.com/bmayfield/mcpb-budget/internal/mcp"
)

// version is the release version, injected at build time via
// -ldflags "-X main.version=...". It falls back to "dev" for plain `go build`.
var version = "dev"

// serverInstructions is surfaced to the client as top-level guidance. The
// authoritative copies live inside each tool description because agents often
// ignore this block.
const serverInstructions = `NAB (Not A Budget) manages a quarterly cash-flow budget stored in a SQLite file.

Core model:
- A "budget" is a single SQLite file. Every tool takes a "budget" path — pass the SQLite file to
  operate on. Read tools (quarter_status, year_summary, category_breakdown, list_*, get_budget_info)
  are read-only and open the file read-only, so they work in stateless, read-only sessions such as
  live artifacts. Write tools create the budget file if it does not yet exist.
- Spending is tracked against a FLAT quarterly curve: a straight line from $0 to the
  quarterly goal (default $15,000) across the quarter. quarter_status shows spend vs that line.
- External-fund CREDITS (add_credit) are transfers from savings that fund large one-off
  purchases so they do not consume the general budget; they subtract from net spend.
- To import transactions from a CSV/spreadsheet: read and normalize the rows yourself
  (map columns, sign amounts, drop credit-card payments and account transfers). For more than a
  handful of rows, write them to a normalized CSV/JSON file and call import_file with its path
  (this avoids client array-parameter limits); for a few rows, import_transactions works too.
  Both dedup and filter defensively and return a report.
- NEVER write to the budget's SQLite file directly. If a bulk import through import_transactions
  fails (e.g. the client cannot pass an array), use import_file, pass rows_json as a string, or
  the CLI — do not hand-roll SQL inserts. Direct writes bypass validation and dedup invariants.

For LARGE imports or when writing a script, call get_cli_info: the same binary runs as a CLI
(subcommands import/quarter-status/year-summary/category-breakdown/add-credit) so a script can
operate on a budget through the sanctioned code path instead of making one tool call per row.

Always follow the render_instructions returned by quarter_status / year_summary / category_breakdown.`

func main() {
	log.SetFlags(0)

	// With arguments, act as a CLI (script-facing). With none, the MCP client
	// launched us — run the stdio server. "serve" forces server mode explicitly.
	cli.Version = version
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		os.Exit(cli.Run(os.Args[1:]))
	}

	defaultBudget := os.Getenv("MCPB_BUDGET_DEFAULT")
	budgetDir := os.Getenv("MCPB_BUDGET_DIR")

	handlers := mcpserver.New(defaultBudget, budgetDir)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "not-a-budget",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})
	handlers.Register(server)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("not-a-budget: %v", err)
	}
}
