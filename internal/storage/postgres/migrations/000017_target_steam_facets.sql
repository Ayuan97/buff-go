-- Rust 出售搜索把 category_steamcat / category_itemclass 交给 Steam。
-- 空数组表示不限。求购 orderbook 没有对应参数。
ALTER TABLE collection_targets
    ADD COLUMN steam_cats text[] NOT NULL DEFAULT '{}',
    ADD COLUMN item_classes text[] NOT NULL DEFAULT '{}';

-- PostgreSQL 的 CHECK 不能写子查询，slug 合法性由领域层校验。
ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_steam_cats_shape CHECK (cardinality(steam_cats) <= 8),
    ADD CONSTRAINT collection_targets_item_classes_shape CHECK (cardinality(item_classes) <= 128);

-- Rust 搜索回的 type 是「创意工坊物品」，按商品名里最长的 item class 回填。
UPDATE steam_products AS product
SET item_type = matched.label
FROM (
    SELECT DISTINCT ON (candidate.product_id)
           candidate.product_id,
           class.label
    FROM steam_products AS candidate
    JOIN (
        VALUES
            ('Armored Metal Door'),
            ('Double Barrel Shotgun'),
            ('Semi Auto Pistol'),
            ('Semi Auto Rifle'),
            ('Waterpipe Shotgun'),
            ('Salvaged Icepick'),
            ('Satchel Explosives'),
            ('Concrete Barricade'),
            ('Sandbag Barricade'),
            ('Sheet Metal Door'),
            ('Large Wooden Box'),
            ('Metal Torso Plate'),
            ('Coffeecan Helmet'),
            ('Deer Skull Mask'),
            ('Roadsign Jacket'),
            ('Burlap Headwrap'),
            ('Burlap Trousers'),
            ('Hide Halterneck'),
            ('Rocket Launcher'),
            ('Salvaged Sword'),
            ('Pump Shotgun'),
            ('Stone Hatchet'),
            ('Stone Pickaxe'),
            ('Burlap Gloves'),
            ('Burlap Shirt'),
            ('Burlap Shoes'),
            ('Bucket Helmet'),
            ('Collared Shirt'),
            ('Metal Facemask'),
            ('Reactive Sign'),
            ('Rifle Helmet'),
            ('Roadsign Kilt'),
            ('Sleeping Bag'),
            ('Wooden Door'),
            ('Bolt Rifle'),
            ('Bone Club'),
            ('Bone Knife'),
            ('Hide Pants'),
            ('Hide Poncho'),
            ('Hide Skirt'),
            ('Long TShirt'),
            ('Miner''s Hat'),
            ('Snow Jacket'),
            ('Wooden Box'),
            ('Balaclava'),
            ('Longsword'),
            ('Revolver'),
            ('Tank Top'),
            ('Thompson'),
            ('Bandana'),
            ('Beenie'),
            ('Boonie'),
            ('Crossbow'),
            ('Grenade'),
            ('Hatchet'),
            ('Hoodie'),
            ('Jacket'),
            ('Shorts'),
            ('TShirt'),
            ('Boots'),
            ('Guitar'),
            ('Hammer'),
            ('Pants'),
            ('AK47u'),
            ('Cap'),
            ('Mp5'),
            ('Rock'),
            ('SMG')
    ) AS class(label)
      ON candidate.name ILIKE '%' || class.label || '%'
    WHERE candidate.appid = 252490
      AND (candidate.item_type IS NULL OR candidate.item_type = '创意工坊物品')
    ORDER BY candidate.product_id, char_length(class.label) DESC
) AS matched
WHERE product.product_id = matched.product_id;
