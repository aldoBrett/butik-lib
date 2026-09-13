package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A syntactically valid UUID that gen_random_uuid() will never produce
// collisions with in a freshly truncated table.
const newInventoryMovementID = "55555555-5555-5555-5555-555555555555"

// newInventoryMovementsTestPool connects to the database pointed at by
// DATABASE_URL and makes sure the tables inventory_movements depends on
// (products, product_variants, inventory_locations, inventories) plus
// inventory_movements itself exist and are empty. Tests that need a database
// are skipped when DATABASE_URL is not set.
func newInventoryMovementsTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := newInventoriesTestPool(t)

	schema := `CREATE TABLE IF NOT EXISTS butiks_engine.inventory_movements (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		inventory_id UUID REFERENCES butiks_engine.inventories (id) ON DELETE CASCADE,
		quantity INT NOT NULL,
		movement_type TEXT NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`
	if _, err := pool.Exec(ctx, schema); err != nil {
		t.Fatalf("preparing inventory_movements schema: %v", err)
	}

	truncate := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE butiks_engine.inventory_movements RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncating inventory_movements: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return ctx, pool
}

// seedInventoryMovementsFixtures creates a product, variant, location and a
// single inventory row with the given starting quantity, returning the
// inventory id. suffix must be unique per call within a test so the
// products.slug and inventory_locations.code unique constraints aren't
// violated when a test needs more than one inventory.
func seedInventoryMovementsFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string, startingQuantity int) string {
	t.Helper()

	productID := seedProduct(t, ctx, pool, "Widget "+suffix, "widget-"+suffix, "a widget", time.Now().UTC())
	variantID := seedProductVariant(t, ctx, pool, productID, "widget-sku-"+suffix, 9.99, time.Now().UTC())
	locationID := seedInventoryLocation(t, ctx, pool, "Main "+suffix, "main-"+suffix, "addr", true, time.Now().UTC())
	return seedInventory(t, ctx, pool, variantID, locationID, startingQuantity, time.Now().UTC())
}

