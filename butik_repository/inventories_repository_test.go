package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A syntactically valid UUID that gen_random_uuid() will never produce
// collisions with in a freshly truncated table.
const (
	missingInventoryID = "00000000-0000-0000-0000-0000000000ff"
	newInventoryID     = "44444444-4444-4444-4444-444444444444"
)

// newInventoriesTestPool connects to the database pointed at by DATABASE_URL
// and makes sure the tables inventories depends on (products, product_variants,
// inventory_locations) plus inventories itself exist and are empty. Tests that
// need a database are skipped when DATABASE_URL is not set.
func newInventoriesTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := newTestPool(t)

	schema := []string{
		`CREATE TABLE IF NOT EXISTS butiks_engine.product_variants (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			product_id UUID REFERENCES butiks_engine.products (id) ON DELETE CASCADE,
			sku TEXT NOT NULL,
			price NUMERIC(10, 2) NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS butiks_engine.inventory_locations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			code TEXT NOT NULL UNIQUE,
			address TEXT,
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS butiks_engine.inventories (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			product_variant_id UUID REFERENCES butiks_engine.product_variants (id) ON DELETE CASCADE,
			location_id UUID REFERENCES butiks_engine.inventory_locations (id) ON DELETE CASCADE,
			product_lot_id UUID,
			quantity INT NOT NULL,
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
		stmt := `TRUNCATE
			butiks_engine.inventories,
			butiks_engine.inventory_locations,
			butiks_engine.product_variants
			RESTART IDENTITY CASCADE`
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("truncating inventories schema: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return ctx, pool
}

// seedInventory inserts an inventory row with an explicit created_at so tests
// can assert on ordering, and returns its generated id.
func seedInventory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, variantID, locationID string, quantity int, createdAt time.Time) string {
	t.Helper()

	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO butiks_engine.inventories (product_variant_id, location_id, quantity, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $4)
		 RETURNING id`,
		variantID, locationID, quantity, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding inventory: %v", err)
	}
	return id
}

