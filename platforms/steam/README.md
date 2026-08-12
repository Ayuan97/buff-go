# Steam 接口资料

## 当前状态

Goal 0B 尚未完成。2026-08-11 的首轮受控复探在第一个匿名请求即收到 `429`，随后按安全边界停止；没有发送登录 Cookie，也没有继续请求商品详情、订单簿或历史价格。

| 接口 | 用途候选 | 证据状态 |
|---|---|---|
| [`market/search/render`](./search-render.md) | 商品目录、出售摘要 | 仅观察到首个匿名请求返回 `429`；成功结构和字段语义未验证 |
| `market/listings/{appid}/{market_hash_name}` | listing 页面、`item_nameid` 候选 | 未请求 |
| `market/itemordershistogram` | 求购、出售与深度候选 | 未请求 |
| `market/orderbook` | 求购、出售与 histogram 回退候选 | 未请求 |
| `market/listings/{appid}/{market_hash_name}/render` | 出售 listing 与费用候选 | 未请求 |
| `market/priceoverview` | 出售概览、币种与成交量候选 | 未请求 |
| `market/pricehistory` | 登录态历史价格候选 | 未请求 |
| `market/appfilters/{appid}` | 分类与筛选元数据候选 | 未请求 |
| `market/recentcompleted` | 全市场近期成交候选；不属于当前 bid/ask 核心 | 未请求 |

## 证据边界

- `testdata/steam_buy_sample.json` 只含 `sell_*` 字段，不能证明 Steam 求购接口。
- 现有样例只观察到美元样式文本，没有人民币、账号地区、手续费或登录失效证据。
- `classid` 在样例中并非每行都有；`item_nameid` 不在任何外部样例中。二者都不是已经确认的正式商品键。
- `hash_name`、`asset_description.market_hash_name` 和两处 `appid` 的优先级在旧解析器中不一致；真实响应对账完成前不能晋升为正式 Steam 商品身份。
- 旧代码中的搜索限频数字没有可复核的平台资料支撑，不能写入新限频策略。

真实 Cookie 位于被 Git 忽略的 `platforms/private/steam/cookie.md`，不属于接口证据文件。
