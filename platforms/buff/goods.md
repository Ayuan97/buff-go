# BUFF 市场商品列表

- 用途：摘要目录；同一响应含出售最低价与求购最高价。ask 用 `sell_min_price`，bid 用 `buy_max_price`。
- 方法：GET `https://buff.163.com/api/market/goods`
- 线路：国内站；需要登录 Cookie。匿名 200 且 `code`/`error` 为登录要求。
- 入参（本窗）：`game=csgo`、`page_num`、`page_size`；空结果用无匹配 `search`。
- 分页：见 [README](./README.md)。游标应使用 **响应里的** `page_num`，下一页 `page_num+1`，`page_num >= total_page` 或 `total_count=0` 为结束。
- 身份：`id`（BUFF）、`market_hash_name`（Steam 关联）、`appid`。
- 价格：十进制元字符串。本窗求购冻结金额与 `ParseCNYCents` 对账。
- 数量：`sell_num`、`buy_num` 本窗为整数，语义未与订单簿逐档对总数。
- 空目录：`code=OK`，`total_count=0`，`items` 空。
- 禁止：`sell_reference_price`、`quick_price`、`goods_info.steam_price_cny`、`market_min_price`。
- 限流：未观测。
- 验证：2026-08-13 登录列表两页 + 空搜索 + 末页/越页；匿名登录要求。
