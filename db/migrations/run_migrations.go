// Package migrations applies the tickets engine schema's SQL migration files.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

// Run applies every embedded .sql migration file, in filename order, that
// has not already been recorded in public.schema_migrations. Each file is
// applied in its own transaction.
func Run(ctx context.Context, pool *pgxpool.Pool) error {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		applied, err := isApplied(ctx, pool, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		if err := applyMigration(ctx, pool, name); err != nil {
			return err
		}
	}

	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: read dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	return names, nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, name string) error {
	sql, err := files.ReadFile(name)
	if err != nil {
		return fmt.Errorf("migrations: read %s: %w", name, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrations: begin %s: %w", name, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("migrations: exec %s: %w", name, err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO public.schema_migrations (name) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("migrations: record %s: %w", name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrations: commit %s: %w", name, err)
	}

	return nil
}

func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.schema_migrations (
			name TEXT PRIMARY KEY NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("migrations: create schema_migrations: %w", err)
	}
	return nil
}

func isApplied(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM public.schema_migrations WHERE name = $1)`,
		name,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("migrations: check %s: %w", name, err)
	}
	return exists, nil
}
