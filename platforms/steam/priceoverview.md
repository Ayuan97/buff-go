# Steam market/priceoverview

## 用途与状态

出售概览 / 币种 / 成交量候选。已实请求；本窗易 429。

## 请求

| 项 | 值 |
|---|---|
| 方法 | GET |
| 路径 | `/market/priceoverview/` |
| Query | `appid`, `currency`, `market_hash_name` |
| 登录 | 登录仍可能 429 |
| 限流头 | 无 Retry-After |

## 2026-08-12

| 场景 | 状态 |
|---|---|
| discovery 登录 | 429（约 24B JSON） |
| search/render 429 窗内再探 | 429 |

字段未验收。精确限流参数未确认。

## 2026-08-13

证据：`steam_strategy_20260813.log`。匿名 watch 07:01–07:31 **4×429**；登录 `watchauth_priceoverview_01` **07:47:39Z 429**（blen=4，无 Retry-After）。

证据续：`steam_protocol_v2.log`。blen=4，无 Retry-After。

| 探针 | UTC | 间隔 | 结果 |
|---|---|---|---|
| s01_0m | 09:45:16 | 距 07:47:39 = **1h57m** | **429** |
| s02_15m | 10:00:16 | **15min** | **429** |
| s03_30m | 进行中 | 30min | — |
