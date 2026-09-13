CREATE TABLE
  IF NOT EXISTS butiks_engine.order_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    order_id UUID NOT NULL REFERENCES butiks_engine.orders (id) ON DELETE CASCADE,
    product_variant_id UUID NOT NULL,
    location_id UUID NOT NULL REFERENCES butiks_engine.inventory_locations (id),
    quantity INT NOT NULL CHECK (quantity > 0),
    unit_price NUMERIC(12, 2) NOT NULL CHECK (unit_price >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now (),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now ()
  );