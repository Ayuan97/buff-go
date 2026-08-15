-- 有效价分值变动才记一条，挂单数变了不记。平台无关。
-- 订阅只决定看哪些序列，不决定记不记。
CREATE TABLE market_price_ticks (
    tick_id         BIGINT GENERATED ALWAYS AS IDENTITY,
    product_id      BIGINT NOT NULL,
    platform        TEXT COLLATE "C" NOT NULL,
    side            TEXT COLLATE "C" NOT NULL,
    prev_cents      BIGINT,
    price_cny_cents BIGINT NOT NULL,
    collected_at    TIMESTAMPTZ NOT NULL,
    switch_version  BIGINT NOT NULL,
    write_seq       BIGINT NOT NULL,
    CONSTRAINT market_price_ticks_pkey PRIMARY KEY (tick_id),
    CONSTRAINT market_price_ticks_product_fkey
        FOREIGN KEY (product_id) REFERENCES steam_products (product_id) ON DELETE RESTRICT,
    CONSTRAINT market_price_ticks_identity_key
        UNIQUE (product_id, platform, side, switch_version, write_seq),
    CONSTRAINT market_price_ticks_product_id_positive CHECK (product_id > 0),
    CONSTRAINT market_price_ticks_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT market_price_ticks_side_valid CHECK (side IN ('bid', 'ask')),
    CONSTRAINT market_price_ticks_price_nonnegative CHECK (price_cny_cents >= 0),
    CONSTRAINT market_price_ticks_prev_nonnegative CHECK (prev_cents IS NULL OR prev_cents >= 0),
    CONSTRAINT market_price_ticks_cents_changed
        CHECK (prev_cents IS NULL OR prev_cents IS DISTINCT FROM price_cny_cents),
    CONSTRAINT market_price_ticks_collected_at_finite CHECK (isfinite(collected_at)),
    CONSTRAINT market_price_ticks_switch_version_positive CHECK (switch_version > 0),
    CONSTRAINT market_price_ticks_write_seq_positive CHECK (write_seq > 0)
);

CREATE INDEX market_price_ticks_series_time_idx
    ON market_price_ticks (product_id, platform, side, collected_at DESC, tick_id DESC);

CREATE INDEX market_price_ticks_collected_at_idx
    ON market_price_ticks (collected_at);

CREATE TABLE market_price_watches (
    product_id BIGINT NOT NULL,
    platform   TEXT COLLATE "C" NOT NULL,
    side       TEXT COLLATE "C" NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT market_price_watches_pkey PRIMARY KEY (product_id, platform, side),
    CONSTRAINT market_price_watches_product_fkey
        FOREIGN KEY (product_id) REFERENCES steam_products (product_id) ON DELETE RESTRICT,
    CONSTRAINT market_price_watches_product_id_positive CHECK (product_id > 0),
    CONSTRAINT market_price_watches_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT market_price_watches_side_valid CHECK (side IN ('bid', 'ask')),
    CONSTRAINT market_price_watches_created_at_finite CHECK (isfinite(created_at))
);
