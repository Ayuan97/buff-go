-- Additive patches for installs that already applied 001_core.
-- Safe to re-run: IF NOT EXISTS / ADD COLUMN IF NOT EXISTS.

ALTER TABLE items ADD COLUMN IF NOT EXISTS classid TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN IF NOT EXISTS commodity BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE items ADD COLUMN IF NOT EXISTS tags_json JSONB NOT NULL DEFAULT '{}';

ALTER TABLE platform_items ADD COLUMN IF NOT EXISTS meta JSONB NOT NULL DEFAULT '{}';

ALTER TABLE quotes ADD COLUMN IF NOT EXISTS median_price DOUBLE PRECISION;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS volume_24h DOUBLE PRECISION;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS low_24h DOUBLE PRECISION;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS high_24h DOUBLE PRECISION;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';

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

CREATE INDEX IF NOT EXISTS idx_quotes_platform ON quotes (platform, appid, side);
