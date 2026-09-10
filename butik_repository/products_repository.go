package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type GetProductsParams struct {
	// OrganizationID *string // TODO: should we add it?
	Offset *int
	Limit  *int
}

const (
	defaultProductsLimit = 50
	maxProductsLimit     = 200
)

type ProductsRepository interface {
	GetProducts(params *GetProductsParams) ([]*butik_domain.Product, error)
	CountProducts() (int, error)
}

type ProductsRepositoryHandler struct {
	ctx  context.Context
	pool *pgxpool.Pool
	user *butik_domain.User
}

func NewProductsRepositoryHandler(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *ProductsRepositoryHandler {
	return &ProductsRepositoryHandler{
		ctx:  ctx,
		pool: pool,
		user: user,
	}
}

func (h *ProductsRepositoryHandler) GetProducts(params *GetProductsParams) ([]*butik_domain.Product, error) {
	limit, offset := resolveProductsPagination(params)

	const query = `
		SELECT id, name, slug, description, created_at, updated_at
		FROM butiks_engine.products
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := h.pool.Query(h.ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("querying products: %w", err)
	}
	defer rows.Close()

	products := make([]*butik_domain.Product, 0)
	for rows.Next() {
		var p butik_domain.Product
		if err := rows.Scan(
			&p.ID,
			&p.Name,
			&p.Slug,
			&p.Description,
			&p.CreatedAt,
			&p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning product: %w", err)
		}
		products = append(products, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating products: %w", err)
	}

	return products, nil
}

func (h *ProductsRepositoryHandler) CountProducts() (int, error) {
	const query = `SELECT COUNT(*) FROM butiks_engine.products`

	var count int
	if err := h.pool.QueryRow(h.ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting products: %w", err)
	}

	return count, nil
}

// resolveProductsPagination applies sane defaults and bounds to the optional
// pagination parameters.
func resolveProductsPagination(params *GetProductsParams) (limit, offset int) {
	limit = defaultProductsLimit
	offset = 0

	if params == nil {
		return limit, offset
	}

	if params.Limit != nil && *params.Limit > 0 {
		limit = min(*params.Limit, maxProductsLimit)
	}

	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}

	return limit, offset
}
