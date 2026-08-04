package postgres

import (
	"context"
	"testing"
)

func TestStoreQuerierBeforeOpen(t *testing.T) {
	// Providers wire the unit of work before the pool exists; until Open runs,
	// every querier entry point must fail with an error, never panic.
	s := New("postgres://unused")
	ctx := context.Background()

	if _, err := s.Begin(ctx); err == nil {
		t.Error("Begin must fail before Open")
	}

	if _, err := s.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("Exec must fail before Open")
	}

	rows, err := s.Query(ctx, "SELECT 1")
	if err == nil {
		rows.Close()
		t.Error("Query must fail before Open")
	}

	if err := s.QueryRow(ctx, "SELECT 1").Scan(); err == nil {
		t.Error("QueryRow.Scan must surface the error")
	}
}
