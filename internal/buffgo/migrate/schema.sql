-- buffgo core schema (PostgreSQL)
-- Data model: docs/DATA_MODEL.md (L1 identity … L5 time series)

CREATE TABLE IF NOT EXISTS games (
    appid       BIGINT PRIMARY KEY,
    code        TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- L1: unified catalog item (Steam identity)
CREATE TABLE IF NOT EXISTS items (
    id                 BIGSERIAL PRIMARY KEY,
    appid              BIGINT NOT NULL REFERENCES games(appid),
    market_hash_name   TEXT NOT NULL,
    name               TEXT NOT NULL DEFAULT '',
    icon_url           TEXT NOT NULL DEFAULT '',
    steam_item_name_id TEXT NOT NULL DEFAULT '',
    classid            TEXT NOT NULL DEFAULT '',
    commodity          BOOLEAN NOT NULL DEFAULT FALSE,
    tags_json          JSONB NOT NULL DEFAULT '{}',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (appid, market_hash_name)
);

CREATE INDEX IF NOT EXISTS idx_items_appid ON items (appid);

-- L1: platform id → item mapping
CREATE TABLE IF NOT EXISTS platform_items (
    id                 BIGSERIAL PRIMARY KEY,
    item_id            BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    appid              BIGINT NOT NULL,
    platform           TEXT NOT NULL,
    platform_item_id   TEXT NOT NULL DEFAULT '',
    platform_name_raw  TEXT NOT NULL DEFAULT '',
    confidence         DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    status             TEXT NOT NULL DEFAULT 'active',
    meta               JSONB NOT NULL DEFAULT '{}',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (platform, appid, platform_item_id)
);

CREATE INDEX IF NOT EXISTS idx_platform_items_item ON platform_items (item_id);

-- L2: best bid/ask snapshot per platform (processor path)
-- side=sell → best ask (lowest sell); side=buy → best bid (highest buy)
CREATE TABLE IF NOT EXISTS quotes (
    id             BIGSERIAL PRIMARY KEY,
    item_id        BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    appid          BIGINT NOT NULL,
    platform       TEXT NOT NULL,
    side           TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    price          DOUBLE PRECISION NOT NULL DEFAULT 0,
    currency       TEXT NOT NULL DEFAULT '',
    quantity       INT NOT NULL DEFAULT 0,
    median_price   DOUBLE PRECISION,
    volume_24h     DOUBLE PRECISION,
    low_24h        DOUBLE PRECISION,
    high_24h       DOUBLE PRECISION,
    observed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source         TEXT NOT NULL DEFAULT '',
    source_meta    JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (item_id, platform, side)
);

CREATE INDEX IF NOT EXISTS idx_quotes_appid_side ON quotes (appid, side, observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_quotes_platform ON quotes (platform, appid, side);

-- L3: individual listings (Buff rows; optional Steam non-commodity)
CREATE TABLE IF NOT EXISTS platform_listings (
    id           BIGSERIAL PRIMARY KEY,
    item_id      BIGINT REFERENCES items(id) ON DELETE SET NULL,
    appid        BIGINT NOT NULL,
    platform     TEXT NOT NULL,
    side         TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    listing_id   TEXT NOT NULL,
    price        DOUBLE PRECISION NOT NULL DEFAULT 0,
    currency     TEXT NOT NULL DEFAULT '',
    quantity     INT NOT NULL DEFAULT 0,
    seller_key   TEXT NOT NULL DEFAULT '',
    fee          DOUBLE PRECISION,
    status       TEXT NOT NULL DEFAULT 'active',
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    batch_id     TEXT NOT NULL DEFAULT '',
    raw_meta     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (platform, appid, listing_id)
);

CREATE INDEX IF NOT EXISTS idx_platform_listings_item
    ON platform_listings (item_id, platform, side, status);
CREATE INDEX IF NOT EXISTS idx_platform_listings_batch
    ON platform_listings (platform, appid, batch_id);

-- L4: aggregated order-book levels (optional depth)
CREATE TABLE IF NOT EXISTS order_book_levels (
    id           BIGSERIAL PRIMARY KEY,
    item_id      BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    appid        BIGINT NOT NULL,
    platform     TEXT NOT NULL,
    side         TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
    price        DOUBLE PRECISION NOT NULL DEFAULT 0,
    quantity     INT NOT NULL DEFAULT 0,
    level_index  INT NOT NULL DEFAULT 0,
    observed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    batch_id     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_order_book_levels_lookup
    ON order_book_levels (item_id, platform, side, batch_id, level_index);

-- L5a: price history / trend buckets
CREATE TABLE IF NOT EXISTS price_points (
    id           BIGSERIAL PRIMARY KEY,
    item_id      BIGINT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    appid        BIGINT NOT NULL,
    platform     TEXT NOT NULL,
    bucket_at    TIMESTAMPTZ NOT NULL,
    price_avg    DOUBLE PRECISION NOT NULL DEFAULT 0,
    volume       DOUBLE PRECISION,
    price_open   DOUBLE PRECISION,
    price_high   DOUBLE PRECISION,
    price_low    DOUBLE PRECISION,
    price_close  DOUBLE PRECISION,
    currency     TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (item_id, platform, bucket_at, source)
);

CREATE INDEX IF NOT EXISTS idx_price_points_item_time
    ON price_points (item_id, platform, bucket_at DESC);

-- L5b: optional trade / completion events
CREATE TABLE IF NOT EXISTS trade_events (
    id                BIGSERIAL PRIMARY KEY,
    platform          TEXT NOT NULL,
    event_id          TEXT NOT NULL,
    item_id           BIGINT REFERENCES items(id) ON DELETE SET NULL,
    appid             BIGINT,
    market_hash_name  TEXT NOT NULL DEFAULT '',
    price             DOUBLE PRECISION NOT NULL DEFAULT 0,
    quantity          INT NOT NULL DEFAULT 0,
    currency          TEXT NOT NULL DEFAULT '',
    event_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source            TEXT NOT NULL DEFAULT '',
    raw_meta          JSONB NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (platform, event_id)
);

CREATE INDEX IF NOT EXISTS idx_trade_events_item_time
    ON trade_events (item_id, event_at DESC);

CREATE TABLE IF NOT EXISTS accounts (
    id                BIGSERIAL PRIMARY KEY,
    platform          TEXT NOT NULL,
    label             TEXT NOT NULL DEFAULT '',
    credentials       TEXT NOT NULL DEFAULT '',
    supported_appids  BIGINT[] NOT NULL DEFAULT '{}',
    status            TEXT NOT NULL DEFAULT 'active',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS proxies (
    id              BIGSERIAL PRIMARY KEY,
    endpoint        TEXT NOT NULL,
    auth            TEXT NOT NULL DEFAULT '',
    line_type       TEXT NOT NULL DEFAULT 'dual',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    only_appids     BIGINT[] NOT NULL DEFAULT '{}',
    prefer_appids   BIGINT[] NOT NULL DEFAULT '{}',
    max_concurrent  INT NOT NULL DEFAULT 1,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (endpoint)
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version     TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- seed known games (runtime monitoring still driven by config enabled_appids)
INSERT INTO games (appid, code, name, enabled)
VALUES (252490, 'rust', 'Rust', TRUE)
ON CONFLICT (appid) DO UPDATE SET
    code = EXCLUDED.code,
    name = EXCLUDED.name,
    enabled = EXCLUDED.enabled,
    updated_at = NOW();

INSERT INTO games (appid, code, name, enabled)
VALUES (730, 'cs2', 'Counter-Strike 2', TRUE)
ON CONFLICT (appid) DO UPDATE SET
    code = EXCLUDED.code,
    name = EXCLUDED.name,
    enabled = EXCLUDED.enabled,
    updated_at = NOW();
