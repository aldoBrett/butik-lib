CREATE TABLE
  IF NOT EXISTS butiks_engine.inventories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    product_variant_id UUID REFERENCES butiks_engine.product_variants (id) ON DELETE CASCADE,
    location_id UUID REFERENCES butiks_engine.inventory_locations (id) ON DELETE CASCADE,
    -- NULL for products managed without lot tracking.
    product_lot_id UUID REFERENCES butiks_engine.product_lots (id) ON DELETE CASCADE,
    quantity INT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW (),
    updated_at TIMESTAMPTZ DEFAULT NOW ()
  );
