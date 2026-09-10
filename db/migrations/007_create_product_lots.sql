CREATE TABLE
  IF NOT EXISTS butiks_engine.product_lots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    product_variant_id UUID REFERENCES butiks_engine.product_variants (id) ON DELETE CASCADE,
    lot_number TEXT NOT NULL UNIQUE,
    manufactured_on DATE,
    expires_on DATE,
    is_blocked BOOLEAN DEFAULT FALSE,
    quantity INT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW (),
    updated_at TIMESTAMPTZ DEFAULT NOW (),
    UNIQUE (product_variant_id, lot_number),
    -- Used by the composite foreign key in inventory.
    UNIQUE (id, product_variant_id)
  );