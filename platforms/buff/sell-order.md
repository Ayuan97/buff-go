# BUFF 出售订单

- 用途：核对列表 `sell_min_price` 是否为当前最低出售。**不是**摘要主采集 path。
- 方法：GET `https://buff.163.com/api/market/goods/sell_order`
- 本窗入参：`game=csgo`、`goods_id`、`page_num`、`page_size`。
- 登录：200，`code=OK`。匿名未单独打。
- 本窗第一档 `items[0].price` 与该商品列表 `sell_min_price` 相同（一件商品、一次时间窗）。
- `price` 为元字符串；`fee`/`income` 口径未验收。
- 限流：未观测。
- 验证：2026-08-13。
