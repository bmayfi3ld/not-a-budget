package store

import (
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"), true)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpdateAndDeleteTransaction(t *testing.T) {
	s := newStore(t)
	if _, err := s.InsertTransaction(Transaction{
		TxnDate: "2026-04-01", Description: "Coffee", Amount: -5, Category: "Dining", TxnType: "expense",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	txns, _ := s.TransactionsBetween("2026-01-01", "2026-12-31")
	id := txns[0].ID

	// Partial update via full-record write (mirrors the MCP handler).
	got, err := s.GetTransaction(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.Amount = -7
	got.Description = "Latte"
	if err := s.UpdateTransaction(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, _ := s.GetTransaction(id)
	if after.Amount != -7 || after.Description != "Latte" {
		t.Fatalf("update not applied: %+v", after)
	}

	// Re-inserting the original values must not collide (hash was recomputed).
	if _, err := s.InsertTransaction(Transaction{
		TxnDate: "2026-04-01", Description: "Coffee", Amount: -5, TxnType: "expense",
	}); err != nil {
		t.Fatalf("reinsert original: %v", err)
	}

	// Editing to collide with another row is rejected.
	if err := s.UpdateTransaction(Transaction{
		ID: id, TxnDate: "2026-04-01", Description: "Coffee", Amount: -5, TxnType: "expense",
	}); err == nil {
		t.Fatalf("expected dedup collision error")
	}

	if err := s.DeleteTransaction(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetTransaction(id); err == nil {
		t.Fatalf("expected error after delete")
	}
	if err := s.DeleteTransaction(id); err == nil {
		t.Fatalf("expected error deleting missing id")
	}
}

func TestUpdateAndDeleteCredit(t *testing.T) {
	s := newStore(t)
	id, err := s.InsertCredit(Credit{Date: "2026-04-01", Amount: 100, Note: "car"})
	if err != nil {
		t.Fatalf("insert credit: %v", err)
	}
	c, _ := s.GetCredit(id)
	c.Amount = 250
	c.Transferred = true
	if err := s.UpdateCredit(c); err != nil {
		t.Fatalf("update credit: %v", err)
	}
	after, _ := s.GetCredit(id)
	if after.Amount != 250 || !after.Transferred {
		t.Fatalf("credit update not applied: %+v", after)
	}
	if err := s.DeleteCredit(id); err != nil {
		t.Fatalf("delete credit: %v", err)
	}
	if err := s.DeleteCredit(id); err == nil {
		t.Fatalf("expected error deleting missing credit")
	}
}
