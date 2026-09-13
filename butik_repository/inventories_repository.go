package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type GetInventoriesParams struct {
	LocationID               string `json:"location_id"`
	IncludeProductAndVariant *bool  `json:"include_product_and_variant"`
	Offset                   *int   `json:"offset"`
	Limit                    *int   `json:"limit"`
}

type CountInventoryParams struct {
	LocationID string `json:"location_id"`
}

const (
	defaultInventoriesLimit = 50
	maxInventoriesLimit     = 200
)

type InventoriesRepository interface {
	SaveInventory(inventory *butik_domain.Inventory) error
	GetInventoryByID(id string) (*butik_domain.Inventory, error)
	DeleteInventory(id string) error
	GetInventories(params *GetInventoriesParams) ([]*butik_domain.InventoryWithDetails, error)
	CountInventories(params *CountInventoryParams) (int, error)
}

type InventoriesRepositoryHandler struct {
	ctx  context.Context
	pool *pgxpool.Pool
	user *butik_domain.User
}

func NewInventoriesRepositoryHandler(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *InventoriesRepositoryHandler {
	return &InventoriesRepositoryHandler{
		ctx:  ctx,
		pool: pool,
		user: user,
	}
}

// SaveInventory upserts the inventory row, inserting it if it doesn't exist
// or updating it if it does.
func (h *InventoriesRepositoryHandler) SaveInventory(inventory *butik_domain.Inventory) error {
	const query = `
		INSERT INTO butiks_engine.inventories (id, product_variant_id, location_id, product_lot_id, quantity, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			product_variant_id = EXCLUDED.product_variant_id,
			location_id = EXCLUDED.location_id,
			product_lot_id = EXCLUDED.product_lot_id,
			quantity = EXCLUDED.quantity,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
	`

	_, err := h.pool.Exec(
		h.ctx,
		query,
		inventory.ID,
		inventory.ProductVariantID,
		inventory.LocationID,
		inventory.ProductLotID,
		inventory.Quantity,
		inventory.CreatedAt,
		inventory.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("saving inventory: %w", err)
	}
	return nil
}

func (h *InventoriesRepositoryHandler) GetInventoryByID(id string) (*butik_domain.Inventory, error) {
	const query = `
		SELECT id, product_variant_id, location_id, product_lot_id, quantity, created_at, updated_at
		FROM butiks_engine.inventories
		WHERE id = $1
	`

	var i butik_domain.Inventory
	if err := h.pool.QueryRow(h.ctx, query, id).Scan(
		&i.ID,
		&i.ProductVariantID,
		&i.LocationID,
		&i.ProductLotID,
		&i.Quantity,
		&i.CreatedAt,
		&i.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("querying inventory by id: %w", err)
	}

	return &i, nil
}

func (h *InventoriesRepositoryHandler) DeleteInventory(id string) error {
	const query = `
		DELETE FROM butiks_engine.inventories
		WHERE id = $1
	`

	_, err := h.pool.Exec(h.ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting inventory by id: %w", err)
	}

	return nil
}

// GetInventories lists inventory rows, optionally filtered by location and
// optionally joined with their product and product variant so an index view
// can display them without extra lookups.
func (h *InventoriesRepositoryHandler) GetInventories(params *GetInventoriesParams) ([]*butik_domain.InventoryWithDetails, error) {
	limit, offset := resolveInventoriesPagination(params)

	var locationID *string
	includeDetails := false
	if params != nil {
		if params.LocationID != "" {
			locationID = &params.LocationID
		}
		includeDetails = params.IncludeProductAndVariant != nil && *params.IncludeProductAndVariant
	}

	if includeDetails {
		return h.getInventoriesWithDetails(locationID, limit, offset)
	}
	return h.getInventoriesWithoutDetails(locationID, limit, offset)
}

func (h *InventoriesRepositoryHandler) getInventoriesWithoutDetails(locationID *string, limit, offset int) ([]*butik_domain.InventoryWithDetails, error) {
	const query = `
		SELECT id, product_variant_id, location_id, product_lot_id, quantity, created_at, updated_at
		FROM butiks_engine.inventories
		WHERE ($1::uuid IS NULL OR location_id = $1::uuid)
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := h.pool.Query(h.ctx, query, locationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying inventories: %w", err)
	}
	defer rows.Close()

	inventories := make([]*butik_domain.InventoryWithDetails, 0)
	for rows.Next() {
		var inv butik_domain.InventoryWithDetails
		if err := rows.Scan(
			&inv.ID,
			&inv.ProductVariantID,
			&inv.LocationID,
			&inv.ProductLotID,
			&inv.Quantity,
			&inv.CreatedAt,
			&inv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning inventory: %w", err)
		}
		inventories = append(inventories, &inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating inventories: %w", err)
	}

	return inventories, nil
}

func (h *InventoriesRepositoryHandler) getInventoriesWithDetails(locationID *string, limit, offset int) ([]*butik_domain.InventoryWithDetails, error) {
	const query = `
		SELECT
			i.id, i.product_variant_id, i.location_id, i.product_lot_id, i.quantity, i.created_at, i.updated_at,
			pv.id, pv.product_id, pv.sku, pv.price, pv.created_at, pv.updated_at,
			p.id, p.name, p.slug, p.description, p.created_at, p.updated_at
		FROM butiks_engine.inventories i
		JOIN butiks_engine.product_variants pv ON pv.id = i.product_variant_id
		JOIN butiks_engine.products p ON p.id = pv.product_id
		WHERE ($1::uuid IS NULL OR i.location_id = $1::uuid)
		ORDER BY i.created_at DESC, i.id DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := h.pool.Query(h.ctx, query, locationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying inventories with details: %w", err)
	}
	defer rows.Close()

	inventories := make([]*butik_domain.InventoryWithDetails, 0)
	for rows.Next() {
		var (
			inv butik_domain.InventoryWithDetails
			pv  butik_domain.ProductVariant
			p   butik_domain.Product
		)
		if err := rows.Scan(
			&inv.ID,
			&inv.ProductVariantID,
			&inv.LocationID,
			&inv.ProductLotID,
			&inv.Quantity,
			&inv.CreatedAt,
			&inv.UpdatedAt,
			&pv.ID,
			&pv.ProductID,
			&pv.SKU,
			&pv.Price,
			&pv.CreatedAt,
			&pv.UpdatedAt,
			&p.ID,
			&p.Name,
			&p.Slug,
			&p.Description,
			&p.CreatedAt,
			&p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning inventory with details: %w", err)
		}
		inv.ProductVariant = &pv
		inv.Product = &p
		inventories = append(inventories, &inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating inventories with details: %w", err)
	}

	return inventories, nil
}

func (h *InventoriesRepositoryHandler) CountInventories(params *CountInventoryParams) (int, error) {
	var locationID *string
	if params != nil && params.LocationID != "" {
		locationID = &params.LocationID
	}

	const query = `
		SELECT COUNT(*)
		FROM butiks_engine.inventories
		WHERE ($1::uuid IS NULL OR location_id = $1::uuid)
	`

	var count int
	if err := h.pool.QueryRow(h.ctx, query, locationID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting inventories: %w", err)
	}

	return count, nil
}

// resolveInventoriesPagination applies sane defaults and bounds to the
// optional pagination parameters.
func resolveInventoriesPagination(params *GetInventoriesParams) (limit, offset int) {
	limit = defaultInventoriesLimit
	offset = 0

	if params == nil {
		return limit, offset
	}

	if params.Limit != nil && *params.Limit > 0 {
		limit = min(*params.Limit, maxInventoriesLimit)
	}

	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}

	return limit, offset
}
