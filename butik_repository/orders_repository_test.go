package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A few syntactically valid UUIDs that gen_random_uuid() will never produce
// collisions with in a freshly truncated table.
const (
	newOrderID      = "66666666-6666-6666-6666-666666666666"
	newOrderItemID  = "77777777-7777-7777-7777-777777777777"
	newOrderItemID2 = "88888888-8888-8888-8888-888888888888"
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
