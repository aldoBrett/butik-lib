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
	GetProductByID(id string) (*butik_domain.Product, error)
	DeleteProductByID(id string) error
	SaveProduct(product *butik_domain.Product) error
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

func (h *ProductsRepositoryHandler) GetProductByID(id string) (*butik_domain.Product, error) {
	const query = `
		SELECT id, name, slug, description, created_at, updated_at
		FROM butiks_engine.products
		WHERE id = $1
	`

	var p butik_domain.Product
	if err := h.pool.QueryRow(h.ctx, query, id).Scan(
		&p.ID,
		&p.Name,
		&p.Slug,
		&p.Description,
		&p.CreatedAt,
		&p.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("querying product by id: %w", err)
	}

	return &p, nil
}

func (h *ProductsRepositoryHandler) CountProducts() (int, error) {
	const query = `SELECT COUNT(*) FROM butiks_engine.products`

	var count int
	if err := h.pool.QueryRow(h.ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting products: %w", err)
	}

	return count, nil
}

func (h *ProductsRepositoryHandler) DeleteProductByID(id string) error {
	const query = `
		DELETE FROM butiks_engine.products
		WHERE id = $1
	`

	_, err := h.pool.Exec(h.ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting product by id: %w", err)
	}

	return nil
}

// This method should upsert the product, inserting it if it doesn't exist or updating it if it does.
func (h *ProductsRepositoryHandler) SaveProduct(product *butik_domain.Product) error {
	const query = `
		INSERT INTO butiks_engine.products (id, name, slug, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			slug = EXCLUDED.slug,
			description = EXCLUDED.description,
			created_at = EXCLUDED.created_at,
			updated_at = EXCLUDED.updated_at
	`

	_, err := h.pool.Exec(
		h.ctx,
		query,
		product.ID,
		product.Name,
		product.Slug,
		product.Description,
		product.CreatedAt,
		product.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("saving product: %w", err)
	}
	return nil
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
