# Steam market/pricehistory

## 用途与状态

历史价格序列候选。已实请求。

## 请求

| 项 | 值 |
|---|---|
| 方法 | GET |
| 路径 | `/market/pricehistory/` |
| Query | `appid`, `market_hash_name` |
| 登录 | 登录可达 200；后可 429 |
| 限流头 | 无 Retry-After |

## 2026-08-12

| 场景 | 状态 | body_len |
|---|---|---|
| discovery 登录 | 200 | ≈189862 |
| 后续 429 窗 | 429 | 24 |

字段/币种未验收。限流上限未打穿结构。
