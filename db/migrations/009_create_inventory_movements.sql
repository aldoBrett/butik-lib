CREATE TABLE
  IF NOT EXISTS butiks_engine.inventory_movements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    inventory_id UUID REFERENCES butiks_engine.inventories (id) ON DELETE CASCADE,
    quantity INT NOT NULL,
    -- TODO: make the catalog of movement types. Right now we're going to leave it
    -- TODO: open until we arrive to something stable.
    movement_type TEXT NOT NULL,
    -- TODO: operation id, or order_id, let's see which route we take.
    created_at TIMESTAMPTZ DEFAULT NOW (),
    updated_at TIMESTAMPTZ DEFAULT NOW ()
  );
