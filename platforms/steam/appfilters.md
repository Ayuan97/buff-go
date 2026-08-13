# Steam market/appfilters/{appid}

## 用途与状态

按 app 的市场筛选元数据候选。已实请求。

## 请求

| 项 | 值 |
|---|---|
| 方法 | GET |
| 路径 | `/market/appfilters/730`（appid 路径段） |
| 登录 | 登录 200（discovery） |
| 限流头 | 无 Retry-After |

## 2026-08-12

| 时间 | 状态 | 备注 |
|---|---|---|
| 09:50:39 登录 | 200 | body_len≈84049 |
| search/render 429 窗内单次 | 200→后见 429 | 与 search 不同步；后续再请求曾 429 |

字段语义未验收。限流桶边界未打穿。
