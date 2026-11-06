CREATE TABLE products (
    id         SERIAL PRIMARY KEY,
    sku        TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    price      INTEGER NOT NULL CHECK (price >= 0), -- IDR, integer: no float money
    stock      INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0),
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE stock_movements (
    id         SERIAL PRIMARY KEY,
    product_id INTEGER NOT NULL REFERENCES products(id),
    type       TEXT NOT NULL CHECK (type IN ('in', 'out')),
    qty        INTEGER NOT NULL CHECK (qty > 0),
    note       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX stock_movements_product_recent_idx ON stock_movements (product_id, created_at DESC, id DESC);

CREATE TABLE sales_orders (
    id              SERIAL PRIMARY KEY,
    customer        TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'CREATED' CHECK (status IN ('CREATED', 'CANCELLED')),
    total           BIGINT NOT NULL CHECK (total >= 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancelled_at    TIMESTAMPTZ,
    idempotency_key TEXT UNIQUE,
    request_hash    TEXT
);

CREATE TABLE sales_order_items (
    id         SERIAL PRIMARY KEY,
    order_id   INTEGER NOT NULL REFERENCES sales_orders(id),
    sku        TEXT NOT NULL REFERENCES products(sku),
    qty        INTEGER NOT NULL CHECK (qty > 0),
    unit_price INTEGER NOT NULL CHECK (unit_price >= 0),
    UNIQUE (order_id, sku)
);
