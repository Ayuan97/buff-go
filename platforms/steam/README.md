# Steam 接口资料

## 当前状态

2026-08-12 / 08-13 完成 market/search 相关接口发现与限流加压（本出口、本 Cookie）。

采集主路径结论摘要：

| 接口 | 登录态最紧观察 | 匿名 | 共享预算 |
|---|---|---|---|
| `market/search/render` | 08-12 **2×200** 后长 429；08-13 过夜 ≈19h 仍 429（2s/5min 均无效） | 持续 429 | 与 orderbook **不共享** |
| `market/orderbook?q=Load` | 须 `qp`；08-13 背靠背 **200×200** 未 429 | 单次 200 | 与 search/render **不共享** |
| `market/appfacets/{appid}` | 背靠背 30 **全 200** | 未对照 | 与 search/render 不同窗（search 429 时 facets 可 200） |
| `market/priceoverview` 等 | 易 429 | 易 429 | 疑与部分只读 JSON 同属更紧桶（未确认精确键） |

**不能**把未打穿的上限写成固定平台 QPS。

**Goal 0B（2026-08-13）已验收**：search/render 身份键 `(appid, market_hash_name)`、`start/count` 分页、`¥` 两位小数与 `sell_price` 分、ask=`sell_price` 而非 `sale_price_text`；orderbook 登录 `eCurrency=23` 与 search 分对账、bid=`amtMaxBuyOrder`、ask=`amtMinSellOrder`、空买=`null`+空数组。详见 [search-render.md](./search-render.md)、[orderbook.md](./orderbook.md)。

## 接口全量表

状态：`已实请求` = 本窗发过 HTTP 并记状态码；`仅流量发现未请求` = SSR 路由/JS 出现但未发请求；`候选未观测` = 历史候选、本窗未在流量中确认。

| 接口 | 方法 | 用途候选 | 状态 | 文档 |
|---|---|---|---|---|
| `market/search` | GET | 搜索页 HTML（SSR 内嵌结果） | 已实请求 200 | — |
| [`market/search/render`](./search-render.md) | GET | 商品目录/出售摘要 JSON | 已实请求 | [search-render.md](./search-render.md) |
| [`market/orderbook` Load](./orderbook.md) | GET | 求购/出售深度（新 SSR RPC） | 已实请求 | [orderbook.md](./orderbook.md) |
| [`market/appfilters/{appid}`](./appfilters.md) | GET | 筛选元数据 JSON | 已实请求 | [appfilters.md](./appfilters.md) |
| [`market/appfacets/{appid}`](./appfacets.md) | GET | 分面筛选 JSON（新 UI） | 已实请求 | [appfacets.md](./appfacets.md) |
| [`market/priceoverview`](./priceoverview.md) | GET | 出售概览/成交量候选 | 已实请求（常 429） | [priceoverview.md](./priceoverview.md) |
| [`market/pricehistory`](./pricehistory.md) | GET | 历史价格 | 已实请求 | [pricehistory.md](./pricehistory.md) |
| `market/listings/{appid}/{name\|bucket}` | GET | listing HTML；新 UI 支持 `G…` bucket id | 已实请求 200 | — |
| `market/listings/.../render/` | GET | 出售 listing 分页候选；本窗返回大 HTML 非 JSON | 已实请求 | — |
| `market/searchsuggestionsresults` | GET | 搜索建议 | 已实请求 200 | — |
| `market/itemordershistogram` | GET | 经典订单簿（需 `item_nameid`） | 已实请求（nameid=0→400）；有效 nameid 未拿到 | — |
| `market/popular` | GET | 热门 | 已实请求 429 | — |
| `market/recentcompleted` | GET | 全市场近期成交 | 已实请求 429 | — |
| `market/myhistory/render` | GET | 本人成交历史 | 已实请求 200 | — |
| `market/mylistings` / `.../render` | GET | 本人在售 | 已实请求 200 | — |
| `market/advancedsearch` | GET | 高级搜索页 HTML | 已实请求 200 | — |
| `market/advancedsearchdata` | GET | 高级搜索数据 | 已实请求 400（缺参形态） | — |
| `market/newhome` / `zoo` / `zoo/browse` / `enhancedappearances` / `faq` / `discussions` / `multisell` | GET | 市场附属页 | 已实请求 200（页面） | — |
| `market/activelistings` / `buyorders` / `history` | GET | 账号侧列表页 | 已实请求 403 | — |
| `market/actions`（裸 GET） | GET | 需 RPC | 已实请求 400 | — |
| `market/userbillinginfo` | GET | 计费信息 | 仅流量发现未请求 | — |
| `market/getbuyorderstatus/` | GET | 查询求购单状态 | 仅流量发现未请求 | — |
| `market/createbuyorder/` 等写接口 | POST | 下单/撤单/改 listing | 仅流量发现未请求（不写） | — |
| `market/orderbook` mutation | POST | SSR mutationAction | 仅流量发现未请求 | — |

