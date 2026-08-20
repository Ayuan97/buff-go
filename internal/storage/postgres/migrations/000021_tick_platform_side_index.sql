-- 加速按平台和方向读取最新变价。
CREATE INDEX market_price_ticks_platform_side_time_idx
    ON market_price_ticks (platform, side, collected_at DESC, tick_id DESC);
