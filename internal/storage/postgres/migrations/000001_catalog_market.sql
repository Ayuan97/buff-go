CREATE TABLE steam_products (
    product_id BIGINT GENERATED ALWAYS AS IDENTITY,
    appid      BIGINT NOT NULL,
    name       TEXT COLLATE "C" NOT NULL,
    CONSTRAINT steam_products_pkey PRIMARY KEY (product_id),
    CONSTRAINT steam_products_product_appid_key UNIQUE (product_id, appid),
    CONSTRAINT steam_products_product_id_positive CHECK (product_id > 0),
    CONSTRAINT steam_products_appid_positive CHECK (appid > 0),
    CONSTRAINT steam_products_name_nonempty CHECK (name <> '')
);

CREATE INDEX steam_products_appid_product_id_idx
    ON steam_products (appid, product_id);

CREATE TABLE platform_product_mappings (
    platform         TEXT COLLATE "C" NOT NULL,
    appid            BIGINT NOT NULL,
    platform_item_id TEXT COLLATE "C" NOT NULL,
    product_id       BIGINT NOT NULL,
    CONSTRAINT platform_product_mappings_pkey
        PRIMARY KEY (platform, appid, platform_item_id),
    CONSTRAINT platform_product_mappings_product_fkey
        FOREIGN KEY (product_id, appid)
        REFERENCES steam_products (product_id, appid)
        ON DELETE RESTRICT,
    CONSTRAINT platform_product_mappings_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT platform_product_mappings_appid_positive CHECK (appid > 0),
    CONSTRAINT platform_product_mappings_platform_item_id_nonempty CHECK (platform_item_id <> ''),
    CONSTRAINT platform_product_mappings_product_id_positive CHECK (product_id > 0)
);

CREATE TABLE market_latest_attempts (
    product_id     BIGINT NOT NULL,
    platform       TEXT COLLATE "C" NOT NULL,
    side           TEXT COLLATE "C" NOT NULL,
    status         TEXT COLLATE "C" NOT NULL,
    reason_code    TEXT COLLATE "C" NOT NULL DEFAULT '',
    source_time    TIMESTAMPTZ,
    collected_at   TIMESTAMPTZ NOT NULL,
    switch_version BIGINT NOT NULL,
    run_sequence   BIGINT NOT NULL,
    page_sequence  BIGINT NOT NULL,
    CONSTRAINT market_latest_attempts_pkey PRIMARY KEY (product_id, platform, side),
    CONSTRAINT market_latest_attempts_product_fkey
        FOREIGN KEY (product_id) REFERENCES steam_products (product_id) ON DELETE RESTRICT,
    CONSTRAINT market_latest_attempts_product_id_positive CHECK (product_id > 0),
    CONSTRAINT market_latest_attempts_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT market_latest_attempts_side_valid CHECK (side IN ('bid', 'ask')),
    CONSTRAINT market_latest_attempts_status_valid
        CHECK (status IN ('present', 'empty', 'unavailable', 'failed')),
    CONSTRAINT market_latest_attempts_reason_valid CHECK (
        (status IN ('present', 'empty') AND reason_code = '') OR
        (status IN ('unavailable', 'failed') AND reason_code ~ '^[a-z][a-z0-9_.-]{0,63}$')
    ),
    CONSTRAINT market_latest_attempts_source_time_finite
        CHECK (source_time IS NULL OR isfinite(source_time)),
    CONSTRAINT market_latest_attempts_collected_at_finite CHECK (isfinite(collected_at)),
    CONSTRAINT market_latest_attempts_switch_version_positive CHECK (switch_version > 0),
    CONSTRAINT market_latest_attempts_run_sequence_positive CHECK (run_sequence > 0),
    CONSTRAINT market_latest_attempts_page_sequence_positive CHECK (page_sequence > 0)
);

CREATE TABLE market_last_present (
    product_id     BIGINT NOT NULL,
    platform       TEXT COLLATE "C" NOT NULL,
    side           TEXT COLLATE "C" NOT NULL,
    price_cny_cents BIGINT NOT NULL,
    order_count    BIGINT,
    item_count     BIGINT,
    source_time    TIMESTAMPTZ,
    collected_at   TIMESTAMPTZ NOT NULL,
    switch_version BIGINT NOT NULL,
    run_sequence   BIGINT NOT NULL,
    page_sequence  BIGINT NOT NULL,
    CONSTRAINT market_last_present_pkey PRIMARY KEY (product_id, platform, side),
    CONSTRAINT market_last_present_product_fkey
        FOREIGN KEY (product_id) REFERENCES steam_products (product_id) ON DELETE RESTRICT,
    CONSTRAINT market_last_present_product_id_positive CHECK (product_id > 0),
    CONSTRAINT market_last_present_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT market_last_present_side_valid CHECK (side IN ('bid', 'ask')),
    CONSTRAINT market_last_present_price_nonnegative CHECK (price_cny_cents >= 0),
    CONSTRAINT market_last_present_order_count_nonnegative CHECK (order_count IS NULL OR order_count >= 0),
    CONSTRAINT market_last_present_item_count_nonnegative CHECK (item_count IS NULL OR item_count >= 0),
    CONSTRAINT market_last_present_source_time_finite
        CHECK (source_time IS NULL OR isfinite(source_time)),
    CONSTRAINT market_last_present_collected_at_finite CHECK (isfinite(collected_at)),
    CONSTRAINT market_last_present_switch_version_positive CHECK (switch_version > 0),
    CONSTRAINT market_last_present_run_sequence_positive CHECK (run_sequence > 0),
    CONSTRAINT market_last_present_page_sequence_positive CHECK (page_sequence > 0)
);
