package butik_repository

import (
	"butik-lib/butik_domain"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type GetProductVariantsParams struct {
	// ProductID, when set, restricts the result to variants of that product.
	ProductID *string
	Offset    *int
	Limit     *int
}

const (
	defaultProductVariantsLimit = 50
	maxProductVariantsLimit     = 200
)

type ProductVariantsRepository interface {
	SaveProductVariant(variant *butik_domain.ProductVariant) error
	DeleteProductVariantByID(id string) error
	DeleteProductVariantsByProductID(productID string) error
	GetProductVariants(params *GetProductVariantsParams) ([]*butik_domain.ProductVariant, error)
	CountProductVariants(params *GetProductVariantsParams) (int, error)
}

type ProductVariantsRepositoryHandler struct {
	ctx  context.Context
	pool *pgxpool.Pool
	user *butik_domain.User
}

func NewProductVariantsRepositoryHandler(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *ProductVariantsRepositoryHandler {
	return &ProductVariantsRepositoryHandler{
		ctx:  ctx,
		pool: pool,
		user: user,
	}
}

func (h *ProductVariantsRepositoryHandler) DeleteProductVariantByID(id string) error {
	query := `DELETE FROM butiks_engine.product_variants WHERE id = $1`
	_, err := h.pool.Exec(h.ctx, query, id)
	if err != nil {
		return fmt.Errorf("deleting product variant by id: %w", err)
	}
	return nil
}

func (h *ProductVariantsRepositoryHandler) DeleteProductVariantsByProductID(productID string) error {
	query := `DELETE FROM butiks_engine.product_variants WHERE product_id = $1`
	_, err := h.pool.Exec(h.ctx, query, productID)
	if err != nil {
		return fmt.Errorf("deleting product variants by product id: %w", err)
	}
	return nil
}

func (h *ProductVariantsRepositoryHandler) SaveProductVariant(variant *butik_domain.ProductVariant) error {
	query := `
		INSERT INTO butiks_engine.product_variants (id, product_id, sku, price, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE
		SET product_id = EXCLUDED.product_id,
		    sku = EXCLUDED.sku,
		    price = EXCLUDED.price,
		    created_at = EXCLUDED.created_at,
		    updated_at = EXCLUDED.updated_at
	`

	_, err := h.pool.Exec(h.ctx, query,
		variant.ID,
		variant.ProductID,
		variant.SKU,
		variant.Price,
		variant.CreatedAt,
		variant.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("saving product variant: %w", err)
	}
	return nil
}

func (h *ProductVariantsRepositoryHandler) GetProductVariants(params *GetProductVariantsParams) ([]*butik_domain.ProductVariant, error) {
	limit, offset := resolveProductVariantsPagination(params)

	query := `
		SELECT id, product_id, sku, price, created_at, updated_at
		FROM butiks_engine.product_variants
	`

	args := make([]any, 0, 3)
	if productID := productVariantsProductFilter(params); productID != "" {
		args = append(args, productID)
		query += fmt.Sprintf("WHERE product_id = $%d\n", len(args))
	}

	args = append(args, limit, offset)
	query += fmt.Sprintf("ORDER BY created_at DESC, id DESC\nLIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := h.pool.Query(h.ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying product variants: %w", err)
	}
	defer rows.Close()

	variants := make([]*butik_domain.ProductVariant, 0)
	for rows.Next() {
		var v butik_domain.ProductVariant
		if err := rows.Scan(
			&v.ID,
			&v.ProductID,
			&v.SKU,
			&v.Price,
			&v.CreatedAt,
			&v.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning product variant: %w", err)
		}
		variants = append(variants, &v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating product variants: %w", err)
	}

	return variants, nil
}

func (h *ProductVariantsRepositoryHandler) CountProductVariants(params *GetProductVariantsParams) (int, error) {
	query := `SELECT COUNT(*) FROM butiks_engine.product_variants`

	args := make([]any, 0, 1)
	if productID := productVariantsProductFilter(params); productID != "" {
		args = append(args, productID)
		query += " WHERE product_id = $1"
	}

	var count int
	if err := h.pool.QueryRow(h.ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting product variants: %w", err)
	}

	return count, nil
}

// productVariantsProductFilter returns the product id to filter on, or "" when
// no filter was requested.
func productVariantsProductFilter(params *GetProductVariantsParams) string {
	if params == nil || params.ProductID == nil {
		return ""
	}
	return *params.ProductID
}

// resolveProductVariantsPagination applies sane defaults and bounds to the
// optional pagination parameters.
func resolveProductVariantsPagination(params *GetProductVariantsParams) (limit, offset int) {
	limit = defaultProductVariantsLimit
	offset = 0

	if params == nil {
		return limit, offset
	}

	if params.Limit != nil && *params.Limit > 0 {
		limit = min(*params.Limit, maxProductVariantsLimit)
	}

	if params.Offset != nil && *params.Offset > 0 {
		offset = *params.Offset
	}

	return limit, offset
}
