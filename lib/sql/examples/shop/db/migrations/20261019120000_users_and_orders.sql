CREATE TABLE users (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    name        TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id),
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    -- SQLite keeps a DECIMAL as a 64-bit float, exact to 15 digits.
    total       DECIMAL(12, 2) NOT NULL,
    placed_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
