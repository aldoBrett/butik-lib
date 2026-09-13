package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A couple of syntactically valid UUIDs that gen_random_uuid() will never
// produce collisions with in a freshly truncated table.
const (
	missingProductID    = "00000000-0000-0000-0000-0000000000ff"
	newProductID        = "11111111-1111-1111-1111-111111111111"
	newProductVariantID = "22222222-2222-2222-2222-222222222222"
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

	t.Run("GetProductByID maps columns for an existing product", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		id := seedProduct(t, ctx, pool, "Shirt", "shirt", "a nice shirt", base)

		got, err := repo.GetProductByID(id)
		if err != nil {
			t.Fatalf("GetProductByID: %v", err)
		}
		if got.ID != id || got.Name != "Shirt" || got.Slug != "shirt" || got.Description != "a nice shirt" {
			t.Fatalf("GetProductByID mapped incorrectly: %+v", got)
		}
		if !got.CreatedAt.Equal(base) || !got.UpdatedAt.Equal(base) {
			t.Fatalf("GetProductByID timestamps = (%s, %s), want %s", got.CreatedAt, got.UpdatedAt, base)
		}
	})

	t.Run("GetProductByID returns pgx.ErrNoRows when the id is unknown", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		seedProduct(t, ctx, pool, "Other", "other", "unrelated", time.Now().UTC())

		got, err := repo.GetProductByID(missingProductID)
		if err == nil {
			t.Fatalf("GetProductByID(unknown) = %+v, want an error", got)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetProductByID(unknown) error = %v, want pgx.ErrNoRows", err)
		}
		if got != nil {
			t.Fatalf("GetProductByID(unknown) returned non-nil product %+v", got)
		}
	})

	t.Run("GetProductByInventoryID resolves the product through the variant", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", now)
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, now)
		locationID := seedInventoryLocation(t, ctx, pool, "Main", "main", "addr", true, now)
		inventoryID := seedInventory(t, ctx, pool, variantID, locationID, 5, now)

		got, err := repo.GetProductByInventoryID(inventoryID)
		if err != nil {
			t.Fatalf("GetProductByInventoryID: %v", err)
		}
		if got.ID != productID || got.Name != "Widget" || got.Slug != "widget" {
			t.Fatalf("GetProductByInventoryID mapped incorrectly: %+v", got)
		}
	})

	t.Run("GetProductByInventoryID returns pgx.ErrNoRows when the inventory id is unknown", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		got, err := repo.GetProductByInventoryID(missingInventoryID)
		if err == nil {
			t.Fatalf("GetProductByInventoryID(unknown) = %+v, want an error", got)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetProductByInventoryID(unknown) error = %v, want pgx.ErrNoRows", err)
		}
		if got != nil {
			t.Fatalf("GetProductByInventoryID(unknown) returned non-nil product %+v", got)
		}
	})

	t.Run("DeleteProductByID removes only the target row", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		keepID := seedProduct(t, ctx, pool, "Keep", "keep", "stays", now)
		dropID := seedProduct(t, ctx, pool, "Drop", "drop", "goes", now)

		if err := repo.DeleteProductByID(dropID); err != nil {
			t.Fatalf("DeleteProductByID: %v", err)
		}

		if _, err := repo.GetProductByID(dropID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("after delete, GetProductByID(dropID) error = %v, want pgx.ErrNoRows", err)
		}
		if _, err := repo.GetProductByID(keepID); err != nil {
			t.Fatalf("DeleteProductByID removed the wrong row: %v", err)
		}

		count, err := repo.CountProducts()
		if err != nil {
			t.Fatalf("CountProducts: %v", err)
		}
		if count != 1 {
			t.Fatalf("CountProducts after delete = %d, want 1", count)
		}
	})

	t.Run("DeleteProductByID is a no-op for an unknown id", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		seedProduct(t, ctx, pool, "Keep", "keep", "stays", time.Now().UTC())

		if err := repo.DeleteProductByID(missingProductID); err != nil {
			t.Fatalf("DeleteProductByID(unknown) = %v, want nil", err)
		}

		count, err := repo.CountProducts()
		if err != nil {
			t.Fatalf("CountProducts: %v", err)
		}
		if count != 1 {
			t.Fatalf("CountProducts after no-op delete = %d, want 1", count)
		}
	})

	t.Run("SaveProduct inserts when the id is new", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		created := time.Now().UTC().Truncate(time.Second)
		p := &butik_domain.Product{
			ID:          newProductID,
			Name:        "Hat",
			Slug:        "hat",
			Description: "a hat",
			CreatedAt:   created,
			UpdatedAt:   created,
		}

		if err := repo.SaveProduct(p); err != nil {
			t.Fatalf("SaveProduct insert: %v", err)
		}

		got, err := repo.GetProductByID(newProductID)
		if err != nil {
			t.Fatalf("GetProductByID after insert: %v", err)
		}
		if got.Name != "Hat" || got.Slug != "hat" || got.Description != "a hat" {
			t.Fatalf("SaveProduct stored the wrong values: %+v", got)
		}
		if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(created) {
			t.Fatalf("SaveProduct timestamps = (%s, %s), want %s", got.CreatedAt, got.UpdatedAt, created)
		}
	})

	t.Run("SaveProduct updates in place when the id already exists", func(t *testing.T) {
		ctx, pool := newTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		created := time.Now().UTC().Truncate(time.Second)
		id := seedProduct(t, ctx, pool, "Old name", "old-slug", "old description", created)

		updated := created.Add(time.Hour)
		p := &butik_domain.Product{
			ID:          id,
			Name:        "New name",
			Slug:        "new-slug",
			Description: "new description",
			CreatedAt:   created,
			UpdatedAt:   updated,
		}

		if err := repo.SaveProduct(p); err != nil {
			t.Fatalf("SaveProduct update: %v", err)
		}

		got, err := repo.GetProductByID(id)
		if err != nil {
			t.Fatalf("GetProductByID after update: %v", err)
		}
		if got.Name != "New name" || got.Slug != "new-slug" || got.Description != "new description" {
			t.Fatalf("SaveProduct did not update the values: %+v", got)
		}
		if !got.UpdatedAt.Equal(updated) {
			t.Fatalf("SaveProduct updated_at = %s, want %s", got.UpdatedAt, updated)
		}

		count, err := repo.CountProducts()
		if err != nil {
			t.Fatalf("CountProducts: %v", err)
		}
		if count != 1 {
			t.Fatalf("SaveProduct upsert created a second row: count = %d, want 1", count)
		}
	})

	t.Run("SaveProductWithProductVariant saves both rows", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		created := time.Now().UTC().Truncate(time.Second)
		product := &butik_domain.Product{
			ID:          newProductID,
			Name:        "Hat",
			Slug:        "hat",
			Description: "a hat",
			CreatedAt:   created,
			UpdatedAt:   created,
		}
		variant := &butik_domain.ProductVariant{
			ID:        newProductVariantID,
			ProductID: newProductID,
			SKU:       "HAT-1",
			Price:     9.99,
			CreatedAt: created,
			UpdatedAt: created,
		}

		if err := repo.SaveProductWithProductVariant(product, variant); err != nil {
			t.Fatalf("SaveProductWithProductVariant: %v", err)
		}

		gotProduct, err := repo.GetProductByID(newProductID)
		if err != nil {
			t.Fatalf("GetProductByID after save: %v", err)
		}
		if gotProduct.Name != "Hat" || gotProduct.Slug != "hat" {
			t.Fatalf("SaveProductWithProductVariant stored the wrong product: %+v", gotProduct)
		}

		productID := newProductID
		variantsRepo := NewProductVariantsRepositoryHandler(ctx, pool, nil)
		gotVariants, err := variantsRepo.GetProductVariants(&GetProductVariantsParams{ProductID: &productID})
		if err != nil {
			t.Fatalf("GetProductVariants after save: %v", err)
		}
		if len(gotVariants) != 1 || gotVariants[0].ID != newProductVariantID || gotVariants[0].SKU != "HAT-1" {
			t.Fatalf("SaveProductWithProductVariant stored the wrong variant: %+v", gotVariants)
		}
	})

	t.Run("SaveProductWithProductVariant rolls back the product when the variant fails", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductsRepositoryHandler(ctx, pool, nil)

		created := time.Now().UTC().Truncate(time.Second)
		product := &butik_domain.Product{
			ID:          newProductID,
			Name:        "Hat",
			Slug:        "hat",
			Description: "a hat",
			CreatedAt:   created,
			UpdatedAt:   created,
		}
		variant := &butik_domain.ProductVariant{
			ID:        newProductVariantID,
			ProductID: missingProductID, // does not exist -> violates the FK constraint.
			SKU:       "HAT-1",
			Price:     9.99,
			CreatedAt: created,
			UpdatedAt: created,
		}

		if err := repo.SaveProductWithProductVariant(product, variant); err == nil {
			t.Fatal("SaveProductWithProductVariant = nil, want error for invalid variant")
		}

		if _, err := repo.GetProductByID(newProductID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("after failed save, GetProductByID(newProductID) error = %v, want pgx.ErrNoRows", err)
		}

		count, err := repo.CountProducts()
		if err != nil {
			t.Fatalf("CountProducts: %v", err)
		}
		if count != 0 {
			t.Fatalf("SaveProductWithProductVariant left a product behind after rollback: count = %d, want 0", count)
		}
	})
}
