# not-a-budget (NAB)

An [MCP bundle](https://github.com/anthropics/mcpb) (`.mcpb`) that manages a
**quarterly cash-flow budget** in SQLite. It is not a traditional budgeting app:
it tracks spending against a **flat linear quarterly curve** and supports an
**external-fund credit** system for large one-off purchases.

The server is a single self-contained Go binary (no cgo). With no arguments it
runs as an MCP server over stdio; with a subcommand it acts as a CLI (see
[CLI mode](#cli-mode)).

## Core ideas

- **Flat curve.** Each quarter has one goal (default **$15,000**, configurable).
  The target is a straight line from $0 → goal across `curve_days` (default 90).
  `quarter_status` plots cumulative net spend against that line — answering
  *"how much have I spent this quarter vs budget?"*
- **External-fund credits.** Big funded purchases (car work, vacation, pool) are
  paid from savings and recorded with `add_credit`; they **subtract from net
  spend** so they don't eat the general budget.
- **Guided import.** The agent normalizes any transaction CSV/spreadsheet
  (mapping columns, signing amounts, dropping credit-card payments/transfers),
  then imports it; the server **de-duplicates** and filters defensively. Never
  write the SQLite file directly — always go through the import tools or CLI.
- **Consistent output.** Every view returns structured data plus
  `render_instructions`, repeated in each tool's description, so charts render
  consistently.

## Budgets are files; tools are read or write

A budget is a single SQLite file. **Every tool takes a `budget` path** — the
SQLite file to operate on. If a default budget is set via `MCPB_BUDGET_DEFAULT`,
the argument may be omitted.

Tools split into two categories:

| Category | Tools | Notes |
|----------|-------|-------|
| **Read** (`readOnlyHint`) | `quarter_status`, `year_summary`, `category_breakdown`, `list_transactions`, `list_credits`, `get_budget_info`, `list_budgets`, `get_cli_info` | Open the budget **read-only** (no disk side effects). Work in stateless, read-only sessions such as live artifacts. |
| **Write** | `set_config`, `import_file`, `import_transactions`, `add_transaction`, `add_credit`, `mark_credit_transferred`, `update_transaction`, `update_credit` | Create/modify a budget; **create the file if it does not exist**. |
| **Delete** (`destructiveHint`) | `delete_transaction`, `delete_credit` | Permanently remove a single record by id. |

This design means a read-only, isolated client (e.g. a live artifact) can call
`quarter_status({budget, quarter})` directly without any stateful setup, and a
new budget is started simply by importing into a fresh path.

See [`docs/INSTRUCTIONS.md`](docs/INSTRUCTIONS.md) and
[`docs/IMPORT_GUIDE.md`](docs/IMPORT_GUIDE.md).

## Configuration

The bundle has no user config: the agent passes the `budget` file path per call.
Optionally, the environment variables `MCPB_BUDGET_DEFAULT` (a default budget
file) and `MCPB_BUDGET_DIR` (folder scanned by `list_budgets`) may be set for the
server; when a default is set, the `budget` argument can be omitted.

## CLI mode

The same binary runs as a CLI when given a subcommand, so a script can bulk
import or query without one MCP call per row. Call `get_cli_info` to get its
absolute path.

```bash
not-a-budget import --budget b.db --csv normalized.csv --source chase_2026q2
cat rows.json | not-a-budget import --budget b.db --json -
not-a-budget quarter-status --budget b.db --quarter 2026-Q2   # JSON on stdout
not-a-budget year-summary --budget b.db
not-a-budget add-credit --budget b.db --date 2026-05-16 --amount 7000 --note Pool
not-a-budget update-transaction --budget b.db --id 42 --amount -12.50 --category Dining
not-a-budget delete-transaction --budget b.db --id 42
not-a-budget update-credit --budget b.db --id 3 --transferred
not-a-budget delete-credit --budget b.db --id 3
```

The CLI has parity with the MCP write/delete tools. `update-transaction` /
`update-credit` are **partial**: only the flags you pass are changed (get ids
from `list_transactions` / `list_credits` or the query output). `delete-*`
remove a single record by id and print `{"ok":true,"deleted_id":N}`.

Import input: CSV needs a header with `txn_date,description,amount` (plus
optional `post_date,category,txn_type,memo`); amounts may include `$`, thousands
separators, or `(parentheses)` for negatives. JSON is an array of row objects or
`{"rows":[...],"source":"..."}`. Both apply the same dedup and payment/transfer
filtering as the MCP tools.

## Data model

One SQLite file per budget:

- `meta` — `quarterly_goal` (15000), `curve_days` (90), `budget_name`, `schema_version`.
- `transactions` — signed `amount` (negative = outflow), normalized `category`,
  `txn_type` (`expense|refund|payment|transfer|other`), `raw_category`, `memo`,
  `source`, and a unique `dedup_hash` = `sha1(txn_date | amount | normalized description)`.
- `credits` — external-fund entries (`date`, positive `amount`, `note`, `note2`,
  `transferred`).

Spending = expense outflows minus refunds; payments and transfers are excluded.
Net quarter total = gross spend − credits.

## Build

Uses [`just`](https://github.com/casey/just):

```bash
just              # list targets
just build        # build host binary + pack dist/not-a-budget.mcpb
just build-all    # cross-compile all platforms into dist/
just check        # gofmt-check + vet + test
just print-version
```

The version lives in **one place** — the top released `## [x.y.z]` heading in
[`CHANGELOG.md`](CHANGELOG.md). `just build` extracts it and injects it into the
binary (via `-ldflags -X main.version`) and into `manifest.json` (via
`sync-version`); nothing else hard-codes it. To cut a release, add a new heading
to the changelog and run `just build`.

Packing requires the `mcpb` CLI (`npm i -g @anthropic-ai/mcpb`, or the build
falls back to `npx @anthropic-ai/mcpb`). `scripts/build.sh` remains as a
just-free fallback.

## Architecture

- `cmd/server` — entry point: dispatches to the CLI (with a subcommand) or the
  MCP stdio server (no args).
- `internal/store` — SQLite schema, migrations, dedup insert, and a read-only
  open mode (pure-Go `modernc.org/sqlite`, no cgo → clean cross-compilation).
- `internal/budget` — quarter math, flat curve, spend/credit aggregation.
- `internal/importer` — CSV/JSON parsing, validation, transfer/payment
  filtering, category normalization, dedup.
- `internal/views` — canonical structured views with render instructions.
- `internal/mcp` — tool registration (read/write categories), per-call budget
  resolution, and handlers.
- `internal/cli` — the script-facing command-line interface.
