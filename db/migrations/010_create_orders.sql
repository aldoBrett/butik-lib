CREATE TABLE
  IF NOT EXISTS butiks_engine.orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    -- location_id UUID REFERENCES butiks_engine.locations(id),
    -- status TEXT NOT NULL,
    total_amount NUMERIC(10, 2) NOT NULL,
    total_price NUMERIC(10, 2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now (),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now ()
  );