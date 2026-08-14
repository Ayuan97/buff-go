# Steam market/search/render

## 用途与状态

`GET steamcommunity.com/market/search/render/` — 商品目录与最低出售摘要候选（ask 侧目录）。

- 字段语义/人民币/分页语义：**2026-08-13 Goal 0B 已验收**（见下文）
- 分页：query `start`/`count`；响应含 `start`、`pagesize`、`total_count`、`results`

## 请求

| 项 | 值 |
|---|---|
| 方法 | GET |
| 路径 | `/market/search/render/` |
| 登录 | 登录 Cookie 本窗可达 200；匿名本出口 429 |
| Query（实测） | `query`, `start`, `count`, `search_descriptions`, `sort_column`, `sort_dir`, `appid`, 可选 `norender=1` |
| 限流头 | 成功/429 均**无** `Retry-After` |

## 2026-08-12 实测（仅可核对日志）

证据：`steam_market_discovery.log`、`steam_ratelimit_search_render.log`、`steam_ratelimit_other.log`（交叉段）。  
**本 Goal scratch 日志中不存在**冷启动阶梯 / 长窗口持续 / 背靠背突发 / 并发等「逐请求 200」加压记录；不得引用无 log 的历史包络。

### 可达性（有 per-request 记录）

| 来源 | 时间(UTC) | 认证 | 状态 | 备注 |
|---|---|---|---|---|
| discovery | 09:50:37 | 登录 | **200** | `norender=1`，body_len≈8667 |
| discovery | 09:50:38 | 登录 | **200** | 无 norender，body_len≈24131 |
| discovery | 09:51:11 | 匿名 | **429** | 无 Retry-After，blen≈24 |
| ratelimit log | 09:53–10:59 | 登录 | **持续 429** | 见下表；无 Retry-After |
| ratelimit log | 09:57 / 10:13 | 匿名 | **429** | 对照 |
| other log 交叉 | ~10:14 | 登录 | **10×429** | 与 orderbook 交替时 search 侧 |

登录成功计数（本 Goal 可核对）：**2×200**（discovery）。  
登录 429 计数：recovery/silent/anon/cross 合计见 `steam_ratelimit_search_render.log`（约 22+ 条 status=429）+ other 交叉 10×429。

### 进入 429 后的恢复（per-request）

| 阶段 | 做法 | 结果 |
|---|---|---|
| recovery_15s | 12 次，间隔 15s | 全 429 |
| recovery_extra60 | +60s 后再 1 次 | 429 |
| silent_5m / 10m / 15m | 静默后各 1 探针 | 全 429 |
| silent_30m（10:14→10:44） | 静默 1800s 后 1 探针 | **429** |
| silent_45m（+900s→10:59） | 再静默 900s | **429** |
| 11:03 单探针 | — | 仍 **429** |

自 discovery 后登录持续 429 起（约 09:53）至 11:03，**累计 ≥70 分钟仍未恢复**（对 search/render 仅有限探针；orderbook 等其它 path 不恢复本 path）。

### 与 orderbook 交叉（search 429 窗内）

`steam_ratelimit_other.log`：`cross_orderbook` **10×200**，`cross_search_render` **10×429**。  
→ 与 orderbook **不共享**预算（本窗）。

### 本窗未完成项

- 冷启动阶梯间隔 / 持续节奏 / 背靠背 / 并发加压：因登录长期 429 **未能在本 Goal 日志中完成**（无 per-request 成功加压 log）。
- 若需打穿登录上限，须在 search/render 恢复 200 后另开观察窗重跑并落 per-request log。

## 能够确认的事实

- 登录态**至少**可成功 2 次（discovery 紧邻请求，间隔约 1s 量级均 200）。
- 登录态**可以**进入长时间 429（本窗 ≥70min 未观察到恢复）。
- 匿名本出口对 search/render 为 429，与登录「可先 200 再 429」不同。
- 429：短 JSON、**无 `Retry-After`**。
- **不能**写成固定「每 N 秒一次」或「冷却固定 5 分钟」。

## 2026-08-13

证据：`steam_ratelimit_20260813.log`。**早段**登录 search/render **0×200**（首包已 429）。

| 阶段 | UTC | 结果 |
|---|---|---|
| 日首次 | 06:20:35 | **429**（上次请求是 08-12 11:03，相隔约 19h，期间无量测） |
| 2s 轮询 | 06:20:37–06:21:23 | **20×429** |
| 静默 10/30/60/120s | 06:22:01–06:25:32 | **4×429** |
| 静默 5min | 06:31:07 | **429** |
| health 登录/匿名 | 同窗 | 均 **429**，blen=4，无 Retry-After |

同窗其它 path：`market/search` HTML **200**；`/market/` HTML **429**；orderbook+qp **200**。会话有效，仅 render JSON 仍锁。

冷却下界：静默 45min（10:14→10:59）仍 429；同日 07:47:39→08:48:13 **60m34s** 静默后恢复 200，即长锁恢复在 **45min–60m34s** 之间。
08-12 11:03→08-13 06:20 隔了约 19h 两端都是 429，但中间无量测，**不能当冷却上界**。

证据续：`steam_strategy_20260813.log`。换新 Cookie 后 `watchauth_search_render_01` **07:47:39Z 429**（登录，blen=4，无 Retry-After）。v2c abort 时 DIRTY 单探**未发出**。

