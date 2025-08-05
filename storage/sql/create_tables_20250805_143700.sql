-- ========================================
-- 数据库表创建SQL文件
-- 生成时间: 2025-08-05 14:37:00
-- 项目: buff-go
-- 说明: 根据Go模型自动生成的数据库表结构
-- ========================================

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;


-- order 表
CREATE TABLE IF NOT EXISTS `order` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `name` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '商品名称',
    `market_hash_name` VARCHAR(255) NOT NULL DEFAULT '',
    `buff_order_id` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'ID',
    `buff_goods_id` INT NOT NULL DEFAULT 0 COMMENT 'ID',
    `buff_crt_time` INT NOT NULL DEFAULT 0 COMMENT '时间',
    `buff_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `buff_pay_methods` VARCHAR(255) NOT NULL DEFAULT '',
    `buff_user_id` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'ID',
    `buff_user_nickname` VARCHAR(100) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ip 表
CREATE TABLE IF NOT EXISTS `ip` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `ip` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'IP地址',
    `type` INT NOT NULL DEFAULT 1 COMMENT '类型',
    `name` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '商品名称',
    `pass_word` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '密码',
    `proxy_type` INT NOT NULL DEFAULT 1,
    `country` INT NOT NULL DEFAULT 0 COMMENT '国家',
    `is_https` VARCHAR(255) NOT NULL DEFAULT '',
    `speed` INT NOT NULL DEFAULT 0 COMMENT '速度',
    `source` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '来源',
    `port` INT NOT NULL DEFAULT 0 COMMENT '端口'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- steam_user 表
CREATE TABLE IF NOT EXISTS `steam_user` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `session_id` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '会话ID',
    `account` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '账号',
    `password` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '密码',
    `status` INT NOT NULL DEFAULT 0 COMMENT '状态',
    `type` INT NOT NULL DEFAULT 1 COMMENT '类型',
    `steam_country` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '数量',
    `browser_id` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'ID',
    `steam_login_secure` VARCHAR(255) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- system 表
CREATE TABLE IF NOT EXISTS `system` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `system_type` BIGINT NOT NULL DEFAULT 1
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- config 表
CREATE TABLE IF NOT EXISTS `config` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `buff_buy_status` INT NOT NULL DEFAULT 0 COMMENT '状态',
    `buff_sell_status` INT NOT NULL DEFAULT 0 COMMENT '状态',
    `steam_buy_status` INT NOT NULL DEFAULT 0 COMMENT '状态',
    `steam_sell_status` INT NOT NULL DEFAULT 0 COMMENT '状态',
    `buff_buy_delay` INT NOT NULL DEFAULT 0,
    `buff_sell_delay` INT NOT NULL DEFAULT 0,
    `steam_buy_delay` INT NOT NULL DEFAULT 0,
    `steam_sell_delay` INT NOT NULL DEFAULT 0,
    `bot_filter` VARCHAR(255) NOT NULL DEFAULT '',
    `buff_page_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `steam_page_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `bot_buff_proportion` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    `bot_steam_proportion` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    `bot_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `min_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `max_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- goods 表
