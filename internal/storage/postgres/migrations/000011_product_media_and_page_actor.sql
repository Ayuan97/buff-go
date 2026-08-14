-- 商品展示元数据与页归属。两者都可空：本迁移之前落库的商品没有图标，
-- 之前提交的页也没有记录用了哪个账号和出口。
ALTER TABLE steam_products
    ADD COLUMN icon_path  TEXT COLLATE "C",
    ADD COLUMN item_type  TEXT COLLATE "C",
    ADD COLUMN name_color TEXT COLLATE "C";

-- icon_path 是平台图片路径片段，展示时由前端拼上 CDN 前缀，库里不存整条 URL。
ALTER TABLE steam_products
    ADD CONSTRAINT steam_products_icon_path_shape CHECK (
        icon_path IS NULL OR
        (octet_length(icon_path) BETWEEN 1 AND 512 AND icon_path !~ '[[:space:][:cntrl:]/]')
    ),
    ADD CONSTRAINT steam_products_item_type_shape CHECK (
        item_type IS NULL OR (octet_length(item_type) BETWEEN 1 AND 128 AND item_type !~ '[[:cntrl:]]')
    ),
    -- 平台给的是不带井号的六位十六进制
    ADD CONSTRAINT steam_products_name_color_shape CHECK (
        name_color IS NULL OR name_color ~ '^[0-9a-fA-F]{6}$'
    );

ALTER TABLE collection_pages
    ADD COLUMN account_id   BIGINT,
    ADD COLUMN exit_address INET;

-- 归属是诊断信息：账号被删也不该抹掉采集历史，所以不加外键，只约束取值形状。
ALTER TABLE collection_pages
    ADD CONSTRAINT collection_pages_account_id_positive CHECK (
        account_id IS NULL OR account_id > 0
    ),
    ADD CONSTRAINT collection_pages_exit_host_address CHECK (
        exit_address IS NULL OR
        (family(exit_address) = 4 AND masklen(exit_address) = 32) OR
        (family(exit_address) = 6 AND masklen(exit_address) = 128)
    );

CREATE INDEX steam_products_appid_item_type
    ON steam_products (appid, item_type)
    WHERE item_type IS NOT NULL;