写接口（createbuyorder / cancelbuyorder / removelisting / buylisting / cancelalllistings / deleteitembucket）及只读 `getbuyorderstatus` 在 JS 中确认路径，**本 Goal 未对写接口发请求**；`getbuyorderstatus` 未实请求。

## SSR RPC 传输（新 UI）

来自 `ssr/Z9wBQ90a2.js`：

- **Query**：`GET {path}?q={Method}&qp={json_array_args}`，Header `x-valve-request-type: queryAction`（orderbook Load 实测无此头也可 200）
- **Mutation**：`POST {path}`，JSON body `{"m":Method,"mp":[...]}`，Header `x-valve-request-type: mutationAction`

示例：`GET /market/orderbook?q=Load&qp=[730,"AK-47 | Redline (Field-Tested)"]`

## 限流策略结论（有证据）

汇总见 [`rate-limit.md`](./rate-limit.md)。详情见各接口 md 与 scratch 日志（本机证据目录，不入库）：

- `steam_market_discovery.log`：发现路径 + 可达性状态码
- `steam_ratelimit_search_render.log`：search/render 恢复与加压
- `steam_ratelimit_other.log`：orderbook/appfacets 等与交叉实验

### 已确认

1. **匿名 vs 登录不是同一策略（search/render）**  
   - 登录：本 Goal log **2×200** 后长 429。  
   - 匿名：多次 429，无 `Retry-After`。
2. **429 形态**：HTTP 429，`Content-Type: application/json`，body 极短（约 24B），**无 `Retry-After`**；未见 403/验证码页作为本窗限流信号。
3. **接口预算隔离（部分）**  
   - search/render 处于 429 时：`orderbook` Load 仍可持续 200；交叉 orderbook=200、search/render=429。  
   - HTML `market/search` 在 search/render 429 时仍 200。  
   - `appfacets` 在 search/render 429 时仍可 200（后 b2b 30×200）。
4. **orderbook 本窗未打穿**  
   - 登录 b2b 80×200 + 并行 40×200；匿名 1×200。  
   - 「已测最紧包络内无 429」，**不是**无限流。
5. **search/render 冷却**  
   - 进入 429 后静默 5/10/15/30/45min 探针仍 429；累计 **≥70min** 未恢复。  
   - **不能**写固定 2s/3min/5min 公式。  
   - 本 Goal **无**冷启动阶梯/突发/并发成功加压 log（长期 429 无法补跑）。

### 未确认

- search/render 登录桶的精确容量、窗口长度与健康窗突发上限（本 Goal 无成功加压 log）。
- priceoverview / popular / recentcompleted 的独立桶参数。
- 有效 `item_nameid` 下 `itemordershistogram` 的可达与限流。
- 换 IP/账号后的差异。

## 发现边界

- 页面为 SSR React；搜索结果可内嵌在 HTML（`market_hash_name`、`market_bucket_group_id` 如 `G18D2253004`）。
- 未打开的交易确认弹层/支付深层 UI 可能还有 path；已标写接口为「仅流量发现未请求」。
- 经典 `economy_market.js` CDN 404；新逻辑在 `steamcommunity/public/ssr/*.js`。

## 安全

真实 Cookie 仅在被 Git 忽略的 `platforms/private/steam/cookie.md`。接口文档不含 Cookie 值、账号标识、完整响应正文、`Set-Cookie`。
