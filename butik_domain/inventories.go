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

type Order struct {
	ID          string    `json:"id"`
	TotalAmount float64   `json:"total_amount"`
	TotalPrice  float64   `json:"total_price"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type OrderItem struct {
	ID               string `json:"id"`
	OrderID          string `json:"order_id"`
	ProductVariantID string `json:"product_variant_id"`
	// LocationID identifies which inventory location fulfills this line
	// item, since a product variant can have separate inventory rows per
	// location.
	LocationID string    `json:"location_id"`
	Quantity   int       `json:"quantity"`
	UnitPrice  float64   `json:"unit_price"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type OrderWithOrderItems struct {
	Order      Order       `json:"order"`
	OrderItems []OrderItem `json:"order_items"`
}