func TestInventoriesRepository(t *testing.T) {
	t.Run("resolveInventoriesPagination", func(t *testing.T) {
		ptr := func(i int) *int { return &i }

		cases := []struct {
			name       string
			params     *GetInventoriesParams
			wantLimit  int
			wantOffset int
		}{
			{"nil params -> defaults", nil, defaultInventoriesLimit, 0},
			{"empty params -> defaults", &GetInventoriesParams{}, defaultInventoriesLimit, 0},
			{"explicit limit and offset", &GetInventoriesParams{Limit: ptr(10), Offset: ptr(20)}, 10, 20},
			{"limit above max is capped", &GetInventoriesParams{Limit: ptr(maxInventoriesLimit + 1)}, maxInventoriesLimit, 0},
			{"zero limit -> default", &GetInventoriesParams{Limit: ptr(0)}, defaultInventoriesLimit, 0},
			{"negative limit -> default", &GetInventoriesParams{Limit: ptr(-5)}, defaultInventoriesLimit, 0},
			{"negative offset -> zero", &GetInventoriesParams{Offset: ptr(-5)}, defaultInventoriesLimit, 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotLimit, gotOffset := resolveInventoriesPagination(tc.params)
				if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
					t.Fatalf("resolveInventoriesPagination(%+v) = (%d, %d), want (%d, %d)",
						tc.params, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
				}
			})
		}
	})

	t.Run("SaveInventory inserts then updates in place", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", time.Now().UTC())
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, time.Now().UTC())
		locationID := seedInventoryLocation(t, ctx, pool, "Main", "main", "addr", true, time.Now().UTC())

		created := time.Now().UTC().Truncate(time.Second)
		inv := &butik_domain.Inventory{
			ID:               newInventoryID,
			ProductVariantID: variantID,
			LocationID:       locationID,
			Quantity:         5,
			CreatedAt:        created,
			UpdatedAt:        created,
		}
		if err := repo.SaveInventory(inv); err != nil {
			t.Fatalf("SaveInventory insert: %v", err)
		}

		got, err := repo.GetInventoryByID(newInventoryID)
		if err != nil {
			t.Fatalf("GetInventoryByID after insert: %v", err)
		}
		if got.Quantity != 5 || got.ProductVariantID != variantID || got.LocationID != locationID {
			t.Fatalf("SaveInventory stored the wrong values: %+v", got)
		}
		if got.ProductLotID != nil {
			t.Fatalf("ProductLotID = %v, want nil", got.ProductLotID)
		}

		inv.Quantity = 10
		inv.UpdatedAt = created.Add(time.Hour)
		if err := repo.SaveInventory(inv); err != nil {
			t.Fatalf("SaveInventory update: %v", err)
		}

		got, err = repo.GetInventoryByID(newInventoryID)
		if err != nil {
			t.Fatalf("GetInventoryByID after update: %v", err)
		}
		if got.Quantity != 10 {
			t.Fatalf("SaveInventory did not update quantity: %+v", got)
		}
	})

	t.Run("GetInventoryByID returns pgx.ErrNoRows when the id is unknown", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		got, err := repo.GetInventoryByID(missingInventoryID)
		if err == nil {
			t.Fatalf("GetInventoryByID(unknown) = %+v, want an error", got)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetInventoryByID(unknown) error = %v, want pgx.ErrNoRows", err)
		}
	})

	t.Run("DeleteInventory removes only the target row", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", time.Now().UTC())
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, time.Now().UTC())
		locationID := seedInventoryLocation(t, ctx, pool, "Main", "main", "addr", true, time.Now().UTC())

		now := time.Now().UTC()
		keepID := seedInventory(t, ctx, pool, variantID, locationID, 1, now)
		dropID := seedInventory(t, ctx, pool, variantID, locationID, 2, now)

		if err := repo.DeleteInventory(dropID); err != nil {
			t.Fatalf("DeleteInventory: %v", err)
		}

		if _, err := repo.GetInventoryByID(dropID); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("after delete, GetInventoryByID(dropID) error = %v, want pgx.ErrNoRows", err)
		}
		if _, err := repo.GetInventoryByID(keepID); err != nil {
			t.Fatalf("DeleteInventory removed the wrong row: %v", err)
		}
	})

	t.Run("GetInventories without details returns nil product and variant", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", time.Now().UTC())
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, time.Now().UTC())
		locationID := seedInventoryLocation(t, ctx, pool, "Main", "main", "addr", true, time.Now().UTC())
		seedInventory(t, ctx, pool, variantID, locationID, 3, time.Now().UTC())

		inventories, err := repo.GetInventories(nil)
		if err != nil {
			t.Fatalf("GetInventories: %v", err)
		}
		if len(inventories) != 1 {
			t.Fatalf("GetInventories returned %d rows, want 1", len(inventories))
		}
		if inventories[0].Product != nil || inventories[0].ProductVariant != nil {
			t.Fatalf("GetInventories without details populated Product/ProductVariant: %+v", inventories[0])
		}
	})

	t.Run("GetInventories with details joins product and variant", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", time.Now().UTC())
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, time.Now().UTC())
		locationID := seedInventoryLocation(t, ctx, pool, "Main", "main", "addr", true, time.Now().UTC())
		invID := seedInventory(t, ctx, pool, variantID, locationID, 3, time.Now().UTC())

		includeDetails := true
		inventories, err := repo.GetInventories(&GetInventoriesParams{IncludeProductAndVariant: &includeDetails})
		if err != nil {
			t.Fatalf("GetInventories with details: %v", err)
		}
		if len(inventories) != 1 {
			t.Fatalf("GetInventories returned %d rows, want 1", len(inventories))
		}

		got := inventories[0]
		if got.ID != invID {
			t.Fatalf("GetInventories returned id %s, want %s", got.ID, invID)
		}
		if got.Product == nil || got.Product.ID != productID || got.Product.Name != "Widget" {
			t.Fatalf("GetInventories did not join Product correctly: %+v", got.Product)
		}
		if got.ProductVariant == nil || got.ProductVariant.ID != variantID || got.ProductVariant.SKU != "widget-sku" {
			t.Fatalf("GetInventories did not join ProductVariant correctly: %+v", got.ProductVariant)
		}
	})

	t.Run("GetInventories filters by location and paginates", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", time.Now().UTC())
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, time.Now().UTC())
		locationA := seedInventoryLocation(t, ctx, pool, "A", "a", "addr", true, time.Now().UTC())
		locationB := seedInventoryLocation(t, ctx, pool, "B", "b", "addr", true, time.Now().UTC())

		base := time.Now().UTC().Truncate(time.Second)
		var wantOrder []string
		for i := range 5 {
			id := seedInventory(t, ctx, pool, variantID, locationA, i, base.Add(time.Duration(-i)*time.Minute))
			wantOrder = append(wantOrder, id)
		}
		seedInventory(t, ctx, pool, variantID, locationB, 99, base)

		limit := 2
		offset := 1
		inventories, err := repo.GetInventories(&GetInventoriesParams{LocationID: locationA, Limit: &limit, Offset: &offset})
		if err != nil {
			t.Fatalf("GetInventories filtered: %v", err)
		}
		if len(inventories) != 2 {
			t.Fatalf("GetInventories returned %d rows, want 2", len(inventories))
		}
		if inventories[0].ID != wantOrder[1] || inventories[1].ID != wantOrder[2] {
			t.Fatalf("GetInventories order/pagination mismatch: got [%s %s], want [%s %s]",
				inventories[0].ID, inventories[1].ID, wantOrder[1], wantOrder[2])
		}
		for _, inv := range inventories {
			if inv.LocationID != locationA {
				t.Fatalf("GetInventories leaked a row from another location: %+v", inv)
			}
		}
	})

	t.Run("CountInventories counts all rows and filters by location", func(t *testing.T) {
		ctx, pool := newInventoriesTestPool(t)
		repo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		productID := seedProduct(t, ctx, pool, "Widget", "widget", "a widget", time.Now().UTC())
		variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku", 9.99, time.Now().UTC())
		locationA := seedInventoryLocation(t, ctx, pool, "A", "a", "addr", true, time.Now().UTC())
		locationB := seedInventoryLocation(t, ctx, pool, "B", "b", "addr", true, time.Now().UTC())

		now := time.Now().UTC()
		for i := range 3 {
			seedInventory(t, ctx, pool, variantID, locationA, i, now.Add(time.Duration(i)*time.Minute))
		}
		seedInventory(t, ctx, pool, variantID, locationB, 1, now)

		total, err := repo.CountInventories(nil)
		if err != nil {
			t.Fatalf("CountInventories(nil): %v", err)
		}
		if total != 4 {
			t.Fatalf("CountInventories(nil) = %d, want 4", total)
		}

		filtered, err := repo.CountInventories(&CountInventoryParams{LocationID: locationA})
		if err != nil {
			t.Fatalf("CountInventories(locationA): %v", err)
		}
		if filtered != 3 {
			t.Fatalf("CountInventories(locationA) = %d, want 3", filtered)
		}
	})
}
