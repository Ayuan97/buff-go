-- 短效水位与代理商配置。真拉号后置，本迁移只存配置。
CREATE TABLE short_pool_watermarks (
    region                        TEXT COLLATE "C" NOT NULL,
    min_usable                    INTEGER NOT NULL,
    revision                      BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT short_pool_watermarks_pkey PRIMARY KEY (region),
    CONSTRAINT short_pool_watermarks_region_valid
        CHECK (region IN ('domestic', 'foreign', 'hongkong')),
    CONSTRAINT short_pool_watermarks_min_usable_nonneg CHECK (min_usable >= 0),
    CONSTRAINT short_pool_watermarks_revision_positive CHECK (revision > 0)
);

INSERT INTO short_pool_watermarks (region, min_usable) VALUES
    ('domestic', 0),
    ('foreign', 0),
    ('hongkong', 0);

CREATE TABLE proxy_providers (
    provider_id                   BIGINT GENERATED ALWAYS AS IDENTITY,
    name                          TEXT COLLATE "C" NOT NULL,
    enabled                       BOOLEAN NOT NULL DEFAULT TRUE,
    priority                      INTEGER NOT NULL,
    revision                      BIGINT NOT NULL DEFAULT 1,
    credential_plaintext          BYTEA NOT NULL,
    CONSTRAINT proxy_providers_pkey PRIMARY KEY (provider_id),
    CONSTRAINT proxy_providers_name_key UNIQUE (name),
    CONSTRAINT proxy_providers_provider_id_positive CHECK (provider_id > 0),
    CONSTRAINT proxy_providers_name_length CHECK (octet_length(name) BETWEEN 1 AND 128),
    CONSTRAINT proxy_providers_name_trimmed CHECK (name = btrim(name)),
    CONSTRAINT proxy_providers_name_no_control CHECK (name !~ '[[:cntrl:]]'),
    CONSTRAINT proxy_providers_priority_range CHECK (priority BETWEEN 1 AND 10000),
    CONSTRAINT proxy_providers_revision_positive CHECK (revision > 0),
    CONSTRAINT proxy_providers_credential_nonempty
        CHECK (octet_length(credential_plaintext) > 0)
);

CREATE TABLE proxy_provider_regions (
    provider_id                   BIGINT NOT NULL,
    region                        TEXT COLLATE "C" NOT NULL,
    CONSTRAINT proxy_provider_regions_pkey PRIMARY KEY (provider_id, region),
    CONSTRAINT proxy_provider_regions_provider_fkey
        FOREIGN KEY (provider_id) REFERENCES proxy_providers (provider_id)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    CONSTRAINT proxy_provider_regions_region_valid
        CHECK (region IN ('domestic', 'foreign', 'hongkong'))
);
