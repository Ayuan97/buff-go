-- 采集顺序按目标配置。默认值与接入采集时的固定行为一致，所以已有行的采集
-- 结果不会因为这次迁移改变。游标是列表偏移量，换顺序必须重开一轮，那由
-- switch_version 推进来保证，不在本迁移内表达。
ALTER TABLE collection_targets
    ADD COLUMN sort_column TEXT COLLATE "C" NOT NULL DEFAULT 'price',
    ADD COLUMN sort_dir    TEXT COLLATE "C" NOT NULL DEFAULT 'asc';

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_sort_column_valid
        CHECK (sort_column IN ('price', 'quantity', 'name')),
    ADD CONSTRAINT collection_targets_sort_dir_valid
        CHECK (sort_dir IN ('asc', 'desc'));
