ALTER TABLE collection_latest_pages
    ADD COLUMN switch_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT collection_latest_pages_switch_version_nonnegative
        CHECK (switch_version >= 0);

ALTER TABLE collection_latest_pages
    ALTER COLUMN switch_version DROP DEFAULT;