证据：`steam_protocol_v2.log`。

| 阶段 | UTC | 结果 |
|---|---|---|
| 静默 ≥60min 后单发 | 08:48:13 | **200**（`v2d_render_s01_2m`，距 07:47:39 = **60m34s**，blen=8759） |
| 随后背靠背（与 orderbook 并行） | 08:48:13–08:48:58 | **106×200**（`v2d_search_render_b00001`–`b00106`），进程中断，**0×429** |
| 间隔约 17s 后孤立背靠背 | 08:49:15–08:51:47 | **287×200** 后 **1×429**（`b00287` 200 → `b00288` 429，耗时 **2m32.726s**，无 Retry-After） |
| 本健康窗合计 | 08:48:13–08:51:47 | 恢复 1 + 混合 106 + 孤立 287 = **394×200** 后第一次 429 |
| 静默 2s / 5s / 10s / 20s / 30s | 08:51:50 / 08:51:55 / 08:52:06 / 08:52:26 / 08:52:57 | 各 **429** |
| 静默 60s | 08:53:57 | **200**（`v2d_search_render_s06_60s`，距突发 429 08:51:47 = **2m10s** 墙钟） |

**287/394 不是稳定容量**（非空桶：429 前已有 107×200；重置后再突发 500 与 v2f 4862 均未 429）。不要写成固定 QPS。突发后恢复：2–30s 429，**60s 后 200**。

| 阶段 | UTC | 结果 |
|---|---|---|
| 确认 2.1s×3min | 08:54:00–08:56:55 | **70×200 / 0×429**（`v2_search_render_l001`–`l070`） |
| 再突发 cap=500 | 08:56:57–09:00:36 | **500×200 / 3m39s / 0×429** — 窗口已重置且 **>500** |
| v2f 孤立续打 | 09:09:14–09:45:15 | **4862×200** 后 **302 login**（`b04863`，耗时 **36m2s**），**0×429**。会话失效，不是限流。 |

## 2026-08-13 Goal 0B 字段验收

证据：本窗登录 Cookie 对 CS2（appid=730）`norender=1` 实请求；解析正文见 gitignored scratch `0b_analysis.json`。不把完整响应或 Cookie 写入本文。

### 成功 JSON 形状

顶层键：`success`, `start`, `pagesize`, `total_count`, `searchdata`, `results`。  
`results[]` 键：`name`, `hash_name`, `sell_listings`, `sell_price`, `sell_price_text`, `sale_price_text`, `app_icon`, `app_name`, `asset_description`。  
`asset_description` 含 `appid`, `classid`, `market_hash_name`, `market_name`, `name`, `market_bucket_group_id`, `market_bucket_group_name`。

本窗无 `item_nameid`。

### 身份键

- `hash_name` 与 `asset_description.market_hash_name` **逐字节相同**（英文 market hash）。
- 稳定采集键：**`(appid, market_hash_name)`**。orderbook `qp` 用的也是该英文名。
- `name` / `market_name` 随界面语言变化（本窗为繁体），**不能**当 upsert 键。
- `market_bucket_group_id` 更粗：涂鸦同图案不同颜色共用 group name，不能替代 hash_name。
- 两时段（同 query 间隔数秒）同一页 `hash_name` 列表一致。

### 分页

| 请求 | 响应 |
|---|---|
| `start=0,count=10` | `start=0,pagesize=10,total_count≈35234,results=10` |
| `start=10,count=10` | `start=10`，另一批 10 条，与第一页 hash **不重叠** |
| 无商品 query | `success=true,total_count=0,results=[]`（空目录，不是失败） |
| `start=35230,count=10` | `results=5`，`start+len(results)==total_count` → 末页 |
| `start=35234,count=10` | 仍可能返回最后 1 条；**不能**用 `start>=total_count` 当空页 |

游标：`CursorAfter = start + len(results)`。`Final`：`total_count==0` 或 `start+len(results) >= total_count`。  
`total_count` 在翻页期间可变（本窗 35234→35235）。

### 人民币与 ask

同商品一例：`sell_price=21`，`sell_price_text="¥ 0.21"`。去掉 `¥` 与空格后 `ParseCNYCents("0.21")==21`。  
`sale_price_text="¥ 0.14"` **更低**，是扣除 Steam 手续费后卖家所得，**禁止**当 ask。

当前最低出售 = `sell_price`（分）且必须与 `sell_price_text` 的 `¥` 两位小数对上。不是成交/median/参考价。  
本窗搜索结果均有 `sell_listings>0`；单商品空卖未在本接口出现（无在售商品不会进价格排序列表）。

`¥` 两位小数不是日元（日元通常无分）；匿名 orderbook 同商品改为 `eCurrency=1` 且整数不同，见 orderbook.md。

### 登录失效（采集 path）

- 健康登录突发中途掉会话：v2f `b04863` **302 login**（不是 429）。
- 本窗用无效 Cookie 直接打 render：与匿名相同 **429**、body `null`、无 Retry-After。

适配器：`302` 且 Location 指向 login → `session_invalid`；`429` → 限流。

## 仍未确认

- **287/394 不是稳定容量**；重置后突发上限未打穿（500 + v2f **4862×200 / 36m2s** 均未 429；随后 **302 login**）
- `count=100` 是否被 `pagesize` 完整接受（本窗只验了 count=10）
