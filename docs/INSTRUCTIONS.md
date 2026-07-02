# NAB (Not A Budget) — agent instructions

This bundle manages a **quarterly cash-flow budget** stored in a SQLite file.
It is not a traditional budgeting app. The two core mechanics:

## 1. The flat quarterly spending curve

Each quarter has a single spending goal (default **$15,000**, configurable via
`set_config`). The goal is spread as a **flat straight line** from $0 on day 1 to
the full goal on day `curve_days` (default 90):

```
goal(day) = quarterly_goal * day / curve_days
```

`quarter_status` returns cumulative **net** spend per day alongside that flat
line. The headline question — *"how much have I spent this quarter vs budget?"* —
is answered by `quarter_status`. Its result includes `render_instructions`;
follow them to draw the canonical "Spend vs Budget" chart.

## 2. External-fund credits

Large one-off purchases (car work, a vacation, a pool) are funded by transferring
money from **savings / external accounts** into the general account so they don't
consume the quarterly budget. Record these with `add_credit` (positive amount).
Credits **subtract from net spend** on their date:

```
net_total = gross_spend − credits
```

Track pending transfers with `transferred=false` and `mark_credit_transferred`.

## What counts as spending

`gross_spend` = the sum of expense outflows (negative amounts), with refunds
reducing it. **Credit-card payments and account transfers are excluded** — they
just move money between accounts. See `IMPORT_GUIDE.md`.

## Budgets are files; tools are read or write

A budget is one SQLite file. **Every tool takes a `budget` path** — the SQLite
file to operate on. (If a default budget is configured via `MCPB_BUDGET_DEFAULT`,
the `budget` argument may be omitted.)

- **Read tools** (`quarter_status`, `year_summary`, `category_breakdown`,
  `list_transactions`, `list_credits`, `get_budget_info`, `list_budgets`) are
  read-only and open the file read-only, so they run in stateless, read-only
  sessions such as live artifacts.
- **Write tools** (`import_file`, `import_transactions`, `add_transaction`,
  `add_credit`, `set_config`, `mark_credit_transferred`) modify a budget and
  **create the file if it does not yet exist**.

## Typical workflow

1. Get the budget file path from the user (a budget is one SQLite file) and pass
   it as `budget` on each call. To start a new budget, just import into a new
   path — write tools create it.
2. Import history: read a CSV/spreadsheet, normalize it (see `IMPORT_GUIDE.md`),
   then `import_file(path)` (preferred) or `import_transactions(rows)`.
3. Record big funded purchases with `add_credit`.
4. Review with `quarter_status`, `year_summary`, `category_breakdown` — and
   always follow each result's `render_instructions`.

> The render/normalization instructions are repeated inside every tool's
> description on purpose. Follow the instructions attached to each tool result.
