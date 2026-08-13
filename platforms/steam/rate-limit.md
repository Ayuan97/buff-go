# Steam 市场采集限流结论

日期：2026-08-12 起，续 2026-08-13。绑定：本机出口、`platforms/private/steam/cookie.md` 登录会话。  
证据：scratch 下 `steam_ratelimit_search_render.log`、`steam_ratelimit_other.log`、`steam_market_discovery.log`、`steam_ratelimit_20260813.log`、`steam_strategy_20260813.log`、`steam_protocol_v2.log`。  
**只引用上述 log 中可逐行核对的计数**；无 per-request log 的数字不写入。

## 一句话

- **search/render**：健康窗 **394×200** 后第一次 429（08:51:47）；30s 仍 429、**60s 200**；2.1s×3min **70×200**；再突发 **500×200 / 0×429**。v2f 孤立 **4862×200 / 36m2s / 0×429** 后 **302 login**（09:45:15）。重置后上限未打穿。  
- **orderbook Load**：v2c **2000×200 / 15m13s**；v2d **370×200**；v2e **1033×200**（09:00:36–09:08:02，进程死）皆 **0×429**。上限未打穿。  
- **匿名松桶**（均未打穿）：suggestions 80、listings_render 80、appfacets 80、search HTML 40，全 200。  
- **匿名紧桶**（scan 已 429）：search/render、priceoverview、pricehistory、popular、recentcompleted、appfilters、`/market/` HTML。priceoverview 恢复阶梯 2s→5min **9/9 仍 429**。  
- **登录会话**：08-13 中段 Cookie 一度 302 login；换新 Cookie 后 mylistings/orderbook **200**。`search/render` 换号仍 429 → 紧桶至少绑 IP。  
- **v2 健康窗**（`steam_protocol_v2.log`）：2.1s×5min 再突发 150。suggestions **116+150**、appfacets **113+150**、orderbook **118+150**，全 200、**0×429**。  
- **v2c**（同 log）：suggestions 0.5s×3min **188×200** + 突发 **1000×200**（08:20:52–08:29:18）；orderbook 背靠背 **2000×200**（08:29:18–08:44:31，约 15m13s）。全 200、**0×429**。cap 跑完被 abort，未进入 search/render 单探。  
- **v2d/v2e**：search/render 第一次 429 + 60s 恢复 + 2.1s 维持已有 log。重置后再突发 500 未 429。v2e 打 orderbook 至 1033 后进程退出。v2f 续跑。

## 信号形态

| 信号 | 观测 |
|---|---|
| 429 | JSON 极短或 HTML（`/market/`），**无 Retry-After** |
| 302 | Location 指向 login：会话失效，**不是**限流信号 |
| 403 | 账号页 path（activelistings 等），非本轮主采集限流 |
| 400 | 缺参（orderbook 无 qp、nameid=0 histogram） |
| 验证码 | **未观测** |

## 分接口

### market/search/render

| 项 | 结论（log 可核对） |
|---|---|
| 登录 200 | discovery **2**；08-13 恢复窗 **394**（08:48:13–08:51:47） |
| 登录 429 | 08-12 recovery/silent **22+**；08-13 早段 **27×429**；健康窗第一次 429：`v2d_search_render_b00288` **08:51:47** |
| 恢复 | 07:47:39→08:48:13 **200**（60m34s）；突发后 2–30s 429、**60s 200**（08:53:57） |
| 阶梯/突发 | 孤立 **287×200 / 2m33s** 后 429；确认 2.1s **70×200**；再突发 **500×200 / 0×429**；v2f **4862×200 / 36m2s / 0×429** 后 302 login |

### market/orderbook?q=Load

| 项 | 结论 |
|---|---|
| 登录 b2b | 08-12 **80/80 200**；08-13 登录 **200/200 200**；匿名 **800/800 200** |
| 登录并行 | 08-12 **40/40 200**（5×8） |
| 匿名并行 | 08-13 **500/500 200**（10×50） |
| 匿名 | 08-12 **1×200**；08-13 加压合计 **1300×200** |
| 缺参 | 08-13 仅 `q=Load` 无 `qp` → **400** |
| 最紧成功（匿名） | 背靠背+并行仍全成功，**未测到上限** |
| v2 登录 2.1s×5min+突发 | **118+150×200**（08:07:05–08:13:06） |
| v2c 登录背靠背 | **2000×200**（08:29:18–08:44:31，约 15m13s），**0×429** |
| 已测最紧包络 | 登录多段合计仍全 200（v2c 2000 + v2e 1033 等）；上限**未打穿** |

### 其它

| 接口 | 摘要 |
|---|---|
| appfacets | 登录 08-12 b2b 30×200；匿名 08-13 **80×200**；v2 登录 2.1s×5min+突发 **113+150×200**（08:00:06–08:06:44），**0×429** |
| suggestions | 匿名 08-13 **80×200**；v2 **116+150×200**；v2c 0.5s×3min **188×200** + 突发 **1000×200**（08:20:52–08:29:18），**0×429** |
| appfilters | 易 429；匿名 08-13 scan **429** |
| priceoverview / popular / recentcompleted / pricehistory | 匿名 08-13 scan **429**；priceoverview 2s–5min 阶梯 **9×429**；登录末次 `watchauth_priceoverview_01` **07:47:39Z 429** |
| listings_render | 匿名 08-13 **80×200**（大 HTML） |
| HTML search | 匿名 08-13 **40×200**；`/market/` HTML 匿名 **429** |

## 采集策略建议（非正式策略数字）

1. 目录：`search/render` 登录。长锁曾 **>19h**；本窗突发后 30s 仍 429、60s 恢复。不要 2s 轮询。  
2. 深度：优先 **orderbook Load + qp**；与 search 分桶；本窗未测到上限。  
3. 勿假设全站统一 QPS；按 path 分预算。  
4. 写接口未测，禁止当作采集通道。

## 未确认清单

- search/render 重置后突发上限（v2f **4862×200 / 36m2s** 未 429；随后 Cookie **302 login**）  
- orderbook 上限（登录已测 v2c **2000** + v2e **1033** 等段仍 0×429；v2f 时已 302）  
- appfacets / suggestions 第一次 429（v2f 时已 302）  
- priceoverview 长静默：07:47:39 429 → 09:45:16 仍 **429**（间隔 **1h57m**）；15/30/60min 单探进行中  
- 账号+IP 窗口（换新 Cookie 后 mylistings 200；search/render 仍 429 → 至少 IP 分量）  
- histogram 有效 nameid 路径  
- 多账号/多出口差异  
