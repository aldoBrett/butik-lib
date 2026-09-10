// Command migrate applies the butik engine SQL migrations against the
// database named by the DATABASE_URL environment variable.
//
// Usage:
//
//	DATABASE_URL=postgres://user:pass@host:5432/dbname go run ./cmd/migrate
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	migrations "butik-lib/db/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	log.Println("migrate: applying migrations")
	if err := migrations.Run(ctx, pool); err != nil {
		return err
	}
	log.Println("migrate: done")

	return nil
}

func connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}
