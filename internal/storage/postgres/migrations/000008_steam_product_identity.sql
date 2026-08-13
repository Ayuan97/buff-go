-- Goal 0B: Steam catalog identity is (appid, market_hash_name).
CREATE UNIQUE INDEX steam_products_appid_name_key ON steam_products (appid, name);
