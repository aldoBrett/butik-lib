package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type GetInventoryLocationsParams struct {
	Limit  *int
	Offset *int
}

const (
	defaultInventoryLocationsLimit = 50
	maxInventoryLocationsLimit     = 200
)

type InventoryLocationsRepository interface {
	SaveInventoryLocation(location *butik_domain.InventoryLocation) error
	GetInventoryLocationByID(id string) (*butik_domain.InventoryLocation, error)
	DeleteInventoryLocationByID(id string) error
	GetInventoryLocations(params *GetInventoryLocationsParams) ([]*butik_domain.InventoryLocation, error)
	CountInventoryLocations() (int, error)
}

type InventoryLocationsRepositoryHandler struct {
	ctx  context.Context
	pool *pgxpool.Pool
	user *butik_domain.User
}

func NewInventoryLocationsRepositoryHandler(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *InventoryLocationsRepositoryHandler {
	return &InventoryLocationsRepositoryHandler{
		ctx:  ctx,
		pool: pool,
		user: user,
	}
}

// SaveInventoryLocation upserts the location, inserting it if it doesn't
// exist or updating it if it does.
func (h *InventoryLocationsRepositoryHandler) SaveInventoryLocation(location *butik_domain.InventoryLocation) error {
	const query = `
		INSERT INTO butiks_engine.inventory_locations (id, name, code, address, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			code = EXCLUDED.code,
			address = EXCLUDED.address,
			is_active = EXCLUDED.is_active,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
	`

	_, err := h.pool.Exec(
		h.ctx,
		query,
		location.ID,
		location.Name,
		location.Code,
		location.Address,
		location.IsActive,
		location.CreatedAt,
		location.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("saving inventory location: %w", err)
	}
	return nil
}

func (h *InventoryLocationsRepositoryHandler) GetInventoryLocationByID(id string) (*butik_domain.InventoryLocation, error) {
	const query = `
		SELECT id, name, code, address, is_active, created_at, updated_at
		FROM butiks_engine.inventory_locations
		WHERE id = $1
	`

	var l butik_domain.InventoryLocation
	if err := h.pool.QueryRow(h.ctx, query, id).Scan(
		&l.ID,
		&l.Name,
		&l.Code,
		&l.Address,
		&l.IsActive,
		&l.CreatedAt,
		&l.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("querying inventory location by id: %w", err)
	}

	return &l, nil
}

func (h *InventoryLocationsRepositoryHandler) DeleteInventoryLocationByID(id string) error {
	const query = `
		DELETE FROM butiks_engine.inventory_locations
		WHERE id = $1
	`

	_, err := h.pool.Exec(h.ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting inventory location by id: %w", err)
	}

	return nil
}

func (h *InventoryLocationsRepositoryHandler) GetInventoryLocations(params *GetInventoryLocationsParams) ([]*butik_domain.InventoryLocation, error) {
	limit, offset := resolveInventoryLocationsPagination(params)

	const query = `
		SELECT id, name, code, address, is_active, created_at, updated_at
		FROM butiks_engine.inventory_locations
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := h.pool.Query(h.ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying inventory locations: %w", err)
	}
	defer rows.Close()

	locations := make([]*butik_domain.InventoryLocation, 0)
	for rows.Next() {
		var l butik_domain.InventoryLocation
		if err := rows.Scan(
			&l.ID,
			&l.Name,
			&l.Code,
			&l.Address,
			&l.IsActive,
			&l.CreatedAt,
			&l.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning inventory location: %w", err)
		}
		locations = append(locations, &l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating inventory locations: %w", err)
	}

	return locations, nil
}

func (h *InventoryLocationsRepositoryHandler) CountInventoryLocations() (int, error) {
	const query = `SELECT COUNT(*) FROM butiks_engine.inventory_locations`

	var count int
	if err := h.pool.QueryRow(h.ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting inventory locations: %w", err)
	}

	return count, nil
}

// resolveInventoryLocationsPagination applies sane defaults and bounds to the
// optional pagination parameters.
func resolveInventoryLocationsPagination(params *GetInventoryLocationsParams) (limit, offset int) {
	limit = defaultInventoryLocationsLimit
	offset = 0

	if params == nil {
		return limit, offset
	}

	if params.Limit != nil && *params.Limit > 0 {
		limit = min(*params.Limit, maxInventoryLocationsLimit)
	}

	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}

	return limit, offset
}
