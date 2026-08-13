# Steam market/orderbook（SSR Load）

## 用途与状态

新市场 UI 的订单深度候选（bid/ask 摘要与紧凑档位）。

- 用途：求购最高 / 出售最低及紧凑订单列表
- 状态：已实请求；**2026-08-13 Goal 0B 已验收金额与空数据**（见下文）

## 请求

| 项 | 值 |
|---|---|
| 方法 | GET（Query Action） |
| 路径 | `/market/orderbook` |
| Query | `q=Load` **且** `qp=<JSON 数组>`。例 `qp=[730,"AK-47 \| Redline (Field-Tested)"]`。08-13：仅 `q=Load` 无 `qp` → **400**、blen=0 |
| Header | 前端使用 `x-valve-request-type: queryAction`；本窗**去掉该头仍 200** |
| 登录 | 登录 200；**匿名本窗亦 200**（与 search/render 不同） |
| 裸 GET 无 q | 400（discovery） |

成功 JSON 顶层键（仅记结构名）：`success`, `data`。  
`data` 键：`amtMaxBuyOrder`, `amtMinSellOrder`, `eCurrency`, `cBuyOrders`, `cSellOrders`, `rgCompactBuyOrders`, `rgCompactSellOrders`。

## 2026-08-12 限流

| 阶段 | 次数 | 结果 |
|---|---|---|
| orderbook_b2b | 80 | 全 **200**，无 Retry-After |
| orderbook_parallel 5×8 | 40 | 全 **200** |
| cross 与 search/render 交替 | orderbook 10 | 全 **200**（同时 search 全 429） |
| orderbook_anon | 1 | **200** |

结论（本出口本窗）：

- 已测最紧（背靠背 + 5 并发）**未触发 429**。
- 与 `search/render` **不共享**限流预算。
- 匿名至少可通，**不能**推断匿名无限额。

## 2026-08-13

证据：`steam_ratelimit_20260813.log`。

| 阶段 | 次数 | 结果 |
|---|---|---|
| 无 qp | 1 | **400** |
| health + qp | 1 | **200**，blen=15083 |
| obburst 背靠背 | 200 | 全 **200**，约 81s，无 Retry-After |

与 search/render 同时：render 持续 429，orderbook 全 200 → 分桶。本窗仍**未打出上限**。

证据续：`steam_protocol_v2.log`。

| 阶段 | UTC | 次数 | 结果 |
|---|---|---|---|
| v2 leaky 2.1s×5min | 08:07:07–08:12:04 | 118 | 全 **200** |
| v2 突发 150 | 08:12:05–08:13:06 | 150 | 全 **200** |
| v2c 背靠背 cap=2000 | 08:29:18–08:44:31 | 2000 | 全 **200**，约 15m13s，无 Retry-After |

| v2d 背靠背 | 08:46:13–08:48:58 | 370 | 全 **200** |
| v2e 背靠背 | 09:00:36–09:08:02 | 1033 | 全 **200**，进程退出，**0×429** |

已测最紧登录包络：v2c 2000/15m13s + v2e 1033/7m26s 等段，均未 429。v2f 将继续加 cap。

## 2026-08-13 Goal 0B 字段验收

证据：登录 Cookie 对已在 search/render 出现的商品发 `q=Load&qp=[730, market_hash_name]`；匿名/坏 Cookie 对照。正文在 scratch，不入库。

### 成功形状

`success=true`，`data` 键：`amtMaxBuyOrder`, `amtMinSellOrder`, `eCurrency`, `cBuyOrders`, `cSellOrders`, `rgCompactBuyOrders`, `rgCompactSellOrders`。

`rgCompact*` 是 **扁平** `[price, qty, price, qty, …]`，不是对象数组。

### 人民币

登录钱包：`eCurrency=23`。同商品 search/render `sell_price=21` / `¥ 0.21` 时，orderbook `amtMinSellOrder=21`。  
匿名或去掉 `steamLoginSecure`：同一 Redline **仍 200**，但 `eCurrency=1`，`amtMinSellOrder`/`amtMaxBuyOrder` 换成另一套整数（美元分），**不能**当 CNY。

因此：只接受 **登录态 `eCurrency=23`** 的整数分；23 不是从枚举表猜的，而是本钱包与 `¥` 文本对账的结果。换号必须重验。

### ask / bid

| 方向 | 字段 | 本窗证明 |
|---|---|---|
| ask | `amtMinSellOrder` | 等于 `rgCompactSellOrders[0]`，卖档升序 |
| bid | `amtMaxBuyOrder` | 等于 `rgCompactBuyOrders[0]`，买档降序 |

Redline 登录：最高求购第一档 = `amtMaxBuyOrder`；最低出售第一档 = `amtMinSellOrder`。不是成交/median。

空买（廉价涂鸦）：`amtMaxBuyOrder=null`，`cBuyOrders=0`，`rgCompactBuyOrders=[]` → **empty**，不是失败。  
空卖：本窗热门商品均有卖档；结构与空买对称，未抓到真实空卖样本。  
伪造 `market_hash_name`：`{"success":false}` 无 `data` → **失败/不可用**，不是 empty。

无全市场 bid 列表。bid 采集按已有目录的 `market_hash_name` 逐个（或每页一批）打 orderbook。

### 登录失效

坏 Cookie / 无登录：orderbook **仍 200**（美元 `eCurrency=1`），**不是** 302。会话失效不能靠本 path 的 302 判断；CNY 闸门是 `eCurrency!=23` 则金额不得入库。

## 仍未确认

- 登录/匿名上限数值。
- 与 `itemordershistogram` 是否等价或可互相替代（histogram 缺有效 nameid）。
- `rgCompact*` 除第一档外的手续费口径（摘要只用最优档）。
