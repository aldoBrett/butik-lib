package butik_repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool connects to the database pointed at by DATABASE_URL and makes sure
// the products table exists and is empty. Tests that need a database are skipped
// when DATABASE_URL is not set (e.g. `go test` outside docker-compose.test.yml).
func newTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pinging test database: %v", err)
	}

	schema := []string{
		`CREATE SCHEMA IF NOT EXISTS butiks_engine`,
		`CREATE TABLE IF NOT EXISTS butiks_engine.products (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			description TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
	}
	for _, stmt := range schema {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("preparing schema: %v", err)
		}
	}

	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE butiks_engine.products RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncating products: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return ctx, pool
}

// seedProduct inserts a product with an explicit created_at so tests can assert
// on ordering, and returns its generated id.
func seedProduct(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, slug, description string, createdAt time.Time) string {
	t.Helper()

	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO butiks_engine.products (name, slug, description, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $4)
		 RETURNING id`,
		name, slug, description, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding product %q: %v", slug, err)
	}
	return id
}

func TestProductsRepository(t *testing.T) {
	t.Run("resolveProductsPagination", func(t *testing.T) {
		ptr := func(i int) *int { return &i }

		cases := []struct {
			name       string
			params     *GetProductsParams
			wantLimit  int
			wantOffset int
		}{
			{"nil params -> defaults", nil, defaultProductsLimit, 0},
			{"empty params -> defaults", &GetProductsParams{}, defaultProductsLimit, 0},
			{"explicit limit and offset", &GetProductsParams{Limit: ptr(10), Offset: ptr(20)}, 10, 20},
			{"limit above max is capped", &GetProductsParams{Limit: ptr(maxProductsLimit + 1)}, maxProductsLimit, 0},
			{"zero limit -> default", &GetProductsParams{Limit: ptr(0)}, defaultProductsLimit, 0},
			{"negative limit -> default", &GetProductsParams{Limit: ptr(-5)}, defaultProductsLimit, 0},
			{"negative offset -> zero", &GetProductsParams{Offset: ptr(-5)}, defaultProductsLimit, 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotLimit, gotOffset := resolveProductsPagination(tc.params)
				if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
					t.Fatalf("resolveProductsPagination(%+v) = (%d, %d), want (%d, %d)",
						tc.params, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
				}
			})
		}
	})

	t.Run("CountProducts", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		count, err := repo.CountProducts()
		if err != nil {
			t.Fatalf("CountProducts on empty table: %v", err)
		}
		if count != 0 {
			t.Fatalf("CountProducts on empty table = %d, want 0", count)
		}

		now := time.Now().UTC()
		for i := range 3 {
			seedProduct(t, ctx, pool,
				fmt.Sprintf("Product %d", i),
				fmt.Sprintf("product-%d", i),
				"a description",
				now.Add(time.Duration(i)*time.Minute),
			)
		}

		count, err = repo.CountProducts()
		if err != nil {
			t.Fatalf("CountProducts after seeding: %v", err)
		}
		if count != 3 {
			t.Fatalf("CountProducts after seeding = %d, want 3", count)
		}
	})

	t.Run("GetProducts empty table returns empty slice", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		products, err := repo.GetProducts(nil)
		if err != nil {
			t.Fatalf("GetProducts on empty table: %v", err)
		}
		if products == nil {
			t.Fatal("GetProducts returned nil slice, want non-nil empty slice")
		}
		if len(products) != 0 {
			t.Fatalf("GetProducts on empty table returned %d products, want 0", len(products))
		}
	})

	t.Run("GetProducts maps columns and orders by created_at desc", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		oldID := seedProduct(t, ctx, pool, "Old", "old", "the old one", base.Add(-2*time.Hour))
		midID := seedProduct(t, ctx, pool, "Mid", "mid", "the middle one", base.Add(-1*time.Hour))
		newID := seedProduct(t, ctx, pool, "New", "new", "the new one", base)

		products, err := repo.GetProducts(nil)
		if err != nil {
			t.Fatalf("GetProducts: %v", err)
		}
		if len(products) != 3 {
			t.Fatalf("GetProducts returned %d products, want 3", len(products))
		}

		wantOrder := []string{newID, midID, oldID}
		for i, want := range wantOrder {
			if products[i].ID != want {
				t.Fatalf("products[%d].ID = %s, want %s (order should be created_at desc)", i, products[i].ID, want)
			}
		}

		first := products[0]
		if first.Name != "New" || first.Slug != "new" || first.Description != "the new one" {
			t.Fatalf("first product mapped incorrectly: %+v", first)
		}
		if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
			t.Fatalf("timestamps not populated: %+v", first)
		}
	})

	t.Run("GetProducts respects limit and offset", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		ids := make([]string, 5)
		for i := range 5 {
			// Newest first index: created later => earlier in result order.
			ids[i] = seedProduct(t, ctx, pool,
				fmt.Sprintf("P%d", i),
				fmt.Sprintf("p-%d", i),
				"desc",
				base.Add(time.Duration(-i)*time.Minute),
			)
		}
		// ids[0] is newest, ids[4] is oldest -> result order is ids[0..4].

		limit := 2
		offset := 1
		products, err := repo.GetProducts(&GetProductsParams{Limit: &limit, Offset: &offset})
		if err != nil {
			t.Fatalf("GetProducts with limit/offset: %v", err)
		}
		if len(products) != 2 {
			t.Fatalf("GetProducts returned %d products, want 2", len(products))
		}
		if products[0].ID != ids[1] || products[1].ID != ids[2] {
			t.Fatalf("GetProducts limit=2 offset=1 = [%s %s], want [%s %s]",
				products[0].ID, products[1].ID, ids[1], ids[2])
		}
	})
}
