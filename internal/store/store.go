// Package store handles opening, migrating, and querying a single budget's
// SQLite database. One SQLite file == one budget.
package store

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Default budget configuration values, used when a budget is first created.
const (
	DefaultQuarterlyGoal = 15000.0
	DefaultCurveDays     = 90
	schemaVersion        = "1"
)

// Store wraps a SQLite connection for one budget file.
type Store struct {
	db       *sql.DB
	path     string
	readOnly bool
}

// Meta holds a budget's top-level configuration.
type Meta struct {
	SchemaVersion string  `json:"schema_version"`
	BudgetName    string  `json:"budget_name"`
	QuarterlyGoal float64 `json:"quarterly_goal"`
	CurveDays     int     `json:"curve_days"`
	Path          string  `json:"path"`
}

// Transaction is a single stored spending/inflow record.
type Transaction struct {
	ID          int64   `json:"id"`
	TxnDate     string  `json:"txn_date"`
	PostDate    string  `json:"post_date,omitempty"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Category    string  `json:"category"`
	RawCategory string  `json:"raw_category,omitempty"`
	TxnType     string  `json:"txn_type"`
	Memo        string  `json:"memo,omitempty"`
	Source      string  `json:"source,omitempty"`
}

// Credit is an external-fund entry that offsets net spend on its date.
type Credit struct {
	ID          int64   `json:"id"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Note        string  `json:"note,omitempty"`
	Note2       string  `json:"note2,omitempty"`
	Transferred bool    `json:"transferred"`
	Source      string  `json:"source,omitempty"`
}

// Open opens (creating if requested) a budget file at path and runs migrations.
// The returned Store is read-write.
func Open(path string, create bool) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("budget path is required")
	}
	// modernc.org/sqlite registers under the driver name "sqlite".
	// Default DSN creates the file (rwc); mode=rw opens an existing file only.
	dsn := path
	if !create {
		dsn = path + "?mode=rw"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open budget %q: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open budget %q: %w", path, err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenReadOnly opens an existing budget file for reading only. It uses SQLite's
// read-only mode and does NOT migrate, so it performs no writes to disk — safe
// for stateless, read-only sessions (e.g. sandboxed artifacts). It fails if the
// file does not exist.
func OpenReadOnly(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("budget path is required")
	}
	// mode=ro opens read-only; immutable=1 avoids creating -wal/-shm/journal
	// files and lock contention, guaranteeing no filesystem side effects.
	db, err := sql.Open("sqlite", path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, fmt.Errorf("open budget %q (read-only): %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open budget %q (read-only): %w", path, err)
	}
	return &Store{db: db, path: path, readOnly: true}, nil
}

