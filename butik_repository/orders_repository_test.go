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

// A few syntactically valid UUIDs that gen_random_uuid() will never produce
// collisions with in a freshly truncated table.
const (
	newOrderID      = "66666666-6666-6666-6666-666666666666"
	newOrderItemID  = "77777777-7777-7777-7777-777777777777"
	newOrderItemID2 = "88888888-8888-8888-8888-888888888888"
	missingOrderID  = "99999999-9999-9999-9999-999999999999"
)

// newOrdersTestPool connects to the database pointed at by DATABASE_URL and
// makes sure the tables CreateOrder depends on (products, product_variants,
// inventory_locations, inventories) plus orders, order_items and
// inventory_movements exist and are empty. Tests that need a database are
// skipped when DATABASE_URL is not set.
func newOrdersTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	ctx, pool := newInventoriesTestPool(t)

	schema := []string{
		`CREATE TABLE IF NOT EXISTS butiks_engine.orders (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			total_amount NUMERIC(10, 2) NOT NULL,
			total_price NUMERIC(10, 2) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS butiks_engine.order_items (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			order_id UUID NOT NULL REFERENCES butiks_engine.orders (id) ON DELETE CASCADE,
			product_variant_id UUID NOT NULL,
			location_id UUID NOT NULL REFERENCES butiks_engine.inventory_locations (id),
			quantity INT NOT NULL CHECK (quantity > 0),
			unit_price NUMERIC(12, 2) NOT NULL CHECK (unit_price >= 0),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS butiks_engine.inventory_movements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			inventory_id UUID REFERENCES butiks_engine.inventories (id) ON DELETE CASCADE,
			quantity INT NOT NULL,
			movement_type TEXT NOT NULL,
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
			butiks_engine.order_items,
			butiks_engine.orders,
			butiks_engine.inventory_movements,
			butiks_engine.inventories,
			butiks_engine.inventory_locations,
			butiks_engine.product_variants
			RESTART IDENTITY CASCADE`
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("truncating orders schema: %v", err)
		}
	}
	truncate()
	t.Cleanup(truncate)

	return ctx, pool
}

// seedOrderFixtures creates a product, variant, location and a single
// inventory row with the given starting quantity, returning the variant,
// location and inventory ids. suffix must be unique per call within a test
// so the products.slug and inventory_locations.code unique constraints
// aren't violated when a test needs more than one inventory.
func seedOrderFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string, startingQuantity int) (variantID, locationID, inventoryID string) {
	t.Helper()

	productID := seedProduct(t, ctx, pool, "Widget "+suffix, "widget-"+suffix, "a widget", time.Now().UTC())
	variantID = seedProductVariant(t, ctx, pool, productID, "widget-sku-"+suffix, 9.99, time.Now().UTC())
	locationID = seedInventoryLocation(t, ctx, pool, "Main "+suffix, "main-"+suffix, "addr", true, time.Now().UTC())
	inventoryID = seedInventory(t, ctx, pool, variantID, locationID, startingQuantity, time.Now().UTC())
	return variantID, locationID, inventoryID
}

// seedOrder inserts an order directly (bypassing CreateOrder, which also
// requires inventory fixtures) with an explicit created_at so tests can
// assert on ordering, and returns its generated id.
func seedOrder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, totalAmount, totalPrice float64, createdAt time.Time) string {
	t.Helper()

	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO butiks_engine.orders (total_amount, total_price, created_at, updated_at)
		 VALUES ($1, $2, $3, $3)
		 RETURNING id`,
		totalAmount, totalPrice, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seeding order: %v", err)
	}
	return id
}

func countOrderItems(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID string) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM butiks_engine.order_items WHERE order_id = $1`, orderID).Scan(&count); err != nil {
		t.Fatalf("counting order_items: %v", err)
	}
	return count
}

func countOrders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orderID string) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM butiks_engine.orders WHERE id = $1`, orderID).Scan(&count); err != nil {
		t.Fatalf("counting orders: %v", err)
	}
	return count
}

func countInventoryMovementsForInventory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, inventoryID string) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM butiks_engine.inventory_movements WHERE inventory_id = $1`, inventoryID).Scan(&count); err != nil {
		t.Fatalf("counting inventory_movements: %v", err)
	}
	return count
}

