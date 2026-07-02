# Import Guide — normalizing arbitrary transaction tables

The `import_transactions` tool expects **pre-normalized** rows. You (the agent)
read the source spreadsheet/CSV, map it onto the canonical shape below, and call
the tool. The tool then dedups, filters, and stores — but it is only a safety
net; do the normalization deliberately.

## Canonical row shape

```json
{
  "txn_date": "2026-04-01",        // REQUIRED, ISO YYYY-MM-DD (the transaction/purchase date)
  "post_date": "2026-04-03",       // optional
  "description": "KROGER",         // REQUIRED
  "amount": -290.30,               // REQUIRED, SIGNED: negative = money out, positive = money in
  "category": "Groceries",         // optional; normalized to the canonical set
  "txn_type": "expense",           // expense | refund | payment | transfer | other
  "memo": "..."                    // optional
}
```

## Step-by-step

1. **Identify columns.** Sources vary. Common headers:
   - Credit-card export: `Transaction Date, Post Date, Description, Category, Type, Amount, Memo`.
   - Bank export: `Date, Description, Category, Amount` (often with types like
     `ACH_CREDIT`, `MISC_DEBIT`, `QUICKPAY_DEBIT`, `ATM`, `LOAN_PMT`).
2. **Fix the amount sign.** Money leaving the account must be **negative**. Some
   exports use a positive "debit" column and a separate "credit" column — combine
   them into one signed `amount`.
3. **Assign `txn_type`:**
   - `expense` — a normal purchase (money out). Includes `Sale`, `DEBIT`, `ATM`, `FEE`.
   - `refund` — a return/refund/reimbursement (money in that reduces spend).
   - `payment` — a **credit-card payment** ("AUTOMATIC PAYMENT", "PAYMENT - THANK YOU"). **Not spending.**
   - `transfer` — an account transfer or deposit / paycheck (`ACH_CREDIT`, "to/from savings"). **Not spending.**
   - `other` — anything genuinely uncategorizable.
   If you leave `txn_type` blank the tool infers it from the amount sign and
   description keywords, but set it explicitly when you can.
4. **Drop non-spending rows.** Credit-card payments and account transfers are
   *not* spending — they only move money between accounts. Mark them `payment`
   or `transfer`; the tool filters them out and reports the count.
5. **Watch for column-shifted / malformed rows.** Real exports contain rows
   where the category landed in the type column, or an amount landed in the memo.
   Repair them before import, or drop them and note it to the user.
6. **De-duplicate across sources.** The same purchase may appear in both a
   credit-card export and a bank export. The tool dedups on
   `sha1(txn_date | amount | normalized description)`, so re-importing or
   overlapping files is safe — but if the two sources describe the same purchase
   differently, prefer one source per account to avoid double counting.

## Canonical categories

Categories are for **analysis only** — budgeting is a single quarterly total.
Unknown categories become `Other`. Canonical set:

`Groceries, Food & Drink, Gas, Shopping, Bills & Utilities, Home, Automotive,
Travel, Entertainment, Health, Insurance, Education, Personal, Subscriptions,
Gifts & Donations, Fees, Other`

## Bulk import (many rows)

Passing thousands of rows to `import_transactions` is wasteful, and some clients
can't pass array parameters at all (they stringify them). **Never respond to that
by writing to the SQLite file directly** — it bypasses validation and the dedup
invariants. Use one of these supported bulk paths instead:

1. **`import_file` tool (preferred).** Write the normalized rows to a CSV or JSON
   file (you have filesystem tools), then call `import_file` with the file path.
   No array parameter, no size limit.
2. **`rows_json` string.** If you have inline rows but the client mangles arrays,
   pass them to `import_transactions` as a JSON string in `rows_json`.
3. **CLI.** Shell out to the bundled binary — see below.

### CLI mode

Call `get_cli_info` to get the binary's absolute path, then shell out from a
script. It applies the identical dedup / filtering / normalization logic.

```python
import subprocess
BIN = "..."  # from the get_cli_info tool's binary_path
# import a normalized CSV (columns: txn_date,post_date,description,amount,category,txn_type,memo)
subprocess.run([BIN, "import", "--budget", "/path/budget.db",
                "--csv", "normalized.csv", "--source", "chase_2026q2"], check=True)
# or stream canonical JSON rows on stdin
subprocess.run([BIN, "import", "--budget", "/path/budget.db", "--json", "-"],
               input=json_rows, text=True, check=True)
```

The CSV needs `txn_date, description, amount` at minimum; amounts may include
`$`, thousands separators, or `(parentheses)` for negatives. Query subcommands
(`quarter-status`, `year-summary`, `category-breakdown`) print JSON to stdout so
a script can build its own reports. Pass `--create` to import into a new file.

## After import

Always relay the returned report to the user: `inserted`, `skipped_duplicates`,
`filtered_out`, and any `warnings`.
