# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file is the single source of truth for the version number: the most recent
released `## [x.y.z]` heading below drives the version injected into the binary
and `manifest.json` at build time (`just build`).

## [Unreleased]

## [0.6.0] - 2026-07-01

### Added
- MCP annotations on the write tools (`readOnlyHint: false`, `destructiveHint`,
  `idempotentHint`, `openWorldHint: false`). Clients like Claude Desktop group
  tools by these hints, so the write tools now appear under "Write/delete tools"
  instead of the generic "Other tools" bucket (read tools were already grouped
  via `readOnlyHint`). Every tool now carries annotations.

## [0.5.0] - 2026-07-01

### Changed
- Renamed the connector to **not-a-budget** (display name **NAB**); the binary
  and bundle are now `not-a-budget` / `not-a-budget.mcpb`.
- Write tools now create the budget file on demand if it does not exist (this
  replaces `open_budget(create)`).

### Removed
- The `open_budget` tool. It is redundant now that every tool takes a `budget`
  path and write tools create the file on demand.
- The `user_config` settings (`default_budget`, `budget_dir`). The agent passes
  the `budget` path per call; `MCPB_BUDGET_DEFAULT` / `MCPB_BUDGET_DIR` env vars
  still work if set for the server.

### Notes
- The "Other tools" grouping shown in Claude Desktop's tool-permission UI is
  determined by the client, not the MCPB manifest, and cannot be renamed here.

## [0.4.0] - 2026-07-01

### Added
- Every tool now accepts an optional `budget` file path. Resolution order per
  call: explicit `budget` arg → the session's active budget → the configured
  default budget. Read tools no longer require a prior `open_budget`.
- Read/write tool categories. Read tools (`quarter_status`, `year_summary`,
  `category_breakdown`, `list_transactions`, `list_credits`, `get_budget_info`,
  `list_budgets`, `get_cli_info`) carry the MCP `readOnlyHint` annotation and
  open the budget read-only, so they work in stateless, read-only sessions such
  as live artifacts.
- `store.OpenReadOnly` opens a budget with `mode=ro&immutable=1` and skips
  migration, guaranteeing zero filesystem side effects for reads.

### Changed
- Renamed `get_active_budget` → `get_budget_info` (it resolves any budget, not
  just a session-active one).
- Server startup is lazy: a configured default budget is opened read-write when
  possible, otherwise remembered and opened read-only on demand — so a read-only
  filesystem no longer breaks startup.

### Fixed
- Read tools failing in isolated/read-only sessions that never called
  `open_budget`.

## [0.3.0] - 2026-07-01

### Added
- `import_file` tool: bulk-import a normalized CSV or JSON file by path. The
  preferred path for large imports — no array parameter, no size limit.
- `rows_json` string parameter on `import_transactions`, a fallback for clients
  that cannot pass array parameters (they stringify them).

### Changed
- Shared CSV/JSON readers moved into the `importer` package so the MCP tools and
  the CLI use one code path.
- Tool descriptions and instructions now explicitly forbid writing to the SQLite
  file directly and point to the supported import paths.

### Fixed
- Bulk import breaking on clients that stringify array parameters, which led
  agents to bypass the server and write raw SQL.

## [0.2.0] - 2026-07-01

### Added
- CLI mode: the same binary runs as a command-line tool when given a subcommand
  (`import`, `quarter-status`, `year-summary`, `category-breakdown`,
  `add-credit`), so scripts can operate on a budget without one MCP call per row.
- `get_cli_info` tool that reports the binary path and CLI usage for scripts.

## [0.1.0] - 2026-07-01

### Added
- Initial release: Go MCP server (stdio) packaged as an `.mcpb` bundle.
- SQLite budget store with idempotent migrations and dedup-on-insert.
- Quarterly domain logic: quarter math, the flat spending curve, and
  spend/credit aggregation.
- Guided transaction import with payment/transfer filtering and category
  normalization (`import_transactions`, `add_transaction`).
- External-fund credit system (`add_credit`, `mark_credit_transferred`,
  `list_credits`).
- Canonical views with render instructions: `quarter_status`, `year_summary`,
  `category_breakdown`.
- Manifest, build script, and test suite.
