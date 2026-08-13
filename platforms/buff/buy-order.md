# BUFF 求购订单

- 用途：核对列表 `buy_max_price` 是否为当前最高求购。**不是**摘要主采集 path。
- 方法：GET `https://buff.163.com/api/market/goods/buy_order`
- 本窗入参：`game=csgo`、`goods_id`、`page_num`、`page_size`。
- 登录：200，`code=OK`。
- 本窗第一档 `items[0].price="2.1"` 且 `frozen_amount="210"`，与列表 `buy_max_price` 一致。
- 限流：未观测。
- 验证：2026-08-13。
