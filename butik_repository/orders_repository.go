package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// orderInventoryMovementType marks the inventory_movements rows CreateOrder
// writes when it decrements stock for an order. The movement type catalog is
// still open (see 009_create_inventory_movements.sql), so this is a
// provisional label rather than a fixed enum value.
const orderInventoryMovementType = "order"

type CreateOrderParams struct {
	Order      *butik_domain.Order
	OrderItems []*butik_domain.OrderItem
}

type OrdersRepository interface {
	CreateOrder(params CreateOrderParams) error
}

type OrdersRepositoryHandler struct {
	ctx  context.Context
	pool *pgxpool.Pool
	user *butik_domain.User
}

func NewOrdersRepositoryHandler(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *OrdersRepositoryHandler {
	return &OrdersRepositoryHandler{
		ctx:  ctx,
		pool: pool,
		user: user,
	}
}

// CreateOrder inserts the order and its items and, in the same transaction,
// decrements the inventory backing each item (locked with FOR UPDATE, keyed
// by product variant and location so concurrent orders against the same
// stock are serialized instead of racing on a stale quantity). Each
// decrement is also recorded as an inventory_movements row so the movement
// ledger reflects the sale. If any item's location doesn't have enough
// stock, nothing is written and ErrInsufficientInventory is returned.
func (h *OrdersRepositoryHandler) CreateOrder(params CreateOrderParams) error {
	if params.Order == nil {
		return fmt.Errorf("creating order: order is required")
	}
	if len(params.OrderItems) == 0 {
		return fmt.Errorf("creating order: at least one order item is required")
	}

	tx, err := h.pool.Begin(h.ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(h.ctx)

	const orderQuery = `
		INSERT INTO butiks_engine.orders (id, total_amount, total_price, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	if _, err := tx.Exec(
		h.ctx,
		orderQuery,
		params.Order.ID,
		params.Order.TotalAmount,
		params.Order.TotalPrice,
		params.Order.CreatedAt,
		params.Order.UpdatedAt,
	); err != nil {
		return fmt.Errorf("saving order: %w", err)
	}

	const lockInventoryQuery = `
		SELECT id, quantity
		FROM butiks_engine.inventories
		WHERE product_variant_id = $1 AND location_id = $2
		FOR UPDATE
	`
	const updateInventoryQuery = `
		UPDATE butiks_engine.inventories
		SET quantity = $2, updated_at = $3
		WHERE id = $1
	`
	const insertMovementQuery = `
		INSERT INTO butiks_engine.inventory_movements (inventory_id, quantity, movement_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	const insertOrderItemQuery = `
		INSERT INTO butiks_engine.order_items (id, order_id, product_variant_id, location_id, quantity, unit_price, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	for _, item := range params.OrderItems {
		var inventoryID string
		var currentQuantity int
		if err := tx.QueryRow(h.ctx, lockInventoryQuery, item.ProductVariantID, item.LocationID).Scan(&inventoryID, &currentQuantity); err != nil {
			return fmt.Errorf("locking inventory for product variant %s at location %s: %w", item.ProductVariantID, item.LocationID, err)
		}

		newQuantity := currentQuantity - item.Quantity
		if newQuantity < 0 {
			return fmt.Errorf("%w: product variant %s at location %s has %d, order needs %d", ErrInsufficientInventory, item.ProductVariantID, item.LocationID, currentQuantity, item.Quantity)
		}

		if _, err := tx.Exec(h.ctx, updateInventoryQuery, inventoryID, newQuantity, item.UpdatedAt); err != nil {
			return fmt.Errorf("updating inventory quantity: %w", err)
		}

		if _, err := tx.Exec(h.ctx, insertMovementQuery, inventoryID, -item.Quantity, orderInventoryMovementType, item.CreatedAt, item.UpdatedAt); err != nil {
			return fmt.Errorf("saving inventory movement: %w", err)
		}

		if _, err := tx.Exec(
			h.ctx,
			insertOrderItemQuery,
			item.ID,
			params.Order.ID,
			item.ProductVariantID,
			item.LocationID,
			item.Quantity,
			item.UnitPrice,
			item.CreatedAt,
			item.UpdatedAt,
		); err != nil {
			return fmt.Errorf("saving order item: %w", err)
		}
	}

	if err := tx.Commit(h.ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}
