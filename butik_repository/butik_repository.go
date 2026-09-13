package butik_repository

import (
	"butik-lib/butik_domain"
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repositories struct {
	ProductVariants    ProductVariantsRepository
	Products           ProductsRepository
	InventoryLocations InventoryLocationsRepository
}

func NewRepositories(ctx context.Context, pool *pgxpool.Pool, user *butik_domain.User) *Repositories {
	return &Repositories{
		ProductVariants:    NewProductVariantsRepositoryHandler(ctx, pool, user),
		Products:           NewProductsRepositoryHandler(ctx, pool, user),
		InventoryLocations: NewInventoryLocationsRepositoryHandler(ctx, pool, user),
	}
}
