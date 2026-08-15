-- 出售搜索把价格区间交给 Steam search/render 的 price_min / price_max。
-- 空表示不限。换区间必须重开一轮，由 switch_version 推进，不在本迁移内表达。
ALTER TABLE collection_targets
    ADD COLUMN price_min_cents bigint,
    ADD COLUMN price_max_cents bigint;

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_price_range_valid CHECK (
        (price_min_cents IS NULL OR price_min_cents >= 0)
        AND (price_max_cents IS NULL OR price_max_cents >= 0)
        AND (price_min_cents IS NULL OR price_max_cents IS NULL OR price_min_cents <= price_max_cents)
    );
