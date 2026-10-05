// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package record

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func txSetup(t *testing.T) Driver {
	t.Helper()
	d := newTestDriver(t)
	if err := RegisterPool("txt", d); err != nil {
		t.Fatalf("RegisterPool: %v", err)
	}
	_, err := d.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS accounts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  balance INTEGER NOT NULL
);`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	_, _ = d.Exec(context.Background(), "DELETE FROM accounts;")
	_, err = d.Exec(context.Background(), "INSERT INTO accounts(balance) VALUES (100);")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	return d
}

func TestTransaction_CommitsOnNil(t *testing.T) {
	txSetup(t)
	err := Transaction(context.Background(), "txt", func(tx *Tx) error {
		_, err := tx.Exec(context.Background(), "UPDATE accounts SET balance = balance + 5;")
		return err
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	d := PoolOf(context.Background(), "txt")
	row := d.QueryRow(context.Background(), "SELECT balance FROM accounts;")
	var bal int
	if err := row.Scan(&bal); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if bal != 105 {
		t.Errorf("balance = %d, want 105", bal)
	}
}

func TestTransaction_RollsBackOnError(t *testing.T) {
	txSetup(t)
	myErr := errors.New("boom")
	err := Transaction(context.Background(), "txt", func(tx *Tx) error {
		_, _ = tx.Exec(context.Background(), "UPDATE accounts SET balance = 999;")
		return myErr
	})
	if !errors.Is(err, myErr) {
		t.Fatalf("err = %v, want boom", err)
	}
	d := PoolOf(context.Background(), "txt")
	row := d.QueryRow(context.Background(), "SELECT balance FROM accounts;")
	var bal int
	_ = row.Scan(&bal)
	if bal == 999 {
		t.Error("rollback did not revert the update")
	}
}

func TestTransaction_Savepoint(t *testing.T) {
	txSetup(t)
	err := Transaction(context.Background(), "txt", func(tx *Tx) error {
		// First sub-op succeeds
		if err := tx.Savepoint(context.Background(), "sp1", func(*Tx) error {
			_, err := tx.Exec(context.Background(), "UPDATE accounts SET balance = 1;")
			return err
		}); err != nil {
			return err
		}
		// Second sub-op fails — rolls back to sp2 only
		innerErr := errors.New("inner")
		if err := tx.Savepoint(context.Background(), "sp2", func(*Tx) error {
			_, _ = tx.Exec(context.Background(), "UPDATE accounts SET balance = 2;")
			return innerErr
		}); !errors.Is(err, innerErr) {
			_ = err
			return innerErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	d := PoolOf(context.Background(), "txt")
	row := d.QueryRow(context.Background(), "SELECT balance FROM accounts;")
	var bal int
	_ = row.Scan(&bal)
	// sp1 succeeded (balance=1), sp2 failed (reverted to 2->1), commit keeps balance=1
	if bal != 1 {
		t.Errorf("balance = %d, want 1 (sp1 persisted, sp2 rolled back)", bal)
	}
}

func TestTransaction_MissingPool(t *testing.T) {
	err := Transaction(context.Background(), "nonexistent-pool", func(*Tx) error { return nil })
	if err == nil {
		t.Fatal("expected error on missing pool")
	}
}

func TestTransaction_ConcurrentNotDeadlocked(t *testing.T) {
	txSetup(t)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Transaction(context.Background(), "txt", func(tx *Tx) error {
				_, err := tx.Exec(context.Background(), "UPDATE accounts SET balance = balance + 1;")
				return err
			})
		}()
	}
	wg.Wait()
	// no deadlock/panic means pass
}

func TestIsSerialization_FalsePositive(t *testing.T) {
	// pure sanity: random error is not classified as serialization
	if isSerialization(errors.New("totally unrelated")) {
		t.Error("false positive on unrelated error")
	}
	if !isSerialization(errors.New("could not serialize access")) {
		t.Error("missed serialization failure")
	}
}

func TestTransaction_RetryOnSerialization(t *testing.T) {
	txSetup(t)
	// Simulate a serialization failure once, then succeed.
	var calls int
	err := Transaction(context.Background(), "txt", func(tx *Tx) error {
		calls++
		if calls == 1 {
			return errors.New("could not serialize access")
		}
		_, err := tx.Exec(context.Background(), "UPDATE accounts SET balance = 42;")
		return err
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", calls)
	}
	// balance should be 42
	d := PoolOf(context.Background(), "txt")
	row := d.QueryRow(context.Background(), "SELECT balance FROM accounts;")
	var bal int
	_ = row.Scan(&bal)
	if bal != 42 {
		t.Errorf("balance = %d, want 42", bal)
	}
}

func TestSetTenantSessionVar_SqliteNoop(t *testing.T) {
	txSetup(t)
	err := Transaction(context.Background(), "txt", func(tx *Tx) error {
		return SetTenantSessionVar(context.Background(), tx, "tenant-1")
	})
	if err != nil {
		t.Errorf("SetTenantSessionVar on sqlite should be no-op, got %v", err)
	}
}

func TestRedactSQL_HidesLiterals(t *testing.T) {
	in := "SELECT * FROM users WHERE email = 'secret@example.com' AND role = 'admin';"
	out := redactSQL(in)
	if strings.Contains(out, "secret@example.com") {
		t.Errorf("redact leaked literal: %s", out)
	}
	if !strings.Contains(out, "SELECT") || !strings.Contains(out, "WHERE") {
		t.Errorf("redact destroyed SQL shape: %s", out)
	}
}
