package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// orderInventoryMovementType marks the inventory_movements rows CreateOrder
// writes when it decrements stock for an order, and
// orderCancellationMovementType marks the rows DeleteOrderByID writes when it
// restores that stock. The movement type catalog is still open (see
// 009_create_inventory_movements.sql), so these are provisional labels
// rather than fixed enum values.
const (
	orderInventoryMovementType    = "order"
	orderCancellationMovementType = "order_cancellation"
)

type CreateOrderParams struct {
	Order      *butik_domain.Order
	OrderItems []*butik_domain.OrderItem
}

type GetOrdersParams struct {
	Offset            *int
	Limit             *int
	IncludeOrderItems *bool
}

const (
	defaultOrdersLimit = 50
	maxOrdersLimit     = 200
)

type OrdersRepository interface {
	CreateOrder(params CreateOrderParams) error
	GetOrders(params *GetOrdersParams) ([]*butik_domain.OrderWithOrderItems, error)
	CountOrders() (int, error)
	GetOrderByID(id string) (*butik_domain.Order, error)
	DeleteOrderByID(id string) error
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

// GetOrders lists orders, optionally joined with their order items so a
// detail view can display them without extra lookups.
func (h *OrdersRepositoryHandler) GetOrders(params *GetOrdersParams) ([]*butik_domain.OrderWithOrderItems, error) {
	limit, offset := resolveOrdersPagination(params)

	includeOrderItems := params != nil && params.IncludeOrderItems != nil && *params.IncludeOrderItems

	if includeOrderItems {
		return h.getOrdersWithItems(limit, offset)
	}
	return h.getOrdersWithoutItems(limit, offset)
}

func (h *OrdersRepositoryHandler) getOrdersWithoutItems(limit, offset int) ([]*butik_domain.OrderWithOrderItems, error) {
	const query = `
		SELECT id, total_amount, total_price, created_at, updated_at
		FROM butiks_engine.orders
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := h.pool.Query(h.ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying orders: %w", err)
	}
	defer rows.Close()

	orders := make([]*butik_domain.OrderWithOrderItems, 0)
	for rows.Next() {
		var o butik_domain.Order
		if err := rows.Scan(
			&o.ID,
			&o.TotalAmount,
			&o.TotalPrice,
			&o.CreatedAt,
			&o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning order: %w", err)
		}
		orders = append(orders, &butik_domain.OrderWithOrderItems{Order: o})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating orders: %w", err)
	}

	return orders, nil
}

// getOrdersWithItems fetches the paginated page of orders and then, in a
// second query keyed by the page's order ids, all of their order items.
// A join on order_items would duplicate the order columns once per item, so
// two queries plus an in-memory merge is simpler than de-duplicating join
// rows while still avoiding one query per order (N+1).
func (h *OrdersRepositoryHandler) getOrdersWithItems(limit, offset int) ([]*butik_domain.OrderWithOrderItems, error) {
	orders, err := h.getOrdersWithoutItems(limit, offset)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return orders, nil
	}

	orderIDs := make([]string, len(orders))
	byOrderID := make(map[string]*butik_domain.OrderWithOrderItems, len(orders))
	for i, o := range orders {
		orderIDs[i] = o.Order.ID
		byOrderID[o.Order.ID] = o
	}

	const itemsQuery = `
		SELECT id, order_id, product_variant_id, location_id, quantity, unit_price, created_at, updated_at
		FROM butiks_engine.order_items
		WHERE order_id = ANY($1)
		ORDER BY created_at ASC, id ASC
	`

	rows, err := h.pool.Query(h.ctx, itemsQuery, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("querying order items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var it butik_domain.OrderItem
		if err := rows.Scan(
			&it.ID,
			&it.OrderID,
			&it.ProductVariantID,
			&it.LocationID,
			&it.Quantity,
			&it.UnitPrice,
			&it.CreatedAt,
			&it.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning order item: %w", err)
		}
		if o, ok := byOrderID[it.OrderID]; ok {
			o.OrderItems = append(o.OrderItems, it)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating order items: %w", err)
	}

	return orders, nil
}

func (h *OrdersRepositoryHandler) CountOrders() (int, error) {
	const query = `SELECT COUNT(*) FROM butiks_engine.orders`

	var count int
	if err := h.pool.QueryRow(h.ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting orders: %w", err)
	}

	return count, nil
}

func (h *OrdersRepositoryHandler) GetOrderByID(id string) (*butik_domain.Order, error) {
	const query = `
		SELECT id, total_amount, total_price, created_at, updated_at
		FROM butiks_engine.orders
		WHERE id = $1
	`

	var o butik_domain.Order
	if err := h.pool.QueryRow(h.ctx, query, id).Scan(
		&o.ID,
		&o.TotalAmount,
		&o.TotalPrice,
		&o.CreatedAt,
		&o.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("querying order by id: %w", err)
	}

	return &o, nil
}

// DeleteOrderByID reverts the order: in the same transaction it restores the
// inventory CreateOrder decremented for each of the order's items (locked
// with FOR UPDATE, keyed by product variant and location, mirroring
// CreateOrder's locking), records the restock as an inventory_movements row,
// and then deletes the order. Deleting the order cascades to its order_items
// (see 011_create_order_items.sql), so those aren't deleted separately.
func (h *OrdersRepositoryHandler) DeleteOrderByID(id string) error {
	tx, err := h.pool.Begin(h.ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(h.ctx)

	const itemsQuery = `
		SELECT product_variant_id, location_id, quantity
		FROM butiks_engine.order_items
		WHERE order_id = $1
	`
	rows, err := tx.Query(h.ctx, itemsQuery, id)
	if err != nil {
		return fmt.Errorf("querying order items for order %s: %w", id, err)
	}
	type orderItemStock struct {
		productVariantID string
		locationID       string
		quantity         int
	}
	var items []orderItemStock
	for rows.Next() {
		var it orderItemStock
		if err := rows.Scan(&it.productVariantID, &it.locationID, &it.quantity); err != nil {
			rows.Close()
			return fmt.Errorf("scanning order item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterating order items: %w", err)
	}
	rows.Close()

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

	now := time.Now().UTC()

	for _, item := range items {
		var inventoryID string
		var currentQuantity int
		if err := tx.QueryRow(h.ctx, lockInventoryQuery, item.productVariantID, item.locationID).Scan(&inventoryID, &currentQuantity); err != nil {
			return fmt.Errorf("locking inventory for product variant %s at location %s: %w", item.productVariantID, item.locationID, err)
		}

		newQuantity := currentQuantity + item.quantity

		if _, err := tx.Exec(h.ctx, updateInventoryQuery, inventoryID, newQuantity, now); err != nil {
			return fmt.Errorf("restoring inventory quantity: %w", err)
		}

		if _, err := tx.Exec(h.ctx, insertMovementQuery, inventoryID, item.quantity, orderCancellationMovementType, now, now); err != nil {
			return fmt.Errorf("saving inventory movement: %w", err)
		}
	}

	const deleteOrderQuery = `
		DELETE FROM butiks_engine.orders
		WHERE id = $1
	`
	if _, err := tx.Exec(h.ctx, deleteOrderQuery, id); err != nil {
		return fmt.Errorf("deleting order by id: %w", err)
	}

	if err := tx.Commit(h.ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

// resolveOrdersPagination applies sane defaults and bounds to the optional
// pagination parameters.
func resolveOrdersPagination(params *GetOrdersParams) (limit, offset int) {
	limit = defaultOrdersLimit
	offset = 0

	if params == nil {
		return limit, offset
	}

	if params.Limit != nil && *params.Limit > 0 {
		limit = min(*params.Limit, maxOrdersLimit)
	}

	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}

	return limit, offset
}
