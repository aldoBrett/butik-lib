CREATE TABLE
  IF NOT EXISTS auth.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid (),
    role TEXT NOT NULL DEFAULT 'user',
    organization_id UUID REFERENCES auth.organizations (id) ON DELETE CASCADE,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW (),
    updated_at TIMESTAMPTZ DEFAULT NOW ()
  );