// ReadOnly reports whether the store was opened read-only.
func (s *Store) ReadOnly() bool { return s.readOnly }

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the budget file path.
func (s *Store) Path() string { return s.path }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			txn_date TEXT NOT NULL,
			post_date TEXT,
			description TEXT NOT NULL,
			amount REAL NOT NULL,
			category TEXT NOT NULL DEFAULT 'Other',
			raw_category TEXT,
			txn_type TEXT NOT NULL DEFAULT 'expense',
			memo TEXT,
			source TEXT,
			dedup_hash TEXT NOT NULL UNIQUE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_txn_date ON transactions(txn_date)`,
		`CREATE INDEX IF NOT EXISTS idx_txn_category ON transactions(category)`,
		`CREATE TABLE IF NOT EXISTS credits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			date TEXT NOT NULL,
			amount REAL NOT NULL,
			note TEXT,
			note2 TEXT,
			transferred INTEGER NOT NULL DEFAULT 0,
			source TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_credit_date ON credits(date)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Seed default meta values only if absent.
	defaults := map[string]string{
		"schema_version": schemaVersion,
		"quarterly_goal": fmt.Sprintf("%g", DefaultQuarterlyGoal),
		"curve_days":     fmt.Sprintf("%d", DefaultCurveDays),
		"budget_name":    "",
	}
	for k, v := range defaults {
		if _, err := s.db.Exec(
			`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, k, v,
		); err != nil {
			return fmt.Errorf("seed meta %q: %w", k, err)
		}
	}
	return nil
}

// DedupHash computes the uniqueness key for a transaction:
// sha1(txn_date | amount | normalized_description).
func DedupHash(txnDate string, amount float64, description string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(description)), " ")
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%.2f|%s", txnDate, amount, norm)))
	return hex.EncodeToString(h[:])
}

// GetMeta reads the budget configuration.
func (s *Store) GetMeta() (Meta, error) {
	rows, err := s.db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		return Meta{}, err
	}
	defer rows.Close()
	m := Meta{Path: s.path}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Meta{}, err
		}
		switch k {
		case "schema_version":
			m.SchemaVersion = v
		case "budget_name":
			m.BudgetName = v
		case "quarterly_goal":
			fmt.Sscanf(v, "%g", &m.QuarterlyGoal)
		case "curve_days":
			fmt.Sscanf(v, "%d", &m.CurveDays)
		}
	}
	return m, rows.Err()
}

// SetMetaValue upserts a single meta key.
func (s *Store) SetMetaValue(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// InsertTransaction inserts one transaction, skipping if its dedup hash already
// exists. Returns true if a new row was inserted, false if it was a duplicate.
func (s *Store) InsertTransaction(t Transaction) (bool, error) {
	hash := DedupHash(t.TxnDate, t.Amount, t.Description)
	res, err := s.db.Exec(
		`INSERT INTO transactions
		 (txn_date, post_date, description, amount, category, raw_category, txn_type, memo, source, dedup_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(dedup_hash) DO NOTHING`,
		t.TxnDate, nullStr(t.PostDate), t.Description, t.Amount, t.Category,
		nullStr(t.RawCategory), t.TxnType, nullStr(t.Memo), nullStr(t.Source), hash,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// InsertCredit inserts one external-fund credit and returns its new id.
func (s *Store) InsertCredit(c Credit) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO credits (date, amount, note, note2, transferred, source)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.Date, c.Amount, nullStr(c.Note), nullStr(c.Note2), boolInt(c.Transferred), nullStr(c.Source),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// MarkCreditTransferred sets the transferred flag on a credit.
func (s *Store) MarkCreditTransferred(id int64, transferred bool) error {
	res, err := s.db.Exec(`UPDATE credits SET transferred=? WHERE id=?`, boolInt(transferred), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no credit with id %d", id)
	}
	return nil
}

// TransactionsBetween returns transactions with txn_date in [start, end]
// (inclusive), ordered by date then id.
func (s *Store) TransactionsBetween(start, end string) ([]Transaction, error) {
	rows, err := s.db.Query(
		`SELECT id, txn_date, COALESCE(post_date,''), description, amount, category,
		        COALESCE(raw_category,''), txn_type, COALESCE(memo,''), COALESCE(source,'')
		 FROM transactions WHERE txn_date >= ? AND txn_date <= ?
		 ORDER BY txn_date, id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTransactions(rows)
}

// CreditsBetween returns credits with date in [start, end], ordered by date.
func (s *Store) CreditsBetween(start, end string) ([]Credit, error) {
	rows, err := s.db.Query(
		`SELECT id, date, amount, COALESCE(note,''), COALESCE(note2,''), transferred, COALESCE(source,'')
		 FROM credits WHERE date >= ? AND date <= ? ORDER BY date, id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCredits(rows)
}

// AllCredits returns every credit ordered by date.
func (s *Store) AllCredits() ([]Credit, error) {
	rows, err := s.db.Query(
		`SELECT id, date, amount, COALESCE(note,''), COALESCE(note2,''), transferred, COALESCE(source,'')
		 FROM credits ORDER BY date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCredits(rows)
}

// DateBounds returns the earliest and latest transaction dates (empty if none).
func (s *Store) DateBounds() (min, max string, err error) {
	row := s.db.QueryRow(`SELECT COALESCE(MIN(txn_date),''), COALESCE(MAX(txn_date),'') FROM transactions`)
	err = row.Scan(&min, &max)
	return
}

func scanTransactions(rows *sql.Rows) ([]Transaction, error) {
	var out []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.TxnDate, &t.PostDate, &t.Description, &t.Amount,
			&t.Category, &t.RawCategory, &t.TxnType, &t.Memo, &t.Source); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanCredits(rows *sql.Rows) ([]Credit, error) {
	var out []Credit
	for rows.Next() {
		var c Credit
		var transferred int
		if err := rows.Scan(&c.ID, &c.Date, &c.Amount, &c.Note, &c.Note2, &transferred, &c.Source); err != nil {
			return nil, err
		}
		c.Transferred = transferred != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