func TestOrdersRepository(t *testing.T) {
	t.Run("resolveOrdersPagination", func(t *testing.T) {
		ptr := func(i int) *int { return &i }

		cases := []struct {
			name       string
			params     *GetOrdersParams
			wantLimit  int
			wantOffset int
		}{
			{"nil params -> defaults", nil, defaultOrdersLimit, 0},
			{"empty params -> defaults", &GetOrdersParams{}, defaultOrdersLimit, 0},
			{"explicit limit and offset", &GetOrdersParams{Limit: ptr(10), Offset: ptr(20)}, 10, 20},
			{"limit above max is capped", &GetOrdersParams{Limit: ptr(maxOrdersLimit + 1)}, maxOrdersLimit, 0},
			{"zero limit -> default", &GetOrdersParams{Limit: ptr(0)}, defaultOrdersLimit, 0},
			{"negative limit -> default", &GetOrdersParams{Limit: ptr(-5)}, defaultOrdersLimit, 0},
			{"negative offset -> zero", &GetOrdersParams{Offset: ptr(-5)}, defaultOrdersLimit, 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotLimit, gotOffset := resolveOrdersPagination(tc.params)
				if gotLimit != tc.wantLimit || gotOffset != tc.wantOffset {
					t.Fatalf("resolveOrdersPagination(%+v) = (%d, %d), want (%d, %d)",
						tc.params, gotLimit, gotOffset, tc.wantLimit, tc.wantOffset)
				}
			})
		}
	})

	t.Run("CountOrders", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		count, err := repo.CountOrders()
		if err != nil {
			t.Fatalf("CountOrders on empty table: %v", err)
		}
		if count != 0 {
			t.Fatalf("CountOrders on empty table = %d, want 0", count)
		}

		now := time.Now().UTC()
		for i := range 3 {
			seedOrder(t, ctx, pool, 9.99, 9.99, now.Add(time.Duration(i)*time.Minute))
		}

		count, err = repo.CountOrders()
		if err != nil {
			t.Fatalf("CountOrders after seeding: %v", err)
		}
		if count != 3 {
			t.Fatalf("CountOrders after seeding = %d, want 3", count)
		}
	})

	t.Run("GetOrders empty table returns empty slice", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		orders, err := repo.GetOrders(nil)
		if err != nil {
			t.Fatalf("GetOrders on empty table: %v", err)
		}
		if orders == nil {
			t.Fatal("GetOrders returned nil slice, want non-nil empty slice")
		}
		if len(orders) != 0 {
			t.Fatalf("GetOrders on empty table returned %d orders, want 0", len(orders))
		}
	})

	t.Run("GetOrders maps columns and orders by created_at desc", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		oldID := seedOrder(t, ctx, pool, 10, 10, base.Add(-2*time.Hour))
		midID := seedOrder(t, ctx, pool, 20, 20, base.Add(-1*time.Hour))
		newID := seedOrder(t, ctx, pool, 30, 30, base)

		orders, err := repo.GetOrders(nil)
		if err != nil {
			t.Fatalf("GetOrders: %v", err)
		}
		if len(orders) != 3 {
			t.Fatalf("GetOrders returned %d orders, want 3", len(orders))
		}

		wantOrder := []string{newID, midID, oldID}
		for i, want := range wantOrder {
			if orders[i].ID != want {
				t.Fatalf("orders[%d].ID = %s, want %s (order should be created_at desc)", i, orders[i].ID, want)
			}
		}

		first := orders[0]
		if first.TotalAmount != 30 || first.TotalPrice != 30 {
			t.Fatalf("first order mapped incorrectly: %+v", first)
		}
		if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
			t.Fatalf("timestamps not populated: %+v", first)
		}
	})

	t.Run("GetOrders respects limit and offset", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		ids := make([]string, 5)
		for i := range 5 {
			// Newest first index: created later => earlier in result order.
			ids[i] = seedOrder(t, ctx, pool, float64(i), float64(i), base.Add(time.Duration(-i)*time.Minute))
		}
		// ids[0] is newest, ids[4] is oldest -> result order is ids[0..4].

		limit := 2
		offset := 1
		orders, err := repo.GetOrders(&GetOrdersParams{Limit: &limit, Offset: &offset})
		if err != nil {
			t.Fatalf("GetOrders with limit/offset: %v", err)
		}
		if len(orders) != 2 {
			t.Fatalf("GetOrders returned %d orders, want 2", len(orders))
		}
		if orders[0].ID != ids[1] || orders[1].ID != ids[2] {
			t.Fatalf("GetOrders limit=2 offset=1 = [%s %s], want [%s %s]",
				orders[0].ID, orders[1].ID, ids[1], ids[2])
		}
	})

	t.Run("GetOrderByID maps columns for an existing order", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		base := time.Now().UTC().Truncate(time.Second)
		id := seedOrder(t, ctx, pool, 49.97, 49.97, base)

		got, err := repo.GetOrderByID(&GetOrderByIDParams{ID: id})
		if err != nil {
			t.Fatalf("GetOrderByID: %v", err)
		}
		if got.Order.ID != id || got.Order.TotalAmount != 49.97 || got.Order.TotalPrice != 49.97 {
			t.Fatalf("GetOrderByID mapped incorrectly: %+v", got)
		}
		if !got.Order.CreatedAt.Equal(base) || !got.Order.UpdatedAt.Equal(base) {
			t.Fatalf("GetOrderByID timestamps = (%s, %s), want %s", got.Order.CreatedAt, got.Order.UpdatedAt, base)
		}
		if len(got.OrderItems) != 0 {
			t.Fatalf("GetOrderByID without IncludeOrderItems returned %d items, want 0", len(got.OrderItems))
		}
	})

	t.Run("GetOrderByID returns pgx.ErrNoRows when the id is unknown", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		seedOrder(t, ctx, pool, 1, 1, time.Now().UTC())

		got, err := repo.GetOrderByID(&GetOrderByIDParams{ID: missingOrderID})
		if err == nil {
			t.Fatalf("GetOrderByID(unknown) = %+v, want an error", got)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("GetOrderByID(unknown) error = %v, want pgx.ErrNoRows", err)
		}
		if got != nil {
			t.Fatalf("GetOrderByID(unknown) returned non-nil order %+v", got)
		}
	})

	t.Run("GetOrderByID returns an error when id is empty", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		if _, err := repo.GetOrderByID(&GetOrderByIDParams{}); err == nil {
			t.Fatal("GetOrderByID with empty id = nil error, want an error")
		}
		if _, err := repo.GetOrderByID(nil); err == nil {
			t.Fatal("GetOrderByID(nil) = nil error, want an error")
		}
	})

	t.Run("GetOrderByID with IncludeOrderItems populates the order's items", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		variantA, locationA, _ := seedOrderFixtures(t, ctx, pool, "a", 10)
		variantB, locationB, _ := seedOrderFixtures(t, ctx, pool, "b", 10)

		now := time.Now().UTC().Truncate(time.Second)
		order := &butik_domain.Order{ID: newOrderID, TotalAmount: 19.98, TotalPrice: 19.98, CreatedAt: now, UpdatedAt: now}
		items := []*butik_domain.OrderItem{
			{ID: newOrderItemID, ProductVariantID: variantA, LocationID: locationA, Quantity: 1, UnitPrice: 9.99, CreatedAt: now, UpdatedAt: now},
			{ID: newOrderItemID2, ProductVariantID: variantB, LocationID: locationB, Quantity: 1, UnitPrice: 9.99, CreatedAt: now, UpdatedAt: now},
		}
		if err := repo.CreateOrder(CreateOrderParams{Order: order, OrderItems: items}); err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}

		include := true
		got, err := repo.GetOrderByID(&GetOrderByIDParams{ID: newOrderID, IncludeOrderItems: &include})
		if err != nil {
			t.Fatalf("GetOrderByID with IncludeOrderItems: %v", err)
		}
		if got.Order.ID != newOrderID {
			t.Fatalf("GetOrderByID returned order %s, want %s", got.Order.ID, newOrderID)
		}
		if len(got.OrderItems) != 2 {
			t.Fatalf("GetOrderByID returned %d items, want 2", len(got.OrderItems))
		}
		gotItemIDs := map[string]bool{got.OrderItems[0].ID: true, got.OrderItems[1].ID: true}
		if !gotItemIDs[newOrderItemID] || !gotItemIDs[newOrderItemID2] {
			t.Fatalf("GetOrderByID items = %+v, want ids %s and %s", got.OrderItems, newOrderItemID, newOrderItemID2)
		}
	})

	t.Run("DeleteOrderByID removes only the target row", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		keepID := seedOrder(t, ctx, pool, 1, 1, now)
		dropID := seedOrder(t, ctx, pool, 2, 2, now)

		if err := repo.DeleteOrderByID(dropID); err != nil {
			t.Fatalf("DeleteOrderByID: %v", err)
		}

		if _, err := repo.GetOrderByID(&GetOrderByIDParams{ID: dropID}); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("after delete, GetOrderByID(dropID) error = %v, want pgx.ErrNoRows", err)
		}
		if _, err := repo.GetOrderByID(&GetOrderByIDParams{ID: keepID}); err != nil {
			t.Fatalf("DeleteOrderByID removed the wrong row: %v", err)
		}

		count, err := repo.CountOrders()
		if err != nil {
			t.Fatalf("CountOrders: %v", err)
		}
		if count != 1 {
			t.Fatalf("CountOrders after delete = %d, want 1", count)
		}
	})

	t.Run("DeleteOrderByID is a no-op for an unknown id", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		seedOrder(t, ctx, pool, 1, 1, time.Now().UTC())

		if err := repo.DeleteOrderByID(missingOrderID); err != nil {
			t.Fatalf("DeleteOrderByID(unknown) = %v, want nil", err)
		}

		count, err := repo.CountOrders()
		if err != nil {
			t.Fatalf("CountOrders: %v", err)
		}
		if count != 1 {
			t.Fatalf("CountOrders after no-op delete = %d, want 1", count)
		}
	})

	t.Run("DeleteOrderByID restores inventory, records a reversal movement, and removes the order and its items", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)
		inventoriesRepo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		variantA, locationA, inventoryA := seedOrderFixtures(t, ctx, pool, "a", 10)
		variantB, locationB, inventoryB := seedOrderFixtures(t, ctx, pool, "b", 5)

		now := time.Now().UTC().Truncate(time.Second)
		order := &butik_domain.Order{
			ID:          newOrderID,
			TotalAmount: 49.97,
			TotalPrice:  49.97,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		items := []*butik_domain.OrderItem{
			{
				ID:               newOrderItemID,
				ProductVariantID: variantA,
				LocationID:       locationA,
				Quantity:         3,
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
			{
				ID:               newOrderItemID2,
				ProductVariantID: variantB,
				LocationID:       locationB,
				Quantity:         2,
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
		}
		if err := repo.CreateOrder(CreateOrderParams{Order: order, OrderItems: items}); err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}
		// Sanity check the decrement happened before asserting the reversal.
		invAAfterCreate, err := inventoriesRepo.GetInventoryByID(inventoryA)
		if err != nil || invAAfterCreate.Quantity != 7 {
			t.Fatalf("inventory a quantity after CreateOrder = %+v (err %v), want 7", invAAfterCreate, err)
		}
		invBAfterCreate, err := inventoriesRepo.GetInventoryByID(inventoryB)
		if err != nil || invBAfterCreate.Quantity != 3 {
			t.Fatalf("inventory b quantity after CreateOrder = %+v (err %v), want 3", invBAfterCreate, err)
		}

		if err := repo.DeleteOrderByID(newOrderID); err != nil {
			t.Fatalf("DeleteOrderByID: %v", err)
		}

		invA, err := inventoriesRepo.GetInventoryByID(inventoryA)
		if err != nil {
			t.Fatalf("GetInventoryByID(a) after DeleteOrderByID: %v", err)
		}
		if invA.Quantity != 10 {
			t.Fatalf("inventory a quantity after DeleteOrderByID = %d, want restored to 10", invA.Quantity)
		}

		invB, err := inventoriesRepo.GetInventoryByID(inventoryB)
		if err != nil {
			t.Fatalf("GetInventoryByID(b) after DeleteOrderByID: %v", err)
		}
		if invB.Quantity != 5 {
			t.Fatalf("inventory b quantity after DeleteOrderByID = %d, want restored to 5", invB.Quantity)
		}

		// The order's decrement plus the delete's restore is 2 movements each.
		if got := countInventoryMovementsForInventory(t, ctx, pool, inventoryA); got != 2 {
			t.Fatalf("inventory_movements rows for inventory a after DeleteOrderByID = %d, want 2", got)
		}
		if got := countInventoryMovementsForInventory(t, ctx, pool, inventoryB); got != 2 {
			t.Fatalf("inventory_movements rows for inventory b after DeleteOrderByID = %d, want 2", got)
		}

		var lastMovementType string
		var lastMovementQuantity int
		if err := pool.QueryRow(ctx,
			`SELECT movement_type, quantity FROM butiks_engine.inventory_movements
			 WHERE inventory_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
			inventoryA,
		).Scan(&lastMovementType, &lastMovementQuantity); err != nil {
			t.Fatalf("querying latest inventory movement: %v", err)
		}
		if lastMovementType != orderCancellationMovementType || lastMovementQuantity != 3 {
			t.Fatalf("latest inventory movement for a = (%s, %d), want (%s, 3)", lastMovementType, lastMovementQuantity, orderCancellationMovementType)
		}

		if got := countOrders(t, ctx, pool, newOrderID); got != 0 {
			t.Fatalf("orders rows after DeleteOrderByID = %d, want 0", got)
		}
		if got := countOrderItems(t, ctx, pool, newOrderID); got != 0 {
			t.Fatalf("order_items rows after DeleteOrderByID = %d, want 0 (should cascade)", got)
		}
	})

	t.Run("CreateOrder inserts the order and items and decrements inventory", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)
		inventoriesRepo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		variantA, locationA, inventoryA := seedOrderFixtures(t, ctx, pool, "a", 10)
		variantB, locationB, inventoryB := seedOrderFixtures(t, ctx, pool, "b", 5)

		now := time.Now().UTC().Truncate(time.Second)
		order := &butik_domain.Order{
			ID:          newOrderID,
			TotalAmount: 49.97,
			TotalPrice:  49.97,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		items := []*butik_domain.OrderItem{
			{
				ID:               newOrderItemID,
				ProductVariantID: variantA,
				LocationID:       locationA,
				Quantity:         3,
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
			{
				ID:               newOrderItemID2,
				ProductVariantID: variantB,
				LocationID:       locationB,
				Quantity:         2,
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
		}

		if err := repo.CreateOrder(CreateOrderParams{Order: order, OrderItems: items}); err != nil {
			t.Fatalf("CreateOrder: %v", err)
		}

		if got := countOrders(t, ctx, pool, newOrderID); got != 1 {
			t.Fatalf("orders rows with id %s = %d, want 1", newOrderID, got)
		}
		if got := countOrderItems(t, ctx, pool, newOrderID); got != 2 {
			t.Fatalf("order_items rows for order = %d, want 2", got)
		}

		invA, err := inventoriesRepo.GetInventoryByID(inventoryA)
		if err != nil {
			t.Fatalf("GetInventoryByID(a) after CreateOrder: %v", err)
		}
		if invA.Quantity != 7 {
			t.Fatalf("inventory a quantity after order = %d, want 7", invA.Quantity)
		}

		invB, err := inventoriesRepo.GetInventoryByID(inventoryB)
		if err != nil {
			t.Fatalf("GetInventoryByID(b) after CreateOrder: %v", err)
		}
		if invB.Quantity != 3 {
			t.Fatalf("inventory b quantity after order = %d, want 3", invB.Quantity)
		}

		if got := countInventoryMovementsForInventory(t, ctx, pool, inventoryA); got != 1 {
			t.Fatalf("inventory_movements rows for inventory a = %d, want 1", got)
		}
		if got := countInventoryMovementsForInventory(t, ctx, pool, inventoryB); got != 1 {
			t.Fatalf("inventory_movements rows for inventory b = %d, want 1", got)
		}
	})

	t.Run("CreateOrder rolls back everything when one item has insufficient inventory", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)
		inventoriesRepo := NewInventoriesRepositoryHandler(ctx, pool, nil)

		variantA, locationA, inventoryA := seedOrderFixtures(t, ctx, pool, "a", 5)
		variantB, locationB, inventoryB := seedOrderFixtures(t, ctx, pool, "b", 1)

		now := time.Now().UTC().Truncate(time.Second)
		order := &butik_domain.Order{
			ID:          newOrderID,
			TotalAmount: 100,
			TotalPrice:  100,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		items := []*butik_domain.OrderItem{
			{
				ID:               newOrderItemID,
				ProductVariantID: variantA,
				LocationID:       locationA,
				Quantity:         3,
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
			{
				ID:               newOrderItemID2,
				ProductVariantID: variantB,
				LocationID:       locationB,
				Quantity:         2, // only 1 in stock
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
		}

		err := repo.CreateOrder(CreateOrderParams{Order: order, OrderItems: items})
		if !errors.Is(err, ErrInsufficientInventory) {
			t.Fatalf("CreateOrder error = %v, want ErrInsufficientInventory", err)
		}

		if got := countOrders(t, ctx, pool, newOrderID); got != 0 {
			t.Fatalf("orders rows after rejected order = %d, want 0 (order should have been rolled back)", got)
		}
		if got := countOrderItems(t, ctx, pool, newOrderID); got != 0 {
			t.Fatalf("order_items rows after rejected order = %d, want 0", got)
		}

		invA, err := inventoriesRepo.GetInventoryByID(inventoryA)
		if err != nil {
			t.Fatalf("GetInventoryByID(a) after rejected order: %v", err)
		}
		if invA.Quantity != 5 {
			t.Fatalf("inventory a quantity after rejected order = %d, want unchanged 5", invA.Quantity)
		}

		invB, err := inventoriesRepo.GetInventoryByID(inventoryB)
		if err != nil {
			t.Fatalf("GetInventoryByID(b) after rejected order: %v", err)
		}
		if invB.Quantity != 1 {
			t.Fatalf("inventory b quantity after rejected order = %d, want unchanged 1", invB.Quantity)
		}

		if got := countInventoryMovementsForInventory(t, ctx, pool, inventoryA); got != 0 {
			t.Fatalf("inventory_movements rows for inventory a after rejected order = %d, want 0", got)
		}
		if got := countInventoryMovementsForInventory(t, ctx, pool, inventoryB); got != 0 {
			t.Fatalf("inventory_movements rows for inventory b after rejected order = %d, want 0", got)
		}
	})

	t.Run("CreateOrder rolls back when an item's product variant and location have no inventory row", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		variantA, _, _ := seedOrderFixtures(t, ctx, pool, "a", 10)
		_, otherLocation, _ := seedOrderFixtures(t, ctx, pool, "b", 10)

		now := time.Now().UTC().Truncate(time.Second)
		order := &butik_domain.Order{
			ID:          newOrderID,
			TotalAmount: 9.99,
			TotalPrice:  9.99,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		items := []*butik_domain.OrderItem{
			{
				ID:               newOrderItemID,
				ProductVariantID: variantA,
				LocationID:       otherLocation, // variant a has no inventory row at location b
				Quantity:         1,
				UnitPrice:        9.99,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
		}

		if err := repo.CreateOrder(CreateOrderParams{Order: order, OrderItems: items}); err == nil {
			t.Fatal("CreateOrder with unknown inventory = nil error, want an error")
		}

		if got := countOrders(t, ctx, pool, newOrderID); got != 0 {
			t.Fatalf("orders rows after failed order = %d, want 0 (order should have been rolled back)", got)
		}
	})

	t.Run("CreateOrder returns an error when the order is nil", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		items := []*butik_domain.OrderItem{
			{ID: newOrderItemID, ProductVariantID: "x", LocationID: "y", Quantity: 1, UnitPrice: 1, CreatedAt: now, UpdatedAt: now},
		}
		if err := repo.CreateOrder(CreateOrderParams{OrderItems: items}); err == nil {
			t.Fatal("CreateOrder with nil order = nil error, want an error")
		}
	})

	t.Run("CreateOrder returns an error when there are no order items", func(t *testing.T) {
		ctx, pool := newOrdersTestPool(t)
		repo := NewOrdersRepositoryHandler(ctx, pool, nil)

		now := time.Now().UTC()
		order := &butik_domain.Order{ID: newOrderID, TotalAmount: 1, TotalPrice: 1, CreatedAt: now, UpdatedAt: now}
		if err := repo.CreateOrder(CreateOrderParams{Order: order}); err == nil {
			t.Fatal("CreateOrder with no items = nil error, want an error")
		}

		if got := countOrders(t, ctx, pool, newOrderID); got != 0 {
			t.Fatalf("orders rows after invalid order = %d, want 0", got)
		}
	})
}