CREATE TABLE IF NOT EXISTS `goods` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `appid` INT NOT NULL DEFAULT 0 COMMENT '应用ID',
    `buy_max_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `buy_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `game` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '游戏名称',
    `name` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '商品名称',
    `market_hash_name` VARCHAR(255) NOT NULL DEFAULT '',
    `short_name` VARCHAR(100) NOT NULL DEFAULT '',
    `goods_id` INT NOT NULL UNIQUE DEFAULT 0 COMMENT 'ID',
    `icon_url` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '链接地址',
    `steam_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `steam_price_cny` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `quick_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `sell_min_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `sell_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `sell_reference_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `steam_market_url` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '链接地址',
    `transacted_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `steam_item_name_id` VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'ID',
    `steam_sell_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `highest_buy_order` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    `lowest_sell_order` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    `proportion` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '比例',
    `steam_update` BIGINT NOT NULL DEFAULT 0 COMMENT '时间',
    `buff_update` BIGINT NOT NULL DEFAULT 0 COMMENT '时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_goods_goods_id ON goods (`goods_id`);

-- info 表
CREATE TABLE IF NOT EXISTS `info` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `appid` INT NOT NULL DEFAULT 0 COMMENT '应用ID',
    `buff_buy_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `buff_buy_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `buff_sell_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `buff_sell_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `steam_buy_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `steam_buy_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `steam_sell_price` DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '价格',
    `steam_sell_num` INT NOT NULL DEFAULT 0 COMMENT '数量',
    `game` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '游戏名称',
    `name` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '商品名称',
    `market_hash_name` VARCHAR(255) NOT NULL DEFAULT '',
    `goods_id` INT NOT NULL UNIQUE DEFAULT 0 COMMENT 'ID',
    `icon_url` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '链接地址',
    `steam_item_name_id` VARCHAR(100) NOT NULL DEFAULT '' COMMENT 'ID',
    `is_push` INT NOT NULL DEFAULT 0,
    `is_buy` INT NOT NULL DEFAULT 0,
    `buff_proportion` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    `steam_proportion` DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    `buff_buy_update` INT NOT NULL DEFAULT 0 COMMENT '时间',
    `buff_sell_update` INT NOT NULL DEFAULT 0 COMMENT '时间',
    `steam_buy_update` INT NOT NULL DEFAULT 0 COMMENT '时间',
    `steam_sell_update` INT NOT NULL DEFAULT 0 COMMENT '时间'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE UNIQUE INDEX idx_info_goods_id ON info (`goods_id`);

-- buff_user 表
CREATE TABLE IF NOT EXISTS `buff_user` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `sessionid` VARCHAR(500) NOT NULL DEFAULT '' COMMENT '会话ID',
    `account` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '账号',
    `password` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '密码',
    `device_id` VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'ID',
    `csrf_token` VARCHAR(500) NOT NULL DEFAULT '',
    `remember_me` VARCHAR(255) NOT NULL DEFAULT '',
    `status` INT NOT NULL DEFAULT 0 COMMENT '状态'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- user 表
CREATE TABLE IF NOT EXISTS `user` (
    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID',
    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间',
    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间',
    `nickname` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '昵称',
    `username` VARCHAR(100) NOT NULL DEFAULT '' COMMENT '用户名',
    `phone` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '手机号',
    `password` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '密码',
    `salt` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '盐值',
    `status` INT NOT NULL DEFAULT 0 COMMENT '状态',
    `avatar` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '头像',
    `balance` BIGINT NOT NULL DEFAULT 0 COMMENT '余额',
    `is_admin` TINYINT(1) NOT NULL DEFAULT FALSE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 额外的索引和约束
CREATE INDEX idx_goods_appid ON goods (`appid`);
CREATE INDEX idx_goods_game ON goods (`game`);
CREATE INDEX idx_goods_market_hash_name ON goods (`market_hash_name`);
CREATE INDEX idx_info_appid ON info (`appid`);
CREATE INDEX idx_info_market_hash_name ON info (`market_hash_name`);
CREATE INDEX idx_order_buff_goods_id ON order (`buff_goods_id`);
CREATE INDEX idx_order_buff_user_id ON order (`buff_user_id`);
CREATE INDEX idx_ip_type ON ip (`type`);
CREATE INDEX idx_ip_country ON ip (`country`);
CREATE INDEX idx_buff_user_status ON buff_user (`status`);
CREATE INDEX idx_steam_user_status ON steam_user (`status`);
CREATE INDEX idx_user_username ON user (`username`);
CREATE INDEX idx_user_phone ON user (`phone`);
CREATE INDEX idx_user_status ON user (`status`);

SET FOREIGN_KEY_CHECKS = 1;

-- ========================================
-- SQL文件生成完成
-- ========================================