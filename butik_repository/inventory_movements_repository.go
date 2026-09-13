package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInsufficientInventory is returned by SaveInventoryMovement when applying
// the movement's quantity would take the related inventory row below zero.
var ErrInsufficientInventory = errors.New("insufficient inventory")

type GetInventoryMovementsParams struct {
	InventoryID string `json:"inventory_id"`
	Limit       *int   `json:"limit"`
	Offset      *int   `json:"offset"`
}

type CountInventoryMovementsParams struct {
	InventoryID string `json:"inventory_id"`
}

const (
	defaultInventoryMovementsLimit = 50
	maxInventoryMovementsLimit     = 200
)

type InventoryMovementsRepository interface {
	SaveInventoryMovement(movement *butik_domain.InventoryMovement) error
	GetInventoryMovements(params *GetInventoryMovementsParams) ([]*butik_domain.InventoryMovement, error)
	CountInventoryMovements(params *CountInventoryMovementsParams) (int, error)
}

type InventoryMovementsRepositoryHandler struct {
	ctx  context.Context
	pool *pgxpool.Pool
	user *butik_domain.User
}

func NewInventoryMovementsRepositoryHandler(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *InventoryMovementsRepositoryHandler {
	return &InventoryMovementsRepositoryHandler{
		ctx:  ctx,
		pool: pool,
		user: user,
	}
}

// SaveInventoryMovement records the movement and, in the same transaction,
// applies its quantity as a signed delta to the related inventory row's
// quantity. The inventory row is locked with FOR UPDATE for the duration of
// the transaction so concurrent movements against the same inventory are
// serialized instead of racing on a stale quantity. Movements are an
// append-only ledger, so this always inserts a new row rather than upserting.
// If applying the delta would take the inventory below zero, nothing is
// written and ErrInsufficientInventory is returned.
func (h *InventoryMovementsRepositoryHandler) SaveInventoryMovement(movement *butik_domain.InventoryMovement) error {
	tx, err := h.pool.Begin(h.ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(h.ctx)

	const lockQuery = `
		SELECT quantity
		FROM butiks_engine.inventories
		WHERE id = $1
		FOR UPDATE
	`
	var currentQuantity int
	if err := tx.QueryRow(h.ctx, lockQuery, movement.InventoryID).Scan(&currentQuantity); err != nil {
		return fmt.Errorf("locking inventory: %w", err)
	}

	newQuantity := currentQuantity + movement.Quantity
	if newQuantity < 0 {
		return fmt.Errorf("%w: inventory %s has %d, movement needs %d", ErrInsufficientInventory, movement.InventoryID, currentQuantity, movement.Quantity)
	}

	const updateQuery = `
		UPDATE butiks_engine.inventories
		SET quantity = $2, updated_at = $3
		WHERE id = $1
	`
	if _, err := tx.Exec(h.ctx, updateQuery, movement.InventoryID, newQuantity, movement.CreatedAt); err != nil {
		return fmt.Errorf("updating inventory quantity: %w", err)
	}

	const insertQuery = `
		INSERT INTO butiks_engine.inventory_movements (id, inventory_id, quantity, movement_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	if _, err := tx.Exec(
		h.ctx,
		insertQuery,
		movement.ID,
		movement.InventoryID,
		movement.Quantity,
		movement.MovementType,
		movement.CreatedAt,
		movement.UpdatedAt,
	); err != nil {
		return fmt.Errorf("saving inventory movement: %w", err)
	}

	if err := tx.Commit(h.ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

func (h *InventoryMovementsRepositoryHandler) GetInventoryMovements(params *GetInventoryMovementsParams) ([]*butik_domain.InventoryMovement, error) {
	limit, offset := resolveInventoryMovementsPagination(params)

	var inventoryID *string
	if params != nil && params.InventoryID != "" {
		inventoryID = &params.InventoryID
	}

	const query = `
		SELECT id, inventory_id, quantity, movement_type, created_at, updated_at
		FROM butiks_engine.inventory_movements
		WHERE ($1::uuid IS NULL OR inventory_id = $1::uuid)
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := h.pool.Query(h.ctx, query, inventoryID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying inventory movements: %w", err)
	}
	defer rows.Close()

	movements := make([]*butik_domain.InventoryMovement, 0)
	for rows.Next() {
		var m butik_domain.InventoryMovement
		if err := rows.Scan(
			&m.ID,
			&m.InventoryID,
			&m.Quantity,
			&m.MovementType,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning inventory movement: %w", err)
		}
		movements = append(movements, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating inventory movements: %w", err)
	}

	return movements, nil
}

func (h *InventoryMovementsRepositoryHandler) CountInventoryMovements(params *CountInventoryMovementsParams) (int, error) {
	var inventoryID *string
	if params != nil && params.InventoryID != "" {
		inventoryID = &params.InventoryID
	}

	const query = `
		SELECT COUNT(*)
		FROM butiks_engine.inventory_movements
		WHERE ($1::uuid IS NULL OR inventory_id = $1::uuid)
	`

	var count int
	if err := h.pool.QueryRow(h.ctx, query, inventoryID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting inventory movements: %w", err)
	}

	return count, nil
}

// resolveInventoryMovementsPagination applies sane defaults and bounds to the
// optional pagination parameters.
func resolveInventoryMovementsPagination(params *GetInventoryMovementsParams) (limit, offset int) {
	limit = defaultInventoryMovementsLimit
	offset = 0

	if params == nil {
		return limit, offset
	}

	if params.Limit != nil && *params.Limit > 0 {
		limit = min(*params.Limit, maxInventoryMovementsLimit)
	}

	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}

	return limit, offset
}
