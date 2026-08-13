# BUFF 接口资料

## 当前状态

2026-08-13 对 `buff.163.com` 登录态做了 **Goal 11A 部分实测**。匿名请求返回 `Login Required`。

**结论：列表字段形状已看到，限流、Rust 的 `game` 码、空卖/空买、手续费口径未验收。按计划不写生产适配器。**

| 接口 | 登录 200 | 匿名 | 用途 |
|---|---|---|---|
| [`/api/market/goods`](./goods.md) | 是 | `code=Login Required` | 目录 + 出售最低价 + 求购最高价（同一页） |
| [`/api/market/goods/sell_order`](./sell-order.md) | 是 | 未单独压 | 用来对账列表 `sell_min_price`，不是摘要主 path |
| [`/api/market/goods/buy_order`](./buy-order.md) | 是 | 未单独压 | 用来对账列表 `buy_max_price`，不是摘要主 path |

未测：429/验证码、坏 Cookie 与匿名是否同一形态、`game=rust`（本窗 `total_count=0`）、详情深度。

## 已验证（本窗、CS2/`game=csgo`）

1. 身份：`data.items[].id` 为 BUFF 商品 ID；`market_hash_name` 与 Steam 英文 hash 一致；`appid` 在条目上为 730。`name`/`short_name` 随语言，不能当 upsert 键。
2. 分页：`page_num` 从 1 起；回显 `page_num/page_size/total_count/total_page`。空搜索 `total_count=0` 且 `items` 为空。末页 `page_num==total_page` 可少于 `page_size`。请求超过末页时 **仍回末页数据**（本窗 1794 → 回 1793），不能把「请求页码 > total_page」当成空页。
3. 人民币：求购单 `price="2.1"` 与 `frozen_amount="210"` 对得上 `ParseCNYCents("2.1")==210`。金额是元的十进制字符串，不是 Steam 分。
4. ask：`sell_min_price` 与该 `goods_id` 的 `sell_order` 第一档 `price` 一致（本窗一件商品）。**禁止** `sell_reference_price` / `quick_price` / `steam_price_cny`。
5. bid：`buy_max_price` 与该 `goods_id` 的 `buy_order` 第一档 `price` 一致（本窗一件商品）。不得与 ask 混用。
6. 匿名：同一 goods 接口 200 + `error=请先登录`，不是 302。

## 未验证（卡住 11B）

- 限流：未打到 429，不能写 QPS 或冷却数字。
- 空卖/空买：未找到 `sell_num=0` / `buy_num=0` 的对账件。
- `fee`/`income` 是否含费；列表价是否等于买家实付。
- 非 CS2 的 `game` 查询词。
- 坏 Cookie 是登录错误还是匿名同形。

证据正文在 gitignore 的 `platforms/private/buff/scratch/`。
