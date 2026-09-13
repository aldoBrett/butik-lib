package butik_domain

import "time"

type Product struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProductVariant struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	// Name        string    `json:"name"`
	// Description *string   `json:"description,omitempty"`
	SKU       string    `json:"sku"`
	Price     float64   `json:"price"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProductLot struct {
	ID               string     `json:"id"`
	ProductVariantID string     `json:"product_variant_id"`
	LotNumber        string     `json:"lot_number"`
	ManufacturedOn   *time.Time `json:"manufactured_on"`
	ExpiresOn        *time.Time `json:"expires_on"`
	IsBlocked        bool       `json:"is_blocked"`
	Quantity         int        `json:"quantity"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