func TestInventoryMovementsRepository(t *testing.T) {
	t.Run("resolveInventoryMovementsPagination", func(t *testing.T) {
		ptr := func(i int) *int { return &i }

		cases := []struct {
			name       string
			params     *GetInventoryMovementsParams
			wantLimit  int
			wantOffset int
		}{
			{"nil params -> defaults", nil, defaultInventoryMovementsLimit, 0},
			{"empty params -> defaults", &GetInventoryMovementsParams{}, defaultInventoryMovementsLimit, 0},
			{"explicit limit and offset", &GetInventoryMovementsParams{Limit: ptr(10), Offset: ptr(20)}, 10, 20},
			{"limit above max is capped", &GetInventoryMovementsParams{Limit: ptr(maxInventoryMovementsLimit + 1)}, maxInventoryMovementsLimit, 0},
			{"zero limit -> default", &GetInventoryMovementsParams{Limit: ptr(0)}, defaultInventoryMovementsLimit, 0},
			{"negative limit -> default", &GetInventoryMovementsParams{Limit: ptr(-5)}, defaultInventoryMovementsLimit, 0},
			{"negative offset -> zero", &GetInventoryMovementsParams{Offset: ptr(-5)}, defaultInventoryMovementsLimit, 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotLimit, gotOffset := resolveInventoryMovementsPagination(tc.params)
				if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
					t.Fatalf("resolveInventoryMovementsPagination(%+v) = (%d, %d), want (%d, %d)",
						tc.params, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
				}
			})
		}
	})

	t.Run("SaveInventoryMovement inserts the movement and increases inventory quantity", func(t *testing.T) {
		ctx, pool := newInventoryMovementsTestPool(t)
		repo := NewInventoryMovementsRepositoryHandler(ctx, pool, nil)
		inventoriesRepo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		inventoryID := seedInventoryMovementsFixtures(t, ctx, pool, "a", 5)

		created := time.Now().UTC().Truncate(time.Second)
		movement := &butik_domain.InventoryMovement{
			ID:           newInventoryMovementID,
			InventoryID:  inventoryID,
			Quantity:     3,
			MovementType: "restock",
			CreatedAt:    created,
			UpdatedAt:    created,
		}
		if err := repo.SaveInventoryMovement(movement); err != nil {
			t.Fatalf("SaveInventoryMovement: %v", err)
		}

		inv, err := inventoriesRepo.GetInventoryByID(inventoryID)
		if err != nil {
			t.Fatalf("GetInventoryByID after movement: %v", err)
		}
		if inv.Quantity != 8 {
			t.Fatalf("inventory quantity after +3 movement = %d, want 8", inv.Quantity)
		}
		if !inv.UpdatedAt.Equal(created) {
			t.Fatalf("inventory updated_at = %s, want %s", inv.UpdatedAt, created)
		}

		movements, err := repo.GetInventoryMovements(&GetInventoryMovementsParams{InventoryID: inventoryID})
		if err != nil {
			t.Fatalf("GetInventoryMovements: %v", err)
		}
		if len(movements) != 1 {
			t.Fatalf("GetInventoryMovements returned %d rows, want 1", len(movements))
		}
		got := movements[0]
		if got.ID != newInventoryMovementID || got.Quantity != 3 || got.MovementType != "restock" {
			t.Fatalf("SaveInventoryMovement stored the wrong values: %+v", got)
		}
	})

	t.Run("SaveInventoryMovement applies a negative quantity as a decrease", func(t *testing.T) {
		ctx, pool := newInventoryMovementsTestPool(t)
		repo := NewInventoryMovementsRepositoryHandler(ctx, pool, nil)
		inventoriesRepo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		inventoryID := seedInventoryMovementsFixtures(t, ctx, pool, "a", 10)

		movement := &butik_domain.InventoryMovement{
			ID:           newInventoryMovementID,
			InventoryID:  inventoryID,
			Quantity:     -4,
			MovementType: "sale",
			CreatedAt:    time.Now().UTC().Truncate(time.Second),
			UpdatedAt:    time.Now().UTC().Truncate(time.Second),
		}
		if err := repo.SaveInventoryMovement(movement); err != nil {
			t.Fatalf("SaveInventoryMovement: %v", err)
		}

		inv, err := inventoriesRepo.GetInventoryByID(inventoryID)
		if err != nil {
			t.Fatalf("GetInventoryByID after movement: %v", err)
		}
		if inv.Quantity != 6 {
			t.Fatalf("inventory quantity after -4 movement = %d, want 6", inv.Quantity)
		}
	})

	t.Run("SaveInventoryMovement rejects a movement that would take inventory below zero", func(t *testing.T) {
		ctx, pool := newInventoryMovementsTestPool(t)
		repo := NewInventoryMovementsRepositoryHandler(ctx, pool, nil)
		inventoriesRepo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		inventoryID := seedInventoryMovementsFixtures(t, ctx, pool, "a", 3)

		movement := &butik_domain.InventoryMovement{
			ID:           newInventoryMovementID,
			InventoryID:  inventoryID,
			Quantity:     -10,
			MovementType: "sale",
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}
		err := repo.SaveInventoryMovement(movement)
		if !errors.Is(err, ErrInsufficientInventory) {
			t.Fatalf("SaveInventoryMovement error = %v, want ErrInsufficientInventory", err)
		}

		inv, err := inventoriesRepo.GetInventoryByID(inventoryID)
		if err != nil {
			t.Fatalf("GetInventoryByID after rejected movement: %v", err)
		}
		if inv.Quantity != 3 {
			t.Fatalf("inventory quantity after rejected movement = %d, want unchanged 3", inv.Quantity)
		}

		count, err := repo.CountInventoryMovements(nil)
		if err != nil {
			t.Fatalf("CountInventoryMovements: %v", err)
		}
		if count != 0 {
			t.Fatalf("CountInventoryMovements after rejected movement = %d, want 0 (movement should not be recorded)", count)
		}
	})

	t.Run("SaveInventoryMovement rolls back the movement when the inventory does not exist", func(t *testing.T) {
		ctx, pool := newInventoryMovementsTestPool(t)
		repo := NewInventoryMovementsRepositoryHandler(ctx, pool, nil)

		movement := &butik_domain.InventoryMovement{
			ID:           newInventoryMovementID,
			InventoryID:  missingInventoryID,
			Quantity:     1,
			MovementType: "restock",
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}
		if err := repo.SaveInventoryMovement(movement); err == nil {
			t.Fatal("SaveInventoryMovement with unknown inventory id = nil error, want an error")
		}

		count, err := repo.CountInventoryMovements(nil)
		if err != nil {
			t.Fatalf("CountInventoryMovements: %v", err)
		}
		if count != 0 {
			t.Fatalf("CountInventoryMovements after failed save = %d, want 0 (movement should have been rolled back)", count)
		}
	})

	t.Run("GetInventoryMovements filters by inventory, orders by created_at desc, and paginates", func(t *testing.T) {
		ctx, pool := newInventoryMovementsTestPool(t)
		repo := NewInventoryMovementsRepositoryHandler(ctx, pool, nil)

		inventoryA := seedInventoryMovementsFixtures(t, ctx, pool, "a", 100)
		inventoryB := seedInventoryMovementsFixtures(t, ctx, pool, "b", 100)

		base := time.Now().UTC().Truncate(time.Second)
		ids := []string{
			"11111111-1111-1111-1111-111111111111",
			"22222222-2222-2222-2222-222222222222",
			"33333333-3333-3333-3333-333333333333",
		}
		for i, id := range ids {
			m := &butik_domain.InventoryMovement{
				ID:           id,
				InventoryID:  inventoryA,
				Quantity:     1,
				MovementType: "restock",
				CreatedAt:    base.Add(time.Duration(-i) * time.Minute),
				UpdatedAt:    base.Add(time.Duration(-i) * time.Minute),
			}
			if err := repo.SaveInventoryMovement(m); err != nil {
				t.Fatalf("SaveInventoryMovement[%d]: %v", i, err)
			}
		}
		// Movement against a different inventory must not leak into inventoryA's results.
		if err := repo.SaveInventoryMovement(&butik_domain.InventoryMovement{
			ID:           "44444444-4444-4444-4444-444444444444",
			InventoryID:  inventoryB,
			Quantity:     1,
			MovementType: "restock",
			CreatedAt:    base,
			UpdatedAt:    base,
		}); err != nil {
			t.Fatalf("SaveInventoryMovement for inventoryB: %v", err)
		}

		limit := 2
		offset := 1
		movements, err := repo.GetInventoryMovements(&GetInventoryMovementsParams{
			InventoryID: inventoryA,
			Limit:       &limit,
			Offset:      &offset,
		})
		if err != nil {
			t.Fatalf("GetInventoryMovements: %v", err)
		}
		if len(movements) != 2 {
			t.Fatalf("GetInventoryMovements returned %d rows, want 2", len(movements))
		}
		if movements[0].ID != ids[1] || movements[1].ID != ids[2] {
			t.Fatalf("GetInventoryMovements order/pagination mismatch: got [%s %s], want [%s %s]",
				movements[0].ID, movements[1].ID, ids[1], ids[2])
		}
		for _, m := range movements {
			if m.InventoryID != inventoryA {
				t.Fatalf("GetInventoryMovements leaked a row from another inventory: %+v", m)
			}
		}
	})

	t.Run("CountInventoryMovements counts all rows and filters by inventory", func(t *testing.T) {
		ctx, pool := newInventoryMovementsTestPool(t)
		repo := NewInventoryMovementsRepositoryHandler(ctx, pool, nil)

		inventoryA := seedInventoryMovementsFixtures(t, ctx, pool, "a", 100)
		inventoryB := seedInventoryMovementsFixtures(t, ctx, pool, "b", 100)

		now := time.Now().UTC()
		for i := range 3 {
			m := &butik_domain.InventoryMovement{
				ID:           []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}[i],
				InventoryID:  inventoryA,
				Quantity:     1,
				MovementType: "restock",
				CreatedAt:    now.Add(time.Duration(i) * time.Minute),
				UpdatedAt:    now.Add(time.Duration(i) * time.Minute),
			}
			if err := repo.SaveInventoryMovement(m); err != nil {
				t.Fatalf("SaveInventoryMovement[%d]: %v", i, err)
			}
		}
		if err := repo.SaveInventoryMovement(&butik_domain.InventoryMovement{
			ID:           "44444444-4444-4444-4444-444444444444",
			InventoryID:  inventoryB,
			Quantity:     1,
			MovementType: "restock",
			CreatedAt:    now,
			UpdatedAt:    now,
		}); err != nil {
			t.Fatalf("SaveInventoryMovement for inventoryB: %v", err)
		}

		total, err := repo.CountInventoryMovements(nil)
		if err != nil {
			t.Fatalf("CountInventoryMovements(nil): %v", err)
		}
		if total != 4 {
			t.Fatalf("CountInventoryMovements(nil) = %d, want 4", total)
		}

		filtered, err := repo.CountInventoryMovements(&CountInventoryMovementsParams{InventoryID: inventoryA})
		if err != nil {
			t.Fatalf("CountInventoryMovements(inventoryA): %v", err)
		}
		if filtered != 3 {
			t.Fatalf("CountInventoryMovements(inventoryA) = %d, want 3", filtered)
		}
	})
}
