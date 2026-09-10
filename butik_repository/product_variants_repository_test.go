package butik_repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newProductVariantsTestPool builds on newTestPool, adding an empty
// product_variants table (which has a foreign key to products). Tests that need
// a database are skipped when DATABASE_URL is not set.
func newProductVariantsTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := newTestPool(t)

	stmt := `CREATE TABLE IF NOT EXISTS butiks_engine.product_variants (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		product_id UUID REFERENCES butiks_engine.products (id) ON DELETE CASCADE,
		sku TEXT NOT NULL,
		price NUMERIC(10, 2) NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("preparing product_variants schema: %v", err)
	}

	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE butiks_engine.product_variants RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncating product_variants: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return ctx, pool
}

// seedProductVariant inserts a variant with an explicit created_at so tests can
// assert on ordering, and returns its generated id.
func seedProductVariant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, productID, sku string, price float64, createdAt time.Time) string {
	t.Helper()

	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO butiks_engine.product_variants (product_id, sku, price, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $4)
		 RETURNING id`,
		productID, sku, price, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding product variant %q: %v", sku, err)
	}
	return id
}

func TestProductVariantsRepository(t *testing.T) {
	t.Run("resolveProductVariantsPagination", func(t *testing.T) {
		ptr := func(i int) *int { return &i }

		cases := []struct {
			name       string
			params     *GetProductVariantsParams
			wantLimit  int
			wantOffset int
		}{
			{"nil params -> defaults", nil, defaultProductVariantsLimit, 0},
			{"empty params -> defaults", &GetProductVariantsParams{}, defaultProductVariantsLimit, 0},
			{"explicit limit and offset", &GetProductVariantsParams{Limit: ptr(10), Offset: ptr(20)}, 10, 20},
			{"limit above max is capped", &GetProductVariantsParams{Limit: ptr(maxProductVariantsLimit + 1)}, maxProductVariantsLimit, 0},
			{"zero limit -> default", &GetProductVariantsParams{Limit: ptr(0)}, defaultProductVariantsLimit, 0},
			{"negative limit -> default", &GetProductVariantsParams{Limit: ptr(-5)}, defaultProductVariantsLimit, 0},
			{"negative offset -> zero", &GetProductVariantsParams{Offset: ptr(-5)}, defaultProductVariantsLimit, 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotLimit, gotOffset := resolveProductVariantsPagination(tc.params)
				if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
					t.Fatalf("resolveProductVariantsPagination(%+v) = (%d, %d), want (%d, %d)",
						tc.params, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
				}
			})
		}
	})

	t.Run("productVariantsProductFilter", func(t *testing.T) {
		id := "abc"
		if got := productVariantsProductFilter(nil); got != "" {
			t.Fatalf("nil params = %q, want empty", got)
		}
		if got := productVariantsProductFilter(&GetProductVariantsParams{}); got != "" {
			t.Fatalf("nil ProductID = %q, want empty", got)
		}
		if got := productVariantsProductFilter(&GetProductVariantsParams{ProductID: &id}); got != id {
			t.Fatalf("set ProductID = %q, want %q", got, id)
		}
	})

	t.Run("CountProductVariants", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductVariantsRepositoryHandler(ctx, pool, nil)

		count, err := repo.CountProductVariants(nil)
		if err != nil {
			t.Fatalf("CountProductVariants on empty table: %v", err)
		}
		if count != 0 {
			t.Fatalf("CountProductVariants on empty table = %d, want 0", count)
		}

		now := time.Now().UTC()
		productA := seedProduct(t, ctx, pool, "Product A", "product-a", "a", now)
		productB := seedProduct(t, ctx, pool, "Product B", "product-b", "b", now)

		seedProductVariant(t, ctx, pool, productA, "A-1", 10.00, now)
		seedProductVariant(t, ctx, pool, productA, "A-2", 20.00, now.Add(time.Minute))
		seedProductVariant(t, ctx, pool, productB, "B-1", 30.00, now.Add(2*time.Minute))

		count, err = repo.CountProductVariants(nil)
		if err != nil {
			t.Fatalf("CountProductVariants after seeding: %v", err)
		}
		if count != 3 {
			t.Fatalf("CountProductVariants after seeding = %d, want 3", count)
		}

		count, err = repo.CountProductVariants(&GetProductVariantsParams{ProductID: &productA})
		if err != nil {
			t.Fatalf("CountProductVariants filtered: %v", err)
		}
		if count != 2 {
			t.Fatalf("CountProductVariants filtered by productA = %d, want 2", count)
		}
	})

	t.Run("GetProductVariants empty table returns empty slice", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductVariantsRepositoryHandler(ctx, pool, nil)

		variants, err := repo.GetProductVariants(nil)
		if err != nil {
			t.Fatalf("GetProductVariants on empty table: %v", err)
		}
		if variants == nil {
			t.Fatal("GetProductVariants returned nil slice, want non-nil empty slice")
		}
		if len(variants) != 0 {
			t.Fatalf("GetProductVariants on empty table returned %d variants, want 0", len(variants))
		}
	})

	t.Run("GetProductVariants maps columns and orders by created_at desc", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductVariantsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		productID := seedProduct(t, ctx, pool, "Shirt", "shirt", "a shirt", base)

		oldID := seedProductVariant(t, ctx, pool, productID, "SHIRT-S", 19.99, base.Add(-2*time.Hour))
		midID := seedProductVariant(t, ctx, pool, productID, "SHIRT-M", 24.99, base.Add(-1*time.Hour))
		newID := seedProductVariant(t, ctx, pool, productID, "SHIRT-L", 29.99, base)

		variants, err := repo.GetProductVariants(nil)
		if err != nil {
			t.Fatalf("GetProductVariants: %v", err)
		}
		if len(variants) != 3 {
			t.Fatalf("GetProductVariants returned %d variants, want 3", len(variants))
		}

		wantOrder := []string{newID, midID, oldID}
		for i, want := range wantOrder {
			if variants[i].ID != want {
				t.Fatalf("variants[%d].ID = %s, want %s (order should be created_at desc)", i, variants[i].ID, want)
			}
		}

		first := variants[0]
		if first.ProductID != productID || first.SKU != "SHIRT-L" || first.Price != 29.99 {
			t.Fatalf("first variant mapped incorrectly: %+v", first)
		}
		if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
			t.Fatalf("timestamps not populated: %+v", first)
		}
	})

	t.Run("GetProductVariants filters by product id", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductVariantsRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		productA := seedProduct(t, ctx, pool, "Product A", "product-a", "a", now)
		productB := seedProduct(t, ctx, pool, "Product B", "product-b", "b", now)

		a1 := seedProductVariant(t, ctx, pool, productA, "A-1", 10.00, now)
		a2 := seedProductVariant(t, ctx, pool, productA, "A-2", 20.00, now.Add(time.Minute))
		seedProductVariant(t, ctx, pool, productB, "B-1", 30.00, now.Add(2*time.Minute))

		variants, err := repo.GetProductVariants(&GetProductVariantsParams{ProductID: &productA})
		if err != nil {
			t.Fatalf("GetProductVariants filtered: %v", err)
		}
		if len(variants) != 2 {
			t.Fatalf("GetProductVariants filtered returned %d variants, want 2", len(variants))
		}

		got := map[string]bool{variants[0].ID: true, variants[1].ID: true}
		if !got[a1] || !got[a2] {
			t.Fatalf("GetProductVariants filtered = %v, want variants %s and %s", got, a1, a2)
		}
		for _, v := range variants {
			if v.ProductID != productA {
				t.Fatalf("variant %s has product_id %s, want %s", v.ID, v.ProductID, productA)
			}
		}
	})

	t.Run("GetProductVariants respects limit and offset", func(t *testing.T) {
		ctx, pool := newProductVariantsTestPool(t)
		repo := NewProductVariantsRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		productID := seedProduct(t, ctx, pool, "Product", "product", "p", base)

		ids := make([]string, 5)
		for i := range 5 {
			// created later => earlier in result order.
			ids[i] = seedProductVariant(t, ctx, pool, productID,
				fmt.Sprintf("SKU-%d", i),
				float64(i),
				base.Add(time.Duration(-i)*time.Minute),
			)
		}
		// ids[0] is newest, ids[4] is oldest -> result order is ids[0..4].

		limit := 2
		offset := 1
		variants, err := repo.GetProductVariants(&GetProductVariantsParams{Limit: &limit, Offset: &offset})
		if err != nil {
			t.Fatalf("GetProductVariants with limit/offset: %v", err)
		}
		if len(variants) != 2 {
			t.Fatalf("GetProductVariants returned %d variants, want 2", len(variants))
		}
		if variants[0].ID != ids[1] || variants[1].ID != ids[2] {
			t.Fatalf("GetProductVariants limit=2 offset=1 = [%s %s], want [%s %s]",
				variants[0].ID, variants[1].ID, ids[1], ids[2])
		}
	})
}
