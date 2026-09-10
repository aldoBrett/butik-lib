CREATE TABLE
  IF NOT EXISTS butiks_engine.product_variants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    product_id UUID REFERENCES butiks_engine.products (id) ON DELETE CASCADE,
    sku TEXT NOT NULL,
    -- name TEXT NOT NULL,
    -- description TEXT,
    price NUMERIC(10, 2) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW (),
    updated_at TIMESTAMPTZ DEFAULT NOW ()
  );