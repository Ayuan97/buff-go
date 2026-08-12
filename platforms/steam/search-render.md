# Steam market/search/render

## 用途与状态

该 GET 路径是商品目录和最低出售摘要的候选接口。当前状态为 `observed_once / http_429`：只确认一次从当前工作机直接出口发出的匿名请求收到 `429`，尚未取得成功响应，因此目录、出售、分页、身份和数量字段均未验证。

## 2026-08-11 首轮受控请求

| 项目 | 值 |
|---|---|
| 时间 | `2026-08-11T17:55:25Z` |
| 方法 | `GET` |
| Host | `steamcommunity.com` |
| Path | `/market/search/render/` |
| HTTP 条件 | HTTP/1.1；禁用 HTTP/2 和连接复用；请求后关闭连接 |
| User-Agent | `Mozilla/5.0` |
| Accept | `application/json, text/javascript` |
| Accept-Language | `zh-CN,zh;q=0.9,en;q=0.5` |
| X-Requested-With | `XMLHttpRequest` |
| Referer | Host `steamcommunity.com`；Path `/market/search`；Query `appid=252490` |
| 登录 Cookie | 未发送 |
| 出口 | 当前工作机直接出口；未使用环境代理；实际 IP 未读取、未记录 |
| 并发与重试 | 单并发，零重试，未翻页 |
| Query | `query=`、`start=0`、`count=5`、`search_descriptions=0`、`sort_column=price`、`sort_dir=asc`、`appid=252490`、`norender=1`、`currency=23`、`country=SG`、`language=schinese` |
| HTTP 状态 | `429` |
| Content-Type | `application/json` |
| Retry-After | 响应头中未出现 |
| 响应正文 | 按安全规则未读取、未保存 |

`currency=23`、`country=SG` 和 `language=schinese` 是本次请求条件，不是已经证明的币种、账号地区或字段语义。

## 停止行为

- 第一个请求收到 `429` 后立即终止整轮。
- 原计划的零结果搜索、登录 listing 页面、订单簿、listing render 和匿名历史价格请求均未执行。
- 本轮真实请求数为 1，认证请求数为 0。
- 没有主动重复请求以制造限频，也没有在缺少 `Retry-After` 时猜测恢复时间。

## 当前能够确认的事实

- 上述匿名搜索请求在该时刻收到 `429`。
- 该响应没有提供 `Retry-After`。
- 阈值、窗口、冷却时长、限制范围及是否与其他 Steam 接口共享预算全部未知。

## 仍未确认

- 成功响应、成功空集、分页字段和结束条件。
- `sell_price` 是否为最低出售、金额单位是否为人民币分、是否含买家手续费。
- `sell_price_text` 与 `sell_price` 的币种、单位和换算关系。
- `sell_price`、`sell_listings` 缺失与显式零分别表示什么；`sell_listings` 是订单条数、商品件数还是其他累计值。
- `name` 是展示名称还是身份字段，`commodity` 是否改变 listing 或详情行为。
- 顶层 `hash_name/appid` 与 `asset_description.market_hash_name/appid/classid` 的真实优先级和稳定性。
- 登录失效、403、验证码及自然恢复后的响应形态。

收到 `429` 后不通过切换出口或接口规避限制。使用者确认 Steam `429` 通常较快恢复，但这只是项目输入，不构成固定冷却时长；具体恢复时间和限制范围仍需受控复探确认。后续成功证据只能先标为“已观察”，需另一时段重复对账后才能升级为“已验证”。
