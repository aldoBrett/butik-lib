package butik_domain

import "time"

type InventoryLocation struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Code      string    `json:"code"`
	Address   string    `json:"address"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Inventory struct {
	ID               string `json:"id"`
	ProductVariantID string `json:"product_variant_id"`
	LocationID       string `json:"location_id"`
	// ProductLotID is nil for inventory managed without lot tracking.
	ProductLotID *string   `json:"product_lot_id,omitempty"`
	Quantity     int       `json:"quantity"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// InventoryWithDetails extends Inventory with its related product and
// product variant, populated when callers ask GetInventories to include
// them so an index view can display them without extra lookups.
type InventoryWithDetails struct {
	Inventory
	Product        *Product        `json:"product,omitempty"`
	ProductVariant *ProductVariant `json:"product_variant,omitempty"`
}

type InventoryMovement struct {
	ID          string `json:"id"`
	InventoryID string `json:"inventory_id"`
	// FromLocationID   string    `json:"from_location_id"`
	// ToLocationID     string    `json:"to_location_id"`
	Quantity     int       `json:"quantity"`
	MovementType string    `json:"movement_type"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
