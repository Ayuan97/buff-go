# Steam market/appfacets/{appid}

## 用途与状态

新 UI 分面筛选 JSON。已实请求。SSR queryKey：`["market","app_facets",appid]`。

## 请求

| 项 | 值 |
|---|---|
| 方法 | GET |
| 路径 | `/market/appfacets/730` |
| 登录 | 登录 200 |
| 限流头 | 无 Retry-After |

## 2026-08-12 限流

| 阶段 | 次数 | 结果 |
|---|---|---|
| discovery | 1 | 200 |
| search/render 429 窗内 | 1 | 200 |
| appfacets_b2b | 30 | 全 **200** |

结论：本窗背靠背 30 未 429；与 search/render 可同时处于不同状态。上限未确认。

## 2026-08-13

证据：`steam_protocol_v2.log`。

| 阶段 | UTC | 次数 | 结果 |
|---|---|---|---|
| v2 leaky 2.1s×5min + 突发 150 | 08:00:06–08:06:44 | 1+113+150=264 | 全 **200**，**0×429** |

已测最紧包络：登录 264×200 / 约 6m38s。上限未打穿。
