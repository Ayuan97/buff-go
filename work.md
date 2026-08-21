# work

## 执行规则

1. README 定义产品；ARCHITECTURE 定义目标模块；REFERENCE 定义统一语义；CONSOLE 定义 Web 操作流程；`platforms/` 保存平台接口证据和开发探测材料。  
2. `platforms/private/` 不作为正式运行时账号来源；运行账号由 PostgreSQL 和 Web 控制面维护。  
3. 平台字段进入开发前，必须先在对应平台文档中达到“已验证”。  
4. 仅在完成一个 Goal 时勾选 `todo.md`，并在本文件写明输入、边界、产物、验收命令、结果和仍不确定项；非 Goal 的日常改动不强制写这两份文件。  
5. 当前 Go 验收命令：`go test ./...` 和 `go vet ./...`；引入 Web 嵌入后固定先执行 `npm ci`、`npm run typecheck`、`npm run build`，再执行 Go 测试与编译。  
6. 平台适配器源码进入 `internal/platform/`；可执行入口进入 `cmd/buffgo/`。  
7. Web 源码进入 `web/`；`internal/webui/` 只嵌入构建产物，不承载页面、数据库或采集逻辑。  
8. 目录迁移按真实业务切片执行，不创建空目标包，不把移动与行为改写混入同一 Goal。  
9. 当前只推进 Steam 摘要从网页跑通；BUFF/IGXE 等第三方平台证据门未开则停，不写半套适配器。  


## Goal 验收记录

每个新 Goal 使用以下结构记录，不能只写“完成”或“测试通过”：

```text
### YYYY-MM-DD Goal <编号>
- 输入与前置条件：
- 变更边界：
- 产物位置：
- 验收命令与结果：
- 未确定事项：
```

### 2026-08-11 Goal 0A

- 输入与前置条件：已确认的产品需求、现有平台样例及接口证据边界。
- 变更边界：只整理产品、架构、统一语义、Web 控制台和实施计划，不移动源码。
- 产物位置：`README.md`、`docs/ARCHITECTURE.md`、`docs/REFERENCE.md`、`docs/CONSOLE.md`、`todo.md`。
- 验收命令与结果：`git diff --check`、`go test ./...`、`go vet ./...` 均通过；技术对抗复核与独立盲读通过，读者能够正确复述产品主线、边界、控制台范围、恢复责任和推荐下一 Goal。
- 未确定事项：各平台具体接口、频率和冷却数字仍需在对应证据 Goal 中确认。

### 2026-08-11 Goal 1A

- 输入与前置条件：当前 `internal/buffgo` 源码、目标架构文档、Git 工作树和 Go 工具链。
- 变更边界：只建立迁移基线和修正目标目录约定，不移动源码、不恢复旧项目、不读取或改写平台账号文件。
- 产物位置：本节的包依赖、逐包去向、迁移批次和风险记录；`todo.md` 增加 Goal 1B。
- 验收命令与结果：`go mod verify`、`go mod tidy -diff`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`./build.sh`、`git diff --check` 均通过。
- 验收限制：11 个 PostgreSQL 集成测试因未配置测试 DSN 而跳过；Redis 集成测试实际通过；仓库没有 `package main`，构建只证明库包可编译。
- 未确定事项：旧项目删除是否全部最终保留、现有 PostgreSQL 数据是否需要迁移、Go 1.24.3 最低版本兼容性、`steam_item_name_id` 是否属于正式商品身份。

当前生产依赖：

```text
run → config, telemetry, pipeline, process, source
pipeline → catalog, config, nameid, telemetry, pool, resolve, source, steam, store
process → config, source, store
pool → telemetry, source
nameid → catalog, steam
resolve → catalog, source
source → steam
store → source
```

逐包迁移清单：

| 当前包 | 迁移方式 | 目标与边界 |
| --- | --- | --- |
| `catalog` | 拆分迁移 | 商品模型进 `catalog`；Steam HTTP 进 `platform/steam`；SQL 进 `storage/postgres`；同步编排进 `collection` |
| `config` | 暂留 | 只作为旧装配桥接；启动配置进入 `app`，任务、代理、FX 和 Redis 配置最终删除 |
| `migrate` | 替换候选 | 不搬旧 schema；新 SQL 进入 `storage/postgres/migrations`，执行器进入 `storage/postgres` |
| `nameid` | 拆分迁移 | Steam 解析进 `platform/steam`，批处理进 `collection`，SQL 进 `storage/postgres`；正式身份作用等待接口证据 |
| `observ` | 原样迁移 | 先迁移到 `telemetry`，不改变事件语义 |
| `pipeline` | 拆分重写 | 用例进 `collection`，依赖装配进 `app`，平台请求能力进对应适配器 |
| `pool` | 拆分重写 | 节点、组合和占用进 `resource`；限频与冷却进 `ratelimit`；Redis Manager 最终删除 |
| `process` | 删除候选 | 当前出售最低价与 FX 换算不属于目标；必要的新鲜度和规则逻辑在 `market`、`rule` 重建 |
| `resolve` | 拆分迁移 | 精确映射策略进 `catalog`，SQL 进 `storage/postgres`；删除低置信自动接受 |
| `run` | 暂留 | 只作为 `app` 临时桥接，最终由持久化 `collection` 调度替换 |
| `source` | 拆分迁移 | 行情契约进 `market`，任务契约进 `collection`，Steam/BUFF 实现进各自平台包 |
| `steam` | 拆分迁移 | HTTP 与解析进 `platform/steam`，限频端口进 `ratelimit`，旧 Redis 实现不迁移 |
| `store` | 拆分迁移 | 行情契约进 `market`，PostgreSQL 实现进 `storage/postgres`；重建最新尝试与最近有效价格模型 |

迁移批次与回退边界：

1. Goal 1B 纯迁移 `observ → telemetry`；只回退路径、包名和导入。
2. Goal 2A 建立 `cmd/buffgo → app → legacy run`；只删除新入口即可回退。
3. Goal 3A/3B 先在旧路径固定市场语义，再纯迁移 `market`。
4. Goal 3C/3D 先固定精确商品映射，再提取 `catalog`。
5. Goal 4A 新建商品与行情 PostgreSQL 切片，不搬旧 schema。
6. Goal 5A～5C 重建 `resource` 与 `ratelimit`，旧 `pool` 保留到替换完成。
7. Goal 0B 完成后逐项迁移 Steam 目录、出售、求购适配器；BUFF 留在旧区直到接口证据完成。
8. 用 `collection` 替换 `pipeline/run` 后，每次只删除一个已无调用方的旧包。

Git 边界：当前是旧项目大量 tracked 删除、新项目整体未跟踪的替换状态。不得使用 `git add -u`、`git commit -a`、`git clean`、reset 或 checkout；`go.mod`、新源码、内嵌 SQL 和根 `testdata/` 必须保持同一可复现边界。平台 Cookie 文件属于用户账号资产，本 Goal 未读取、改写或移动。

### 2026-08-11 Goal 1B

- 输入与前置条件：Goal 1A 将 `observ` 认定为唯一可以先做的纯目录迁移包。
- 变更边界：将三个源码文件从 `internal/buffgo/observ` 移到 `internal/telemetry`，更新包名、六个调用文件的导入和限定符；保留事件、字段、日志前缀和统计语义。
- 产物位置：`internal/telemetry/`，以及 `pool`、`pipeline`、`run` 的导入更新。
- 验收命令与结果：受影响包测试、`go test -count=1 ./...`、受影响包 `-race`、`go vet ./...`、`go build ./...`、`gofmt -d` 均通过；旧目录、旧导入和 `package observ` 为零；telemetry 覆盖率仍为 64.0%。
- 等价性证据：独立审查确认生产源码行数、导出声明和 90 个调用引用迁移前后不变，调用方只发生路径、包限定符和 gofmt 导入排序变化。
- 证据限制：旧源码迁移前未被 Git 跟踪，无法用 Git diff 逐字证明；PostgreSQL 测试跳过与本次纯标准库包迁移无关。
- 未确定事项：既有事件仍可携带 `Proxy`、`Account` 和自由文本 `Detail`。使用真实账号或代理测试前必须完成 Goal 1C，不能在本次纯迁移中顺手改变日志语义。

### 2026-08-11 Goal 1C

- 输入与前置条件：Goal 1B 暴露出 legacy telemetry、直接日志和错误摘要会原样携带账号会话、代理端点、Lease、响应正文或外部错误；本 Goal 未读取、改写或使用 `platforms/` 下的真实 Cookie 与代理认证信息。
- 变更边界：新增进程级随机 HMAC-SHA256 安全引用；在 `Emit`、`Multi`、`Memory`、`Log`、`Default` 和自定义 Recorder 包装层统一清洗 `WorkerID`、`Proxy`、`Account`、`JobKey`、`LeaseID` 与 `Detail`。同步收口 legacy 采集链路的直接日志、`Skipped`、代理解析、HTTP transport、完整 URL、响应正文和平台错误文案；没有改动采集开关、分页、限频、存储或价格业务规则。
- 产物位置：`internal/telemetry/redact.go`；`internal/telemetry/{observ,pool}.go`；legacy 的 `catalog`、`nameid`、`pipeline`、`pool`、`run`、`source`、`steam` 安全调用点及对应测试。
- 安全引用：字段按域生成 `*_v1_` 引用，使用 256-bit 进程随机密钥、128-bit 值摘要和可验证 MAC 标签；同值同域在同一进程内可关联，跨域不同，伪造前缀、重复 Recorder 边界和取出后改写 Event 均不能绕过。`WrapError` 保留 `errors.Is/As`，常规及 debug `fmt` 格式只输出 `error_ref`。
- 资源关联：fetch/job 与 lease/rate-limit 事件统一携带安全的 `worker_ref`、`job_ref`、`node_ref`、`account_ref` 和 `lease_ref`；Buff legacy Cookie 只用于生成进程内 `account_ref`，不进入日志、内存事件或结果摘要原文。
- 验收命令与结果：telemetry 与受影响包测试、相关包 `-race`、`go test -count=1 ./...`、`go vet ./...`、`go build ./...`、`gofmt -d`、`git diff --check`、`git diff --cached --check` 均通过；另对未跟踪的改动文件逐个执行无索引空白检查。专项测试覆盖 Cookie、Bearer、代理 userinfo、低熵 IP/别名、DSN/transport 文本、响应正文、代理解析、服务端字段反射、日志控制字符、伪造引用和资源关联，并实际进入 Steam/BUFF 的全量映射失败与行情写入失败四个分支，确认 `fetch.ok`、`job.fail` 保留同一组安全资源引用。
- 验收限制：11 个 PostgreSQL 集成测试仍因没有测试 DSN 跳过；当前源码未被 Git 跟踪，普通 Git diff 不能证明 24 个既有/新增文件的逐行边界。
- 未确定事项：安全引用密钥随进程重启轮换，因此不能关联历史运行；当前只是 opaque 资源引用，不是 Goal 5A 的持久可读账号/节点别名。`Platform`、`Source`、`Kind` 和 `Reason` 仍要求调用方只传受控机器标签；`WrapError` 隐藏对外字符串和格式化输出，但受信代码仍可显式解包原始 cause。

### 2026-08-11 Goal 2A

- 输入与前置条件：Goal 1C 的安全错误边界、现有 `internal/buffgo/run` 常驻循环、Goal 1A 确定的 `cmd/buffgo → internal/app → legacy run` 迁移边界；本 Goal 未读取或使用真实平台 Cookie、代理认证及生产配置。
- 变更边界：新增唯一可执行入口 `cmd/buffgo` 和同步桥接 `internal/app`；CLI 负责参数、SIGINT/SIGTERM 和退出码，app 只校验启动选项、映射参数、调用并等待 legacy runtime。修复 legacy job loop 在预先取消后仍会执行一次任务的问题，并提取实际拥有 Redis client 与 telemetry 恢复职责的 started-runtime 核心；没有改写采集、存储、价格、映射或平台请求行为。
- 产物位置：`cmd/buffgo/{main.go,main_test.go,signal_test.go}`、`internal/app/{app.go,app_test.go}`、`internal/buffgo/run/{run.go,run_test.go}` 和 `build.sh`。生产 `package main` 只有 `cmd/buffgo/main.go`；cmd 唯一项目内直接依赖是 `internal/app`，app 唯一旧路径依赖是 `internal/buffgo/run`。
- 启动契约：`-config` 必填，`-appid` 必须非负；`-sources` 只接受空值或规范化后的 `steam.sell`、`buff.sell` 集合。帮助或协作取消退出 `0`，真实启动/运行失败退出 `1`，参数错误退出 `2`。flag 解析、非法来源和启动失败均不回显原始参数、配置路径、连接串或外部错误，只保留固定原因或 `error_ref`。
- 关闭证据：子进程分别接收真实 SIGINT 与 SIGTERM 并在 runtime 清理后以 `0` 退出；app 的 release gate 证明 legacy runtime 未完成清理时 app 不返回；started-runtime 测试进入生产生命周期路径，证明活动 executor 未返回时进程不继续关闭，随后输出 telemetry 汇总、调用并等待 Redis client `Close`，确认 client 已为 `redis.ErrClosed` 后才恢复原 telemetry Default。这里证明的是本进程调用和等待顺序，不表示 Redis 服务端资源释放已被外部确认，也不表示 Close 错误已经可观测。
- 验收命令与结果：目标包与全量 `go test -count=1`、全量 `go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod tidy -diff`、`bash -n build.sh`、`./build.sh`、Linux/Windows amd64 无 CGO 构建、`gofmt`、Git whitespace check、入口帮助与退出码、生产依赖方向断言均通过；独立对抗审查通过。`build.sh` 现在执行全量测试，并在临时目录构建真实入口后清理产物。
- 验收限制：11 个 PostgreSQL 集成测试仍因没有测试 DSN 跳过；当前未使用真实 PostgreSQL/Redis 配置启动完整 daemon。legacy 请求若不遵守 context，退出仍可能持续等待；本 Goal 没有增加全局强制退出时限、第二次信号强退或 Redis Close 错误上报。当前环境没有 shellcheck，因此只执行了 `bash -n`。

### 2026-08-11 Goal 3A

- 输入与前置条件：Goal 2A 的可执行入口、README 与统一语义文档、现有 Steam/BUFF 原始响应样例和 legacy 采集链路；平台人民币、数量和求购接口证据仍未完成。本 Goal 未读取、改写或使用 `platforms/` 下的真实 Cookie 与代理认证。
- 变更边界：在旧命名空间新增纯市场契约，固定 `bid/ask`、人民币分整数、独立可空的订单数与件数、平台时间与采集时间、`present/empty/unavailable/failed` 和 `complete/partial`。RawOffer 只保留平台原始证据与统一 Observation；现有 Steam/BUFF 摘要样例因缺少已验证人民币证据只产生失败观察，不能晋升为有效价格。没有迁移到 `internal/market`，没有修改旧 PostgreSQL schema，也没有实现真实 bid 采集。
- 金额与状态：统一价格只能通过显式 CNY + 整数分构造，十进制文本使用定点解析，不经过 float 或汇率换算；构造证明绑定价格，构造后改价会 fail-closed。`present` 必须有价格，其他行情状态不得携带价格；空全集是完整覆盖而不是价格 0，完整抓取后的映射或写入失败仍为 `failed + complete`，页上限、后页失败、页间取消和缺少总数则保留已完成页、续点和 `partial`。
- 分页与方向：已知总数时不因矛盾短页提前结束；未知总数且满页时不冒充完整。每页响应完成后独立记录 `collected_at`，平台未提供 `source_time` 时保持为空。ask Runner 在存储检查和平台请求前拒绝 bid 任务，并在解析写入前拒绝 bid Observation。
- 兼容行为变化：配置 side 和 CLI 来源令牌现为 `ask`、`steam.ask`、`buff.ask`；Goal 2A 当时记录的 `.sell` 令牌已被本 Goal 取代，旧令牌按参数错误退出 `2`。删除 legacy `process` 最低出售比价、FX 配置、无人消费的 `stale_after` 及其合成 fixture；旧平台函数、文件、wire 字段和任意 job key 中的 `sell` 仍只表示适配器历史命名，不是统一 side 值。
- 持久化边界：`Quote` 和内存适配器只接受有效 present Observation，ask 按价格升序、bid 按价格降序。旧 quote 表使用浮点主单位和 `buy/sell`，无法无歧义承载新契约，因此 `QuoteStore.Ready` 在任何平台请求前主动拒绝所有行情读写；旧表与旧迁移保持原样，Goal 4A 前 daemon 不会发布真实统一行情。最新尝试与最近有效价格的独立持久化同样留给 Goal 4A。
- 产物位置：`internal/buffgo/market/`；`source`、`store`、`pipeline`、`run`、`config`、`app`、`cmd/buffgo` 和 telemetry 的过渡调用点与反例测试；两个样例配置；删除 `internal/buffgo/process/` 和 `testdata/processor_lowest_sell_fixture.json`。
- 验收命令与结果：领域包与 source 重复测试、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`./build.sh`、`gofmt -d`、Git whitespace check 均通过；独立依赖审查、Git 边界审查和对抗审查通过。专项测试覆盖 USD、空币种、模糊 `¥`、BUFF 缺币种、精确分解析、缺失与显式零价格、bid/ask、四种行情状态、数量未知与零、逐页时间、空全集、映射失败、页上限、后页失败、取消、缺总数、矛盾短页、方向错配、旧存储请求前门禁和 partial telemetry。
- 验收限制：8 个旧 PostgreSQL 集成测试因未配置测试 DSN 而跳过，分别位于 catalog 3 个、resolve 5 个；新项目源码仍整体处于未跟踪区，普通 Git diff 无法证明全部逐行边界。
- 未确定事项：人民币、单位、手续费、数量口径和真实空数据仍需各平台证据 Goal 确认；价格 0 暂作为可表达的显式 present 值，是否允许由接口证据决定。bid 当前只有稳定契约而无平台实现；持久化恢复 Observation 时必须重新走构造器，不可依赖未导出的运行时验证字段。`store → source` 的 QuoteFromOffer 仍是 Goal 4A 前的过渡耦合，Goal 3B 只原样迁移纯 `market` 包。

### 2026-08-11 Goal 3B

- 输入与前置条件：Goal 3A 已稳定并通过对抗审查的 `internal/buffgo/market` 两个文件及其 10 个直接调用文件；迁移前冻结文件哈希、行数、字节数、导出声明、依赖和引用数量。本 Goal 未读取或使用平台 Cookie、代理认证和真实接口。
- 变更边界：将 `market.go`、`market_test.go` 原样移动到 `internal/market`，只在 5 个生产文件和 5 个测试文件替换 Go import，并接受 gofmt 对同组 import 的机械排序；没有修改字段、方法、错误文本、测试、行情状态、完整性、RawOffer、Quote、存储门禁或平台行为，没有添加兼容 alias。
- 产物位置：`internal/market/{market.go,market_test.go}`；旧 `internal/buffgo/market` 目录和旧 Go import 已清零。直接依赖仍只有 legacy `source`、`store`，pipeline 仅测试 helper 直接依赖。
- 等价性证据：`market.go` 迁移前后均为 179 行、5154 bytes、SHA256 `a85277bf2684f728e74c788599620a9fe92fe4e2d5865d6cd1ee1924d54e1261`；`market_test.go` 均为 169 行、6042 bytes、SHA256 `3ecbf06ae71ddd12c27b0c131f7e314782c297dcf78a10ecd24526c7719e6513`。新包仍只依赖 `fmt`、`math`、`strconv`、`strings`、`time`，10 个直接 import 文件及 66 次 qualified 引用均保持不变，导出与不可导出契约随文件原样迁移。
- 验收命令与结果：新 market 与直接/传递受影响包定向测试、四包重复测试、受影响包 race、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod tidy -diff`、`./build.sh`、`gofmt -d`、Git whitespace check、旧路径和依赖方向断言均通过；独立依赖审查、Git 对账和对抗审查通过。
- 验收限制：8 个旧 PostgreSQL 集成测试仍因未配置测试 DSN 而跳过；新项目源码仍整体处于未跟踪区，普通 Git diff 无法展示目录移动相似度，因此使用迁移前后 SHA256、行数、字节数和声明清单证明等价。
- 未确定事项：无新增业务未确定项；Goal 3A 已记录的平台证据、持久化和运行时 proof 恢复限制继续有效。回退只需将两个文件移回旧目录，并把同 10 个 import 路径改回；下一项为 Goal 3C 商品身份语义改造。

### 2026-08-11 Goal 3C

- 输入与前置条件：Goal 3B 后的市场契约、REFERENCE 的商品映射顺序、legacy `catalog`、`resolve`、`source` 与 pipeline；Goal 0B 尚未确认正式 Steam 商品标识组合。本 Goal 未读取或使用真实 Cookie、平台接口与 PostgreSQL。
- 变更边界：在旧路径新增纯商品身份契约，固定内部 `ProductID`、Steam 标准商品、平台商品、既有平台映射和 typed 匹配结果。顺序只允许“已经验证且唯一的 `(platform, appid, platform_item_id)` 映射 → 同 `appid` 原字符串逐字一致且唯一的名称”；不 trim、忽略大小写、Unicode 归一化、跨游戏、模糊匹配、低置信自动接受、自动建商品或自动写映射。坏映射、歧义和未命中均 fail-closed，且不会降级或修改输入集合。
- 证据边界：`RawOffer` 新增仅供已验证适配器填写的 `ExactName`；`NameRaw`、`MarketHashName` 和第三方类似 Steam 的字段继续只是原始证据，Normalize、Steam/BUFF parser、catalog parser 与 legacy Steam search 均不会把展示名或 raw hash 晋升为正式匹配输入。名称兜底成功也不会自动形成平台映射。
- 活动路径：`MemoryResolver` 只委托纯 matcher；原有 case-insensitive、`needs_review`、EnsureMissing 和 auto-link 行为已删除。legacy PostgreSQL Resolver 在任何 SQL 前固定返回 `ErrLegacyResolverUnavailable`，等待 Goal 4A 重建。两个 ask Runner 在 resolve 前校验 offer 的 side、platform 与 appid，在写行情前校验 matched、正 ProductID、合法 method 与空 reason；伪造结果、跨平台或跨游戏 offer 均不能触发 resolver 后续或行情写入。
- 其他正确性修复：Steam、BUFF 在 hash/平台 ID 缺失时按 raw name 单独去重，避免本次取消 name→hash 后把不同原始行折叠；catalog 缺 hash 不用展示名伪造，导入时只给零 appid 补目标值，显式跨 appid 在 Store 前拒绝。
- 产物位置：`internal/buffgo/catalog/{identity.go,identity_test.go}`；`resolve` 的内存与隔离适配器；`source`、`steam`、`pipeline`、legacy catalog 的最小调用边界与反例测试。
- 验收命令与结果：五个受影响包重复测试与 race、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`./build.sh`、`gofmt` 与 Git whitespace check 均通过；独立依赖审查、Git 边界审查和对抗审查通过。反例覆盖映射优先、坏映射不降级、大小写/空白/Unicode/跨 appid、同名歧义、未知商品不创建、名称成功不 auto-link、raw name/hash 不晋升、PG 查询前隔离、伪造 MatchResult、跨任务 offer 和零行情写入。
- 验收限制：旧 resolve 的 5 个 PostgreSQL 语义测试已由无 SQL 的 fail-closed 测试替换；仍有 3 个 legacy catalog PostgreSQL 测试因未配置 DSN 跳过。新源码处于未跟踪区，普通 Git diff 不能证明全部逐行边界。
- 未确定事项：Goal 0B 前不定义正式 SteamKey，也不猜测第三方 `steam_id`、`market_hash_name` 或其他字段的关联语义；真实 Steam/BUFF 适配器当前不会设置 `ExactName`。因此除了已经验证的既有 PlatformMapping，真实原始数据会正确地保持未映射；持久商品与平台映射 schema、历史冲突处理和 PG 适配器由 Goal 4A 完成。

### 2026-08-11 Goal 3D

- 输入与前置条件：Goal 3C 已稳定且通过对抗审查的 `internal/buffgo/catalog/{identity.go,identity_test.go}`；迁移前冻结两文件的 SHA256、行数、字节数、导出声明、直接调用方和依赖。本 Goal 未读取或使用真实 Cookie、平台接口与 PostgreSQL。
- 变更边界：将两份纯身份文件原样移动到 `internal/catalog`，只更新 3 个生产调用文件和 3 个测试调用文件的 import；`pipeline/observ_test.go` 使用 `legacycatalog` 读取旧 fixture DTO，使用新 `catalog` 处理 MatchResult。没有迁移旧 Item/import/Steam HTTP/PG Store/Service、`nameid` 或 `resolve` 适配器，也没有增加兼容 alias。额外只修正旧包的一行 package 注释，避免身份文件移走后仍错误声称旧包包含正式身份规则；不改变行为。
- 产物位置：`internal/catalog/{identity.go,identity_test.go}`。旧 `internal/buffgo/catalog` 保留 legacy Steam catalog import、HTTP 与 PostgreSQL 适配器；`resolve.MemoryResolver` 继续作为新 matcher 的过渡适配器，PG Resolver 继续固定 fail-closed。
- 等价性证据：`identity.go` 迁移前后均为 169 行、5321 bytes、SHA256 `c82aa52f6a6af9a205b04f32af5276c24e7a9e8b9ece8c5a029da9deb78b2aeb`；`identity_test.go` 均为 270 行、11994 bytes、SHA256 `bff205b9b7fd4de21743b8fb4ae8f55160e6c7fc38ba8e550709aec95dd31bf0`。新包生产依赖为空，测试仅依赖 `reflect`、`testing`；旧 identity 文件、声明和 import 为零，恰有 6 个直接 import 文件和 61 次新包限定引用。
- 验收命令与结果：新 catalog、legacy catalog、resolve、pipeline 定向与重复测试、定向 race、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`./build.sh`、`gofmt`、Git 与未跟踪文件 whitespace check、依赖和旧路径断言均通过；独立依赖审查、Git 对账与对抗审查通过。仍只有 3 个 legacy catalog PostgreSQL 测试因无 DSN 跳过。
- 证据限制：新源码仍处于未跟踪区；两份迁移文件有完整 hash 证据，6 个调用文件主要依靠导入/限定引用对账、依赖图和行为测试证明等价，普通 Git diff 不能单独证明其逐字边界。
- 未确定事项：正式 SteamKey 和真实适配器的 `ExactName` 来源仍等待 Goal 0B；ProductID 分配、PlatformMapping 持久化及 PG 适配器等待 Goal 4A。旧 `internal/buffgo/catalog`、`nameid` 和 `resolve` 中没有其他已验证且可纯迁移的身份领域代码，因此本 Goal 不创建空抽象或搬动未验证 legacy 行为。

### 2026-08-11 Goal 4A

- 输入与前置条件：Goal 3D 的纯商品身份、Goal 3A/3B 的人民币行情契约，以及 REFERENCE 已批准但尚未由运行层分配的开关版本、运行序号和页面序号。Goal 0B 尚未确认正式 Steam 商品键，Goal 7A 尚未建立目标级开关与运行 fence；本 Goal 未读取或使用真实平台 Cookie、代理认证和接口响应。
- 变更边界：在 `internal/market` 新增 1-based `WriteOrder`；新增独立 `internal/storage/postgres`，包含 caller-owned `*sql.DB` Store、商品与映射方法、行情事务、内嵌迁移器和 PostgreSQL 集成测试。没有修改或调用 legacy `internal/buffgo/migrate`、`items/platform_items/quotes`，没有接入现有 daemon，也没有创建账号、节点、运行、游标、完整性、冷却、规则、详情或订单表。
- 迁移与表：包内 `migrations/000001_catalog_market.sql` 只创建 `steam_products`、`platform_product_mappings`、`market_latest_attempts` 和 `market_last_present` 四张业务表，另有独立 `buffgo_storage_migrations` 元数据表。迁移按文件名排序、逐版本事务执行，以 PostgreSQL transaction advisory lock 串行化，记录 SHA-256；已应用版本发生 checksum 漂移时 fail-closed。新表与 legacy schema 可共存，不回填或解释旧浮点、多币种、`buy/sell` 数据。
- 商品与映射：`product_id` 只由数据库显式分配；同一 `appid` 的完全相同名称允许生成多个商品，名称和 `market_hash_name` 都不是当前自然键，也不提供按名称 upsert。映射键固定为 `(platform, appid, platform_item_id)`，相同目标重复写幂等，不同目标返回 typed conflict 且绝不重绑；复合外键保证映射不能跨 `appid` 指向商品，身份文本使用 bytewise `C` collation。
- 行情事实：最新尝试表不含价格、币种或数量；最近有效价格只存 `price_cny_cents`、独立可空且非负的订单数/件数和时间。`present` 在同一事务更新两类事实，`empty/failed/unavailable` 只推进最新尝试并保留历史有效价格。`ObservationBatch` 只接受已经有 `product_id` 的显式商品事实，不会把未出现商品批量标成空、失败或下架；任一非法、过期、冲突或 SQL 失败会使整批回滚。
- 顺序与读回：`WriteOrder` 使用从 1 开始的 `(switch_version, run_sequence, page_sequence)` 字典序。同身份旧序写入被拒绝，同序完全相同为幂等，同序不同状态、时间、价格或数量为冲突；不同身份可合法使用相同序号。PostgreSQL 时间统一为 UTC 微秒精度，截断后为零的时间被拒绝。最新尝试与最近有效价格通过独立 API 读取，并以单 SQL 快照互相核对因果关系；孤儿、零时间或“历史价格版本晚于最新尝试”等污染返回 `ErrMarketIntegrity`。bid 与 ask 使用独立身份和读写路径。
- 性能与原子性：商品列表使用真实 `(appid, product_id)` 索引；映射冲突和行情高低版本并发均由真实 PostgreSQL 验证。测试还实际进入“批内第一商品已写、第二商品因数据库约束失败”分支，确认两种行情表和所有商品均整体回滚。
- 验收命令与结果：一次性 PostgreSQL 16.14 上，`BUFFGO_TEST_DSN` 下新包普通与 race 集成均为 `0 fail / 0 skip`，覆盖 fresh/reapply/concurrent migration、checksum 漂移、失败迁移回滚、实际五表白名单、约束/FK、同名商品、C collation、映射并发冲突、四种行情状态、零价、nil/0 数量、stale/equal/newer、跨商品回滚、bid/ask 隔离和不一致数据读回；测试创建的随机 schema 全部清理。无 DSN 全仓 `go test -count=1 ./...` 与全量 race 均通过并恰有 4 个明确 skip（3 个 legacy catalog、1 个新 PG 顶层），`go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`./build.sh`、gofmt 与 Git whitespace check 均通过；独立对抗审查重复执行真实 PG 普通/race 后放行。
- 既有阻断：fresh PostgreSQL 上给全仓同时设置 DSN 时，新包仍为 `0 fail / 0 skip`，但 3 个 legacy catalog 测试因旧 `migrate.splitSQL` 会把 `schema.sql` 注释内分号切成非法 SQL 而失败。该缺陷不由本 Goal 制造，新存储不依赖旧迁移，因此未在本 Goal 顺手修改；不能把本次验收表述为“全仓 fresh PG 通过”。
- Git 与资产边界：本 Goal 新增 11 个未跟踪文件（`internal/market` 2 个、`internal/storage/postgres` 9 个），另按项目规则只更新本条 `work.md` 和 Goal 4A 的 `todo.md`；没有触碰旧 staged 删除、75 个 tracked 删除、4 个既有 tracked 修改或 Cookie。Steam/BUFF Cookie 的大小和修改时间保持不变，内容未读取。
- 未确定事项与硬边界：正式 SteamKey、目录同步幂等方式、真实平台人民币/单位/数量及零价证据仍分别等待 Goal 0B 和平台证据 Goal。`ObservationBatch` 不是页面 manifest；行内顺序只能保护已经存在的同一行情身份，不能阻止“较新运行未出现商品 P、旧运行的 P 随后首次写入”。完整目标级 fence、关闭版本校验、页面集合和运行完整性必须在 Goal 7A/7B 与行情写入同一事务中完成；在此之前新存储不接生产采集，legacy resolver/QuoteStore 继续 fail-closed。

### 2026-08-11 Goal 5A（资源模型与加密持久化切片）

- 输入与前置条件：Goal 4A 的独立 PostgreSQL 存储边界，以及架构文档中已经确定的平台账号、访问节点、地域、独占分配、出口验证和只写凭据语义。本次没有读取、改写或使用 `platforms/` 下的真实 Cookie、代理认证和真实平台账号。
- 变更边界：新增纯标准库 `internal/resource` 和 `internal/credential`；在 `internal/storage/postgres` 增加资源方法、真实 PostgreSQL 集成测试与 `000002_resources.sql`，并让 Store 可显式注入凭据 Cipher。没有接入 legacy daemon，没有创建账号节点组合、运行时占用、限频、冷却、运行或 API 表。
- 资源模型：平台账号带稳定 ID、平台、别名、会话状态和会话 revision；访问节点统一表达本机直连与代理，地域只允许 `domestic`、`foreign`、`hongkong`，香港可访问国内和国外目标。节点只允许 `static` 或 `sticky` 出口模式，并保存状态、egress revision、可选的平台独占分配和当前出口验证事实；验证过期、revision 不一致或超过粘性会话寿命时 `UsableAt` 必然为 false。
- PostgreSQL 表：迁移只新增 `platform_accounts` 与 `access_nodes` 两张业务表。节点的平台分配是单行可空属性，同一时刻最多属于一个平台；账号和节点安全查询只返回别名、状态、revision、地域、出口事实和凭据存在位，不选择或回显密文。当前只保存节点最近一次已接受的出口事实；旧 IP 的限频与冷却审计属于 Goal 5C，不在本 Goal 提前增加历史表。
- 出口状态机：账号会话替换与节点连接变化均推进 revision 并使旧验证结果失效；旧账号检查、旧节点验证和旧失败结果通过条件更新拒绝。账号检查时间单调，同时间冲突状态只有一方成功；节点同一验证 generation 的成功与失败只有一个终态。代理重新验证在行锁事务内按新 revision 重封凭据、清除旧出口事实后进入 `validating`；平台解除要求提供期望平台，不能清除并发重新分配后的值。
- 凭据安全：账号会话及完整、可恢复的代理连接材料使用 AES-256-GCM 和随机 nonce 加密。密钥和 key ID 由进程外部显式提供，不存入 PostgreSQL；AAD 绑定凭据类别、数据库生成的稳定资源 ID、revision，并按账号平台或节点类型进一步隔离。跨账号、跨节点、跨凭据类别、旧 revision、删除后同名重建和错误密钥均不能解密；创建资源时先分配 ID 再加密，不写入占位明文。
- 数据约束：Go 与 PostgreSQL 同时拒绝空或非规范平台、非法状态、控制字符标签、私网、CGNAT、IPv4-mapped、回环、链路本地、多播、`0/8`、`240/4` 和带网络前缀的出口地址。直接节点不得携带代理密文或粘性会话；代理节点必须有完整加密 envelope；`available` 必须带与当前 revision 一致且时间有效的出口事实。
- 真实验收：一次性 PostgreSQL 16 上，`BUFFGO_TEST_DSN` 下 `go test -count=3 ./internal/storage/postgres` 与 `go test -race -count=3 ./internal/storage/postgres` 均通过且 `0 skip`；独立对抗审查另完成普通/race 重复与 race `count=10`。测试覆盖迁移、两表白名单、凭据重启恢复与交换/回放、NULL CHECK 反例、账号时间并发、节点成功/失败竞态、代理重封、共享出口、过期与粘性有效期、平台分配和 stale unassign；随机 schema 全部清理。
- 全仓验收：无 DSN 的 `go test -count=1 ./...` 与全量 race、`go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`./build.sh`、gofmt、依赖方向、tracked/cached diff check 及本 Goal Go/SQL 文件的无索引 whitespace check 均通过。无 DSN 全仓恰有 4 个明确 skip：3 个 legacy catalog PostgreSQL 测试和 1 个新存储顶层集成测试；新存储不依赖任何 `internal/buffgo` 包。
- Git 与资产边界：本切片新增 `internal/resource`、`internal/credential` 各 2 个文件，并在 `internal/storage/postgres` 新增资源实现、资源集成测试和 `000002`，修改 Store 与迁移测试清单；没有触碰旧 staged 删除、tracked 旧项目删除或既有 Cookie。两份 Cookie 的大小和修改时间保持不变，内容未读取。
- 未完成与未确定事项：Goal 5A 的真实出口探测仍未完成。当前实现负责验证状态机、严格校验并持久化探测结果，但还没有请求架构文档所说的“受控出口检测服务”；在明确该服务的请求/响应契约以及代理连接材料编码前，不能猜测外部服务或把第三方 IP 查询接口写死，因此对应 `todo.md` 项保持未勾选。重启解密要求保留同一份外部 32 字节密钥和 key ID；当前是单密钥 Cipher，不支持多密钥轮换。账号节点组合、占用保护属于 Goal 5B，IP 限频与旧 IP 状态属于 Goal 5C。

### 2026-08-11 Goal 5B（显式组合与进程内占用）

- 输入与前置条件：Goal 5A 的账号、访问节点、平台独占分配、revision 与加密凭据模型，以及架构文档已经确定的“显式组合、页面请求级独占、单机单进程”边界。本 Goal 未读取、改写或使用 `platforms/` 下的 Cookie、真实账号和代理认证信息；Goal 5A 的真实出口 HTTP 探测仍保持未完成。
- 变更边界：在 `internal/resource` 新增组合值对象、进程内 `Coordinator` 及对应测试，并给节点补独立的 `assignment_revision` 和安全的连接写入参数；在 `internal/storage/postgres` 新增组合存储、`000003_resource_combinations.sql` 与集成反例，并更新资源读写、迁移清单和集成入口。没有增加 PostgreSQL occupancy、lease、TTL、heartbeat、任务、开关或限频表，也没有接入 legacy Redis pool。
- 持久组合：`account_node_combinations` 只保存使用者明确建立的账号节点对，允许同一账号配置多个节点、同一节点配置多个账号，唯一键仅为 `(account_id,node_id)`。平台由账号和节点事实推导，两个复合外键强制账号、节点和组合同平台并使用 `RESTRICT`；相同组合幂等，删除重建得到新 ID，跨平台、未分配节点和依赖中的删除、解绑或重分配均拒绝。
- 分配防竞态：`assignment_revision` 与 egress revision 分离。实际分配、解除和重分配使用期望平台与 revision 的严格 CAS，每次真实变化推进 revision；旧的“解除 steam”请求不能在节点经历解除并重新分回 steam 后清掉新分配。账号会话和代理连接材料只按租约快照中的 session、egress、assignment revision 与平台打开。
- 运行时占用：`Coordinator` 由单个进程共享，按组合、账号和节点同时登记 owner；不相交组合可并行，共享组合、账号或节点只有一个获取者。进程随机 epoch 与单调 token 防止旧进程或旧租约释放新占用；新 Coordinator 不加载或继承旧占用，组合继续保存在 PostgreSQL，并在每次准入时按 ID 重新读取。可取消的一槽协调门串行化准入与危险配置修改，数据库等待中的调用可以按 context 退出；`Release` 和组件取消只使用独立的内存 owner 锁，不会被慢数据库操作阻塞。
- 停止与维护：组件取消先拒绝新准入并取消现有租约上下文，再等待执行方显式 `Release`；超时不会强制释放，重复取消可以继续等待同一 drain。节点维护先取得 node-only 租约，再按精确 token 执行重新验证、打开代理连接材料以及写入成功或不可用结果，覆盖 `Begin → 外部探测 → Record/Mark` 整个窗口；页面请求和节点维护不能同时占用同一节点。
- 危险操作保护：删除组合、账号或节点，替换账号会话或节点连接，以及节点分配、解绑和重分配都通过具名 Coordinator 方法检查当前 owner；占用期间不会调用底层 Store。底层资源 Store 的数据库、驱动和扫描错误统一映射为固定的 `ErrResourceStorage` 或 `ErrResourceIntegrity`，关闭数据库反例覆盖账号、节点、组合和所有上述资源路径，不向上返回连接或 SQL 细节。
- 真实验收：全新一次性 PostgreSQL 16 容器中，目标包依次通过 `go test -count=1`、`go test -count=5` 与 `go test -race -count=3 ./internal/storage/postgres`，集成测试实际运行且没有 skip；容器和随机 schema 均已清理。第一次容器内部 ready 与 macOS 主机端口转发之间发生启动竞态，连接在任何迁移前以 EOF 结束；改为等待主机转发稳定后重试，三轮验收全部通过。
- 全仓验收：`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...` 均通过；`internal/resource` 另通过普通 `count=20`、race `count=5`，独立实现与对抗审查分别完成更高重复次数。测试覆盖共享账号、共享节点、同组合单胜、不相交组合并行、精确旧 token、重启 epoch、取消等待与重试、父 context 取消不偷占、慢存储下及时取消、维护租约、凭据快照、提交后调用取消、危险操作保护、组合 FK、assignment ABA 和安全错误边界。
- Git 与资产边界：本 Goal 的业务改动限定在 `internal/resource` 6 个文件、`internal/storage/postgres` 6 个文件以及按规则更新的现有文档、`todo.md` 和本记录；没有新增 staged 项，没有恢复或改写旧项目删除。Steam/BUFF Cookie 仍为 3425/2334 字节，修改时间仍为 `06:10:05`/`06:10:42`，内容未读取。
- 后续边界：当前只保证一个共享 Coordinator 实例内的单进程占用，不提供多进程协调；进程崩溃后占用归零，但不能据此断言旧远端请求已经结束。新 Coordinator 尚未接入真实采集；Goal 7B 必须在装配层共享唯一实例、用当前时间复核节点有效期，并让 Worker 只拿 Coordinator 而不是原始 Store。登录失效回写届时需增加租约 owner 授权的具名入口。真实出口探测仍属于未完成的 Goal 5A，平台、接口、账号、实际 IP 与账号加 IP 的限频和冷却属于 Goal 5C。

### 2026-08-11 Goal 5C（限频与冷却）

- 输入与前置条件：基于 Goal 5A 保存的账号、实际出口验证事实和 Goal 5B 的活动组合租约建立准入身份。平台总预算覆盖目录、摘要和详情等全部外部请求，同平台 bid/ask 共享；每个请求必须同时存在启用的非冷却型平台总规则和精确接口配置。账号、IP、账号加 IP 规则只在平台证据明确支持时启用。本 Goal 未读取或使用真实 Cookie、账号会话、代理认证和平台限频数字。
- 变更边界：新增纯领域包 `internal/ratelimit`；强化 `internal/resource` 的私有租约 authority，使复制或改写公开快照不能伪造限频身份；在 `internal/storage/postgres` 增加 Store 私有签发器、策略与状态方法、`000004_rate_limits.sql` 及普通/真实 PostgreSQL 反例。没有接入 legacy daemon、Redis pool、平台 HTTP 客户端、调度循环或 API，也没有修改 `go.mod`、`go.sum` 和旧迁移。
- 策略模型：范围固定为平台、接口、账号、实际出口 IP、账号与 IP；算法固定为最小间隔、严格滚动窗口和仅冷却。每个平台以稳定 `rule_key` 保存可修订策略，同一范围可以叠加多条证据规则。缺少平台总规则、缺少精确接口配置或禁用任一必需配置时均 fail closed；接口配置允许只有 `cooldown_only`，但平台总规则必须实际限制速率。仓库没有写入任何 Steam、BUFF 或其他平台的猜测值。
- 原子准入：Store 在平台 advisory transaction lock 下按稳定顺序锁定本次适用的策略和具体状态，拿齐锁后才读取 PostgreSQL 时间并结合相关状态的逻辑时钟。全部规则同时满足才在一个事务内预占；阻塞请求不消耗任一预算，成功预占不因随后取消而退款。严格滚动窗口使用 `(now-window, now]`，等于窗口边界时可再次准入；数组最多保存 1024 个时间点，明确限制 O(N) 重写规模，超过该证据量需要更换持久化策略而不是近似计算。
- 策略修订：创建和替换使用全平台策略 `changed_at` 与每条策略最高状态时钟作为高水位，并在真正取得相关锁后重新采样数据库时间，时钟回拨或长时间锁等待不会缩短完整 warmup。替换使用 revision CAS，重置节奏但保留既有冷却；并发同 revision 替换只有一方成功。热路径只读取本次具体状态，旧 IP 历史状态不会被每次请求全量扫描或锁定；低频管理查询由 `(policy_id, clock_floor_at DESC)` 索引支持。
- 冷却反馈：准入凭据由 Store 的进程随机 HMAC 签发，绑定请求、策略 revision、范围、接口和当时 fallback。429/风控只能选择本次准入实际应用的范围；首次反馈时间由 Store 在事务内读取数据库时钟，调用方不能注入时间。相同命令并发或失败重试保持同一观察时间和截止时间，冲突命令拒绝；策略 identity 不变时，已发请求可在 revision 更新后写入当前状态，但仍使用签发时的旧 fallback，不能影响后来新增的同范围规则。
- 持久状态：迁移只新增 `rate_limit_policies` 与 `rate_limit_states` 两张业务表。五种范围都通过约束固定字段形状，IP 只接受最近验证的公网 host 地址，状态不引用节点、组合或资源表；多个节点命中同一出口时共享 IP 状态，出口变化使用新键，旧 IP 行不删除。平台命名空间隔离状态，同一 IP 的 Steam 冷却不会传递给 BUFF。新 Store 从 PostgreSQL 恢复节奏和冷却，但使用新的进程签发密钥，因此旧进程的 Admission 会被拒绝且不能改写截止时间。
- 真实 PostgreSQL 验收：在全新一次性 PostgreSQL 16 中，顶层集成测试实际执行为 `1 pass / 0 skip / 0 fail`；实现、主线程和独立审查分别完成 storage 普通多轮及 race 多轮，容器和随机 schema 均已清理。反例覆盖五种范围、bid/ask 平台共享、跨平台隔离、共享/变化出口、严格时间边界、并发原子准入、缺失和禁用策略、策略 CAS、锁等待后出口过期、完整 warmup、反馈失败重试、跨 revision fallback、重启冷却恢复、旧 ticket 拒绝、精确列/FK 白名单、损坏读回和索引查询计划。
- 全仓验收：无 DSN 时 `go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod verify` 与 `go mod tidy -diff` 均通过；JSON 对账为 `0 fail / 4 skip`，即 3 个 legacy catalog PostgreSQL 测试和 1 个新存储顶层集成测试。`internal/ratelimit`、`internal/resource` 另通过普通 `count=100`、race `count=30` 的独立重复审查。
- Git 与资产边界：本 Goal 的业务改动限定在 `internal/ratelimit` 2 个文件、`internal/resource` 2 个文件、`internal/storage/postgres` 8 个文件，以及按规则更新的现有架构/参考文档、`todo.md` 和本记录；没有新增 staged 项，没有恢复或改写旧项目删除。Steam/BUFF Cookie 内容未读取，文件大小和修改时间保持原值。
- 后续边界：当前只完成通用模型、PostgreSQL 原子准入与冷却恢复，没有任何真实平台策略 seed，也未把真实 HTTP 请求接入准入器；具体接口分类、数字、429 范围和 live 接线分别由各平台证据 Goal 与 Goal 7B 验收。Goal 5A 的受控出口 HTTP 探测仍未完成。账号范围暂以内部 `AccountID` 为键，删除并重建同一远端账号会获得新 ID，直到平台证据提供稳定远端身份前不能宣称继承旧账号冷却；旧 IP 状态仍保留。进程崩溃后旧 Admission 不可恢复，远端在途请求也不能据此判定已经停止。

### 2026-08-11 Goal 0B（Steam 首轮受控复探，未完成）

- 输入与基线：Goal 0B 是 Goal 6A 前的硬前置。复探前 `platforms/steam/` 没有任何接口文档，现有 Steam 样例只能证明若干字段形状曾被观察；`steam_buy_sample.json` 仍只有 `sell_*`，没有求购、人民币、手续费、账号地区、登录失效、详情或 429 恢复证据。目标 Steam、catalog 和 source 包的基线测试均通过。
- 凭据隔离：审查发现 `platforms/steam/cookie.md` 与 `platforms/buff/cookie.md` 都是未跟踪且未被忽略的真实凭据文件。二者已原样移动到现有忽略规则覆盖的 `platforms/private/<platform>/cookie.md`，权限改为 `0600`；大小和修改时间仍为 Steam `3425 / 06:10:05`、BUFF `2334 / 06:10:42`。没有输出、复制或改写 Cookie 值，Git 的未跟踪清单不再包含私密文件。
- 探测门禁：临时探测器经三路只读审查后才执行，固定为当前工作机直接出口、精确 HTTPS host/path/query/referer、GET-only、单并发、最多 6 次、请求完成后至少间隔 30 秒、20 秒超时、零重试、零翻页、禁重定向、JSON 1 MiB/HTML 2 MiB 上限。Transport 禁止连接复用与 HTTP/2，公开接口不带 Cookie，认证请求硬上限 1；错误只输出状态、类型和 `Retry-After` 是否存在，不输出 URL、正文、header、商品名、账号 ID 或出口 IP。临时源码在执行后已经删除，没有进入项目实现。
- 实际结果：`2026-08-11T17:55:25Z` 的第 1 个匿名 `GET steamcommunity.com/market/search/render/` 返回 `429`、`Content-Type: application/json`，没有 `Retry-After`。探测器在读取正文和发送后续请求前立即停止；真实请求数为 1、认证请求数为 0，因此登录 Cookie 从未发送。零结果搜索、listing HTML、订单簿、listing render 与匿名 pricehistory 均未请求。
- 证据结论：当前只能确认该匿名搜索请求收到 `429`，响应头没有 `Retry-After`；正文未读取，不能判断正文是否含恢复提示。无法据此判断限制键是直接 IP、搜索接口、平台总预算还是更广的风控；阈值、窗口、冷却时长、成功与空数据形态、人民币/单位/手续费、账号地区关系、bid/ask 数量口径、`market_hash_name/classid/item_nameid` 的身份作用仍全部未知。请求参数 `currency=23` 与 `country=SG` 只是候选条件，不能当作 CNY 或账号地区证明；旧 `80/100/5m/180s` 数字不得进入新限频策略。
- 文档产物：更新现有平台资料规则，新增 `platforms/steam/README.md` 和 `search-render.md`，只保存脱敏请求条件、429 元数据、停止行为和未知项；同时把 legacy 样例配置、限频、Steam wire DTO、catalog/source 与 nameid 注释中的既有推断明确降级为未验证兼容语义，移除未使用的 `CurrencyCNY=23` 等正式命名，未改变请求参数或运行行为。`todo.md` 的 Goal 0B 三项全部保持未勾选，没有用一次失败观测冒充接口已验证。
- 恢复边界：收到 `429` 后不切换出口、代理或接口规避限制。使用者于 2026-08-12 纠正：Steam `429` 通常较快恢复，原先 24 小时本地门禁过度保守，现已移除；该经验只作为项目输入，不写成固定冷却时长。具体恢复时间和限制范围仍需受控复探确认；即使首次成功也只标为 `observed_once`，仍需另一时段重复对账。正式 Steam 字段、目录和行情适配继续 fail closed，Goal 6A 仍被本 Goal 阻塞。
- 未把握事项：当前无法判断 429 来自直接 IP、平台总预算、搜索接口预算还是更广的风控；也无法从缺少 `Retry-After` 推测安全恢复时间。没有尝试代理、Cookie 登录、验证码绕过或其他 endpoint 来规避限制。

### 2026-08-11 Goal 7A（运行状态与持久化）

- 输入与前置条件：Goal 0B 仍因 Steam 首轮匿名请求收到 `429` 而保持未完成，Goal 5A 的真实出口 HTTP 探测也尚未完成；二者阻止真实平台采集和调度接线，但不阻止纯运行状态与 PostgreSQL fence。本 Goal 没有发送平台请求、读取 Cookie、创建平台适配器或写入任何限频策略。
- 领域边界：新增纯 `internal/collection`，分别建模 `catalog`、`summary`、`detail` 运行形状，期望状态、实际状态、运行结果与完整性保持独立。目录目标固定为 `(steam, appid)` 并要求微秒精度的正数周期；摘要开关固定为 `(platform, side)` 且不含 `appid`，摘要运行另带正数 `appid`，因此同一方向能统一关闭所有游戏而不同游戏仍可并行；详情仅保留领域和 schema 形状，不提供目标或创建入口。
- 状态与版本：target `revision` 记录配置和状态 CAS，`switch_version` 只在启用或禁用时推进；修改目录周期不推进开关版本，也不改实际状态时间，因此不会错误打断在途运行或破坏已经到期的 recheck。自动 waiting/blocked 只有到达 `recheck_at` 后才能重新运行；manual blocked/error 必须显式 `Recover` 回到 starting，不能由调度状态转换绕过。运行终态不可回退；完整性为 complete 时必须至少提交一页，空范围如要声明 complete 也要提交显式空页，run outcome 与完整性仍是独立事实。
- 持久化：`000005_collection.sql` 只新增 `collection_targets`、`collection_runs`、`collection_pages` 三张业务表；约束覆盖 target/run/page 形状、受控原因、状态时间、微秒周期上界、活动运行唯一性、游标、32 字节页面摘要和完整运行至少一页。Store 提供目标、运行、页面的最小具名 API；`Pages` 在只读 `REPEATABLE READ` 事务中读取 run 与页面，避免两个快照把一致数据误报为损坏。
- 同事务 fence：唯一公开摘要写入口 `CommitSummaryPage` 在一个事务中按 target → run → page/market 顺序锁定并核验目标仍启用、开关版本仍当前、运行状态严格为 running、页序连续且游标衔接。`WriteOrder` 只从持久的 `(switch_version, run_sequence, page_sequence)` 派生；调用方提交 Attempts，但不能提交 scope、版本、`WriteOrder` 或 `payload_digest`。页面 manifest、行情 latest/last-present 和运行游标要么全部提交，要么全部回滚。完全相同的页面重试幂等，内容变化、旧开关、旧运行、迟到页、跳页、跨 appid 商品和孤儿行情事实均拒绝；空页只推进页面与游标，不伪造未出现商品的状态。生产代码已没有公开或未加 fence 的 `SaveObservations` 旁路。
- 时间与完整性：页面及每个 attempt 必须落在当前运行开始之后且不晚于页面采集时间；结束运行会把数据库完成时间抬高到最后一页的提交时间，跨节点时钟或未来采集时间不会产生 finished-before-page。页面摘要使用稳定、排序后的语义字段计算；重建 Store 后可以核对连续页、游标链与最后页，最新尝试和最近有效价格继续由独立行情查询读回。
- 真实验收：一次性 PostgreSQL 16 上，storage 普通与 race 的 `count=3` 重复验收通过；最终稳定快照又各跑一轮并明确 `0 skip`，实际执行 collection DDL、Store 生命周期/并发/损坏反例，以及摘要页的原子生命周期、fence/回滚、并发重试与孤儿事实三组测试。纯领域通过普通 `count=100` 与 race `count=30`；独立对抗审查未发现剩余正确性阻断。
- 全仓验收：`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./...`、`go mod verify`、`go mod tidy -diff`、`./build.sh`、`bash -n build.sh`、gofmt、tracked/cached 与本 Goal 文件 whitespace check 均通过；`work.md` 开头既有的 Markdown 硬换行保持原样。无 DSN 的 JSON 对账为 `0 fail / 5 skip`：3 个 legacy catalog PostgreSQL 测试，以及 `TestPostgresIntegration`、`TestCollectionStoreIntegration` 两个显式存储集成入口。
- 未完成与未确定事项：本 Goal 没有调度循环、常驻恢复、HTTP/API、真实平台适配器、详情规则或多游戏 actual-state 聚合，均留给后续 Goal；Goal 0B 与 Goal 5A 对应复选框继续保持未完成。当前逐页行情事务对每个商品执行约 3–5 次 SQL，并在 target/run 锁内完成，真实大页接入前必须改为批量 scope 校验和批量锁写；本 Goal 只验证正确性，不能宣称吞吐性能已经验收。人工直接破坏数据库形成 orphan page/run 的 exact retry 不是正常事务可达路径，读回完整性检查仍会 fail closed。

### 2026-08-11 Goal 7B（单周期调度）

- 输入与前置条件：基于 Goal 7A 的 target/run/page 状态机与 fence 持久化、Goal 5B 的组合占用 Coordinator、Goal 5C 的限频准入。本 Goal 不发送真实平台请求、不读取 Cookie、不写平台限频数字；页面抓取通过 `PageFetcher` 接口注入，真实适配器仍被 Goal 0B/6A 阻塞。
- 变更边界：新增 `internal/collection/schedule.go` 与 `schedule_test.go`；把 collection 域错误哨兵与 `TargetTransition`/`SummaryPageCommit`/`CatalogPageCommit`/`AttemptWrite` 的正典定义收回 `internal/collection`，`internal/storage/postgres` 改为类型别名保持 `errors.Is` 语义；storage 新增 `CommitCatalogPage`（与 `CommitSummaryPage` 同等 fence、幂等、页序与游标衔接）及其真实 PG 集成测试；`internal/resource` 新增 `ErrResourceUnusable` 区分“资源不可用”与基础设施错误。没有常驻循环、HTTP、恢复逻辑或 `go.mod` 变更。
- 调度形状：`Scheduler.RunCycle` 单次评估全部到期目标（目录按独立开关与周期、摘要按方向开关与目录游戏列表展开），不同目标经 `MaxParallelRuns` 信号量并行，同一目标的顺序游标流保持串行；每个页面独立完成“取租约 → 限频准入 → 抓取 → 同事务提交 → 释放”，逐页释放组合，部分成功保留已提交页并按运行结果记录 completeness。
- 失败处置：限频拒绝 → 结束运行为 cooldown 并 waiting 到 `RetryAt`；策略缺失 fail closed → manual blocked；登录失效 → manual blocked 等待人工恢复；超时/网络 → RunFailed(transient) 下一周期新建运行；429 信号经 `admitter.Report` 回写冷却；周期内目标被禁用 → 运行 stopped(cancelled) 并 detached，写回由禁用方所有。出口验证过期或重新验证中的组合在取租约时被 `ErrResourceUnusable` 拒绝；出口变化后限频身份从活动租约的当前出口派生，测试断言旧 IP 键不再出现。
- 对抗审查修复：应用时钟落后数据库时钟时，页面 `collectedAt` 与每个 attempt 的观测时间都以 `run.StartedAt()` 为下界钳制，避免合法页面或摘要 attempt 被判 `ErrInvalidInput` 形成永久失败循环；`targetDue` 对 actual=running 返回 true，处置写回失败或进程中断不会把目标钉死在 running（若原运行已终结会立即新建运行重跑，宁可提前重采）；`applyDisposition` 的 CAS 冲突显式回报 `Superseded` 而非静默吞掉；`acquireLease` 区分占用、不可用与基础设施错误，后者映射 waiting(transient) 而不是误报 blocked(egress_unavailable)；`BeginRun` 的禁用/冲突与 `CreateRun` 一致 detached；多运行错误用 `errors.Join` 汇总。二轮复核确认 running 重派发经存储层活动运行去重与部分唯一索引兜底不会产生重复运行，`collectedAt == started_at` 在域层与 PG CHECK 全链路通过。
- 验收：26 个调度器测试覆盖等待资源、冷却续点、登录失效、超时、资源独占与逐页释放、并发容量上限、出口变化、页面与 attempt 时钟偏斜、running 卡死自愈、中途活动运行续点、周期取消与最差处置聚合。`go test -count=1 ./...`、`go test -race`（collection/resource/storage）、`go vet ./...`、`go build ./...`、gofmt 均通过；真实 PostgreSQL 16 下 storage 普通与 race 各一轮通过，`CommitCatalogPage` 集成测试实际执行。
- 后续边界：本 Goal 只保证单进程单周期语义，常驻循环、关闭恢复与运行接线归 Goal 7C；周期串行前提未在代码中强制（无互斥），并发调用 RunCycle 会被页面 fence 拦下但可能把目标误判为 error(state_integrity)，Goal 7C 接线时必须保证单实例串行；旧开关版本的活动运行若被中断的停止流程泄漏，目标会停在 running 且每周期 detached，依赖 Goal 7C 的重启恢复兜底；单次限频拒绝会阻塞整个目标（不逐组合重试其他准入身份），作为已知保守行为留待后续优化；`CommitCatalogPage` 与 `CommitSummaryPage` 存在约 130 行结构性重复，接入第三个任务类型前应抽公共事务骨架。

### 2026-08-11 Goal 7C（常驻循环、停止与恢复）

- 输入与前置条件：基于 Goal 7B 的单周期调度器与 Goal 7A 的目标/运行状态机。本 Goal 没有发送真实平台请求、没有读取 Cookie、没有新增平台适配器，页面抓取仍由 `PageFetcher` 注入；HTTP/API 与运维入口归 Goal 7D 之后。
- 变更边界：新增 `internal/collection/daemon.go` 与 `daemon_test.go`，其中定义 `DaemonStore`（在 `ScheduleStore` 之上增加 `ActiveRuns`）与 `InstanceGuard`/`InstanceLock` 端口；`internal/storage/postgres` 实现 `ActiveRuns` 与 `AcquireInstanceLock` 并补两个真实 PostgreSQL 子测试。没有新增迁移、没有修改既有表结构、没有改动调度器的单周期语义。
- 常驻形状：`Daemon.Run` 先取得单实例锁，再执行一次启动恢复，然后按 `Interval` 循环执行 `Scheduler.RunCycle`，睡眠可被 ctx 立即打断；周期结果通过可选 `Observer` 同步上报。每个周期开始前复验实例锁。单个周期失败只记录在 `DaemonCycle.Err`，不终止常驻循环——失败语义由目标自身状态表达，下一周期照常重试；取锁失败、锁失效与恢复的全局读取失败都返回错误终止。
- 单实例独占：进程内 Coordinator 的账号与节点占用只在单个实例内有效，双进程会同时占用同一账号加节点并使恢复前提失效，因此 `Daemon.Run` 必须先通过 `InstanceGuard` 取得独占权，取不到时返回 `ErrInstanceLocked` 拒绝启动，退出时用不受取消影响的 context 释放。PostgreSQL 实现使用按 `current_schema()` 命名的会话级 advisory lock 并独占一条池化连接；锁键在取锁时定格为 `int64` 并在之后的校验与释放中作为参数传入，SQL 里不再做位移与 `oid` 转换，也不会因会话 `search_path` 变化去操作另一把锁；释放时显式 `pg_advisory_unlock` 后才归还连接（会话级锁不随连接归还而失效）。**同机进程退出时服务端立刻收尸，但主机宕机或网络分区时锁要等 TCP keepalive 预算耗尽才释放**——PostgreSQL 16 默认 `tcp_keepalives_*`、`tcp_user_timeout`、`idle_session_timeout` 全为 0，回落到 Linux 默认约 2 小时 11 分，期间接替进程每次启动都拿到 `ErrInstanceLocked` 直接退出，采集完全停摆。装配层必须在 DSN 显式设置 `tcp_user_timeout` 与 keepalive 把这个上界压到秒级，否则单实例保证的代价是不可接受的故障恢复时间。锁可能在进程无从知晓的情况下丢失，因此每周期开始前与关闭收敛写库前都用 `InstanceLock.Verify` 复验，失败即停止一切写入并退出（关闭收敛随之跳过，目标留在 `stopping`）；复验受 `LockVerifyTimeout` 约束，避免半开连接把常驻循环阻塞到内核超时。这两条行为都由删除后会失败的测试钉住。**复验必须回读真实归属而不是探测连接可达性**：连接池对服务端会话执行 `DISCARD ALL`（内部调用 `pg_advisory_unlock_all`）会清掉锁而连接依旧可用，ping 在此场景返回成功——这是审查在真实 PostgreSQL 上复现出的假阴性。因此 `Verify` 在锁连接上查询 `pg_locks`，按 `pid = pg_backend_pid()` 与拆成 classid/objid 的锁键比对（两半都要掩到 32 位，`oid` 拒绝有符号键的负高半）。集成测试用 `pg_advisory_unlock_all` 在存活会话上制造锁丢失，钉住这条路径。部署上仍必须保持到 PostgreSQL 的会话级连接：事务或语句级连接池会让取锁本身失去意义。
- 停止语义：`Disable` 只把目标置为 `stopping`，每个周期结束后由 `reconcileStops` 在确认目标任务已退出后才收敛为 `stopped`，并兜底结束写回失败留下的残留运行（`switch_disabled`）；残留运行没能结束时不写回 `stopped`，目标留在 `stopping` 等待下次收敛。关闭时用不受取消影响的有界 context（`ShutdownTimeout`）再收敛一次，否则已受理的关闭会停在 `stopping`——而 `Enable` 要求 `stopped`，运维将既停不干净也无法重新启用。关闭一个方向或一个目录目标不影响其他方向、其他 `appid` 与目录同步；已提交页与其行情事实全部保留，页面级租约在每页结束时释放，周期结束时组件登记取消。
- 重启恢复：取得实例锁后本进程没有任何在途运行，因此数据库中调度器拥有的全部非终态运行（只含目录与摘要，详情运行没有目标也不由调度器派发，已在 SQL 层排除）都属于上一进程。按目标逐条判定处置：目标仍启用、开关版本一致且运行未超过 `MaxResumeAge` 的**保留续点**，由下一周期从存量游标继续；目标已禁用或开关版本已推进的以 `RunStopped/switch_disabled` 结束（开关落后说明这条运行是被运维的关闭终止的，不是崩溃）；目标缺失或超过续点寿命的以 `RunFailed/process_restarted` 结束。随后把 `disabled+stopping` 的目标补齐为 `stopped`；未能结束的残留运行会传给收敛逻辑，避免把仍有活动运行的目标写成 `stopped`。单条运行或单个目标的失败被隔离进 `RecoveryReport.Failures` 并继续处理其余对象，只有 `ActiveRuns`/`Targets` 这类全局读取失败才判为启动失败，避免一条无法结束的运行变成整个系统的启动毒丸。恢复结果通过 `RecoveryObserver` 上报，逐对象失败与被接管而跳过的目标（`SkippedTargets`）只在这里可见；周期路径上的跳过记在 `DaemonCycle.Skipped`，关闭路径的收敛结果也通过 `Observer` 发出最后一次 `DaemonCycle`。
- 续点寿命：`MaxResumeAge` 限制遗留运行可被续点的最大**总寿命**，自运行开始起算，包含有效采集时间与停机时间。偏移量型游标（Steam search render 的 `start=`）经历过长的时间跨度后会移位，运行既可能重复也可能漏采，不能再声称覆盖完整；超龄运行按 `process_restarted` 结束并从空游标重来。**这一个旋钮无法把停机时长与走查耗时分开**：要精确约束停机，需要在运行行上记录最后一页的提交时刻（当前 `Run` 只有 `CreatedAt/StartedAt/FinishedAt`，`ActiveRuns` 也不带出该时刻），属于加字段的持久化改动，留待接线时按真实走查耗时决定是否需要。因此取值必须大于一次完整走查的耗时，否则长目录在每次重启后都会被判超龄、丢弃全部已提交页，恰好废掉续点本身的价值。判定用应用时钟与数据库写入的 `started_at` 比较，只在小时/天级阈值下成立，不适合压到分钟级。
- 验收：24 个常驻测试覆盖重复周期与干净退出、关闭方向后另一方向与目录继续、关闭目录不影响其他游戏与摘要、运行中途关闭保留已提交页并释放占用、临时失败下一周期新建运行、中断运行保留续点且不重采已提交页、超龄运行被结束、开关已推进的遗留运行被结束且目标可重新建运行、遗留运行处置规则的四个分支（含详情运行不归调度器、目标缺失）、中断的关闭在恢复时补齐为 `stopped/switch_disabled`、单目标恢复失败被隔离且不误标 `stopped`、恢复结果上报观察者、被接管目标记入 `SkippedTargets` 与 `DaemonCycle.Skipped`、残留运行兜底结束后才收敛、结束失败时目标留在 `stopping`、关闭路径补齐待收敛的关闭、`Targets`/`ActiveRuns` 两条全局读取失败均即启动失败、调度失败与收敛失败**分别**被记入 `DaemonCycle.Err`（用只失败首次读取与只失败 `ActiveRuns` 两个钩子把两条路径拆开，避免一个测试掩盖另一条路径的回归）、实例锁失效即退出、第二个实例被 `ErrInstanceLocked` 拒绝且退出后接替者可启动。`go test -count=1 ./...`、`go test -race -count=3 ./internal/collection`、`go vet ./...`、`go build ./...`、gofmt 均通过；真实 PostgreSQL 16 下 storage 普通与 race 各两轮通过，活动运行、重启结束与实例锁子测试实际执行——并借此发现并修正了 `ActiveRuns` 误用 `state` 列名（实际列为 `status`）、以及归属校验 SQL 未掩高 32 位导致 `oid` 越界两个缺陷。变异验证逐条执行并全部被杀：`Verify` 换回 ping 实现（集成测试失败）、删除 `runCycle` 的调度错误传递、删除收敛错误传递、删除 `stopTarget` 的冲突分支、删除超龄判定、删除关闭前复验、去掉复验超时。`advisoryLockParts` 另有不依赖数据库的边界表测试（含负高半与 int64 极值），因为集成测试的随机 schema 名只有约一半概率产生负高半键。
- fake 与真实存储对齐：`fakeScheduleStore` 原先在遗留活动运行开关落后时直接新建运行，而真实存储受活动运行唯一索引约束返回 `ErrConflict`——这让「必须结束旧开关运行」这条规则在测试里无法被证伪。已改为与真实存储一致返回冲突。
- 后续边界与已知取舍：跨进程续点的运行若最终跑完全部页面仍标记 `complete`，其覆盖证据是页面链的连续性而非快照一致性，首页与末页的采集时间可能相隔一次停机（上界由 `MaxResumeAge` 约束）；接受这一点是为了避免重启丢弃全部分页进度并成倍消耗最稀缺的限频预算。恢复不再改写 `running` 目标的状态：`targetDue` 对 `running` 返回 true，下一周期直接续跑，同时省掉一次依赖应用时钟的 `recheckAt` 写入。实例锁只在周期开始前与关闭写库前复验，周期内锁失效的残留窗口等于一个完整周期（长度受 `ResourceWait`、`PollInterval` 与分页数支配）；把 fence 下沉到每次页面提交需要存储层在写事务内校验锁归属，留待接线 Goal 评估。复验超时或取消会让 pgx 关闭底层连接，从而真的丢掉锁；取消恰好落在循环内复验期间时，关闭前的复验必然失败、`finalize` 被跳过，已受理的关闭停在 `stopping` 等下次启动收敛——方向上是 fail-safe（失去独占后不写库），但「关闭时再收敛一次」有这个缺口。归属判定是存在性检查而非引用计数：每把锁独占一条连接且只加锁一次，这个前提成立；将来若改为复用连接或重复加锁，必须改为计数判定。唤醒只按固定 `Interval`，到期时间早于间隔的目标最多多等一个间隔；连续失败没有指数退避，失败频率由 `Interval` 兜底。`executeRun` 对创建运行的 `ErrConflict` 判为 detached 且不写回状态：当前 daemon 自身到不了这个入口（残留运行未结束时不会写 `stopped`），但运维 API 一旦暴露状态修复入口就会打开，届时必须让它产生可观测错误，并提供 owner 授权的强制结束入口。`ActiveRuns` 依赖活动运行唯一索引天然有界，未设 limit。派发 goroutine 不做 panic 恢复：适配器 panic 会终止进程，由下次启动的恢复收敛状态。`waitInterval` 使用真实时钟。`DaemonConfig.Guard` 与调度器的 Store 在类型上无关联，装配层必须传同一份持久状态的栅栏，代码无法检测传错。`Observer`/`RecoveryObserver` 可为空且没有默认日志，接入 `internal/telemetry` 归运维接线 Goal。

### 2026-08-12 Goal 7D（本地 API 安全基础）

- 输入与边界：基于 Goal 7C 的常驻骨架建立本地控制面安全边界。本 Goal 没有实现账号、节点、采集控制或概览业务 API，也没有声称新 `internal/collection` daemon 已接入生产入口。
- 契约与实现：新增 `api/openapi.yaml`，公开无副作用的 `GET /api/security/context`；新增 `internal/api` 标准库 Handler。业务 POST 暂由下游 handler 注入测试，真实业务契约留给 Goal 7E～7H。
- Host 与 Origin：API 只有显式 `-api-listen 127.0.0.1:<port>` 才启动；监听地址只接受回环 IP 和明确 TCP 端口。请求 Host 必须与监听 authority 精确一致；默认测试 Handler 仅接受 loopback，拒绝公网域名、通配地址、错端口、尾点和畸形 authority。不信任 `X-Forwarded-Host`，出现 Origin 时必须与请求 scheme、host、port 完全同源；POST 缺少 Origin 直接拒绝。
- 请求与响应：全局只允许 GET/POST，显式拒绝 HEAD、OPTIONS、PUT、PATCH、DELETE 等方法；POST 要求 `application/json`（允许 charset 参数），否则返回固定错误码。响应不设置 CORS，统一写入 CSP、`nosniff`、`Referrer-Policy: no-referrer` 和 `X-Frame-Options: DENY`。
- 控制会话：安全上下文 GET 生成进程内高熵 session cookie 和独立 CSRF token，Cookie 为 host-only、`HttpOnly`、`SameSite=Strict`、`Path=/`、短期过期；POST 必须同时携带 cookie、专用 CSRF header 和同源 JSON。token 绑定 session、过期和跨 session 均用固定 `csrf_rejected` 拒绝，比较使用 constant-time；GET 不创建业务数据。当前回环 HTTP 不设置 `Secure`，TLS/远程访问留后续安全任务。
- 接线（本 Goal 当时状态，已被 Goal 2B 废止）：当时 `cmd/buffgo` 新增可选 `-api-listen`，默认仍走 legacy runtime；设置后在启动 legacy 前绑定 HTTP listener。Goal 2B 已删除 legacy，现行契约为**必填** `-api-listen` 的 API-only 进程。错误仅返回固定 code/error_ref，不回显请求数据或凭据。
- 验收：`go test -count=1 ./internal/api ./internal/app ./cmd/buffgo`、API/app race 测试、`go test -count=1 ./...`、`go vet ./...`、`go build ./...` 均通过；测试覆盖 Host/DNS rebinding、Origin、POST JSON、CSRF 缺失/错误/过期/跨 session、方法白名单、无 CORS、CSP、GET 无业务副作用和显式 loopback listener 校验。PostgreSQL 集成测试本轮仍因未设置 `BUFFGO_TEST_DSN` 未执行。
- 未完成与不确定：业务 API、OpenAPI 资源契约、Web 静态资源和 TLS 未实现。Steam `429` 仍只保留一次匿名观察，用户确认恢复通常较快但具体窗口和限制范围尚未验证。

### 2026-08-12 Goal 0B（Steam search/render 限流加压，部分）

- 输入：用户要求多测，并假设存在约 2 秒间隔 + 约 3 分钟窗口等多重限制。Cookie 本地 private JSON；日志不输出 Cookie 值。
- 变更边界：临时探测器只打 `search/render`；不解析正文、不进正式适配器。
- 结果（登录）：冷等 180s 后 0.5s×12、1s×12、2s×12、2.1s×100、背靠背×80、5 并行×10 ≈ **266 次全 200**，无 `Retry-After`，**未打出登录 429**。假设的 2s/3min 登录限频在本窗未成立。
- 结果（匿名对照）：同出口同接口首包 **429**（与 2026-08-11 一致）。
- 结论：匿名与登录限流不是同一套；不能把匿名 429 写成登录采集策略。登录上限若存在，高于本次加压。
- 文档：更新 `platforms/steam/*`、`todo.md` Goal 0B 注记。
- 未完成：登录上限数值、其它接口预算、字段/人民币验收。

### 2026-08-12 Goal 5D（凭据明文持久化）

- 输入与前置条件：用户明确推翻 Goal 5A 的库内加密与密钥外置决策，要求去掉文档/规则中阻碍装配的加密限制；本 Goal 先于 7E。
- 变更边界：新增 `000006_plaintext_credentials.sql`（清空 combinations/accounts/proxy 节点后改列）；Store 去掉 Cipher；删除 `internal/credential`；API 去掉 `credential_store_unavailable`；更新 ARCHITECTURE/REFERENCE/CONSOLE、todo Goal 5A/5D、AGENTS（禁止再以笼统安全为由阻塞明文凭据）。
- 保留：控制面 GET 仍不返回原始 session；日志脱敏边界不变。
- 产物位置：`migrations/000006_plaintext_credentials.sql`、`internal/storage/postgres/{store,resource,combination}.go` 及相关测试；无 `internal/credential/`。
- 验收：Go 零引用 `internal/credential`；`go test ./internal/storage/postgres ./internal/resource ./internal/api ./internal/ratelimit` 通过（无 DSN 时集成 skip）。
- 未确定事项：有已部署密文库时须跑迁移；未在本机带 `BUFFGO_TEST_DSN` 复跑集成。

### 2026-08-12 Goal 2B（移除 legacy runtime）

- 输入与前置条件：生产入口仍经 `app → internal/buffgo/run`；`collection.Daemon` 已实现但未装配；Goal 0B/6A 仍阻塞真实平台采集。本 Goal 未接线 collection，未读取 Cookie，未发送平台请求。
- 变更边界：整树删除 `internal/buffgo/` 与根目录 `configs/`；重写 `internal/app` 与 `cmd/buffgo` 为仅本地 API 控制面；`Options` 只保留 `APIListen`；必填 `-api-listen`，删除 `-config`/`-sources`/`-appid`。保留根 `testdata/` 作为平台负例证据。`go mod tidy` 移除 `go-redis` 与 `viper`，保留 `pgx`。
- 产物位置：删除后的仓库无 `internal/buffgo`；`internal/app/{app.go,app_test.go}`、`cmd/buffgo/{main.go,main_test.go,signal_test.go}`；文档更新见 `todo.md` Goal 2B、`docs/ARCHITECTURE.md` 入口说明。
- 启动契约：`-api-listen` 必填且必须为回环 IP:端口；帮助/协作取消退出 `0`，启动失败 `1`，参数错误 `2`。进程只服务 `api.NewHandlerForAuthority(nil, …)` 直到取消，有界 Shutdown。
- 验收命令与结果：Go 源码零引用 `internal/buffgo`、`go-redis`、`viper`；`go vet ./...`、`go test -count=1 ./...`、`go test -race -count=1 ./internal/app ./cmd/buffgo ./internal/api`、`go build ./...`、`go mod tidy -diff`、`./build.sh`、`gofmt -d` 均通过。无 DSN 时 storage 集成测试按既有门禁 skip。专项覆盖缺 `-api-listen`、空 `-api-listen=`、未知旧 flag `-config`、hostname/`0.0.0.0` 非法 listen 且不回显、API 就绪后取消退出、监听失败 `error_ref`、真实 SIGINT/SIGTERM。
- 产品回归（有意）：删旧后**无可运行采集**。不能把 API-only 进程表述为采集迁移完成。下一步装配采集属 Goal 7G；Steam 证据与适配仍属 Goal 0B/6A。
- 对抗收尾：已标明 Goal 7D「可选 api-listen + legacy」为废止历史；不切开 `storage/postgres → collection` 传递依赖（包依赖≠Daemon 已装配）。
- 未确定事项：Goal 7E 账号 API 的 Coordinator facade 与生产注入仍未完成；API Handler 仍以 `nil` next/accounts 装配。

### 2026-08-12 Web 页面先行（非 Goal，未接 API）

- 输入与前置条件：用户明确要求“先不做接口对接，先把页面做好”。注意：这与 CONSOLE.md 验收“首个页面接入真实 API，不使用演示数据”存在已知冲突，本阶段为页面设计前置，Goal 8A 验收仍以真实 API 闭环为准。
- 变更边界：新建 `web/`（Vue 3 + TS + Vite），六个页面（概览/采集控制/资源/运行/市场数据/规则与详情）对照 CONSOLE.md 页面边界；数据访问集中在 `web/src/api/` 门面，临时数据隔离在 `web/src/mock/`（接入后整目录删除）；所有 POST 操作明确报“未接入”，不伪装受理。构建产物输出到已被 Git 忽略的 `internal/webui/dist/`；`.gitignore` 补充 `node_modules/`。
- 产物位置：`web/{package.json,vite.config.ts,tsconfig.json,index.html}`、`web/src/{main.ts,router.ts,App.vue}`、`web/src/styles/base.css`、`web/src/api/{types.ts,index.ts}`、`web/src/mock/data.ts`、`web/src/components/`（ActualStateBadge、RunBadges、QuoteStateCell、ConfirmDialog、EmptyState）、`web/src/pages/` 六个页面。
- 验收命令与结果：`npm install`、`npm run build`（含 vue-tsc 类型检查）通过；dev server 下六个页面截图核验通过。子 agent 对抗性审查后修复：市场页平台列改为数据驱动（IGXE 不再缺席）、完整性/采集时间下沉到单元格、补齐 blocked 的 `nextCheckAt`、规则补触发依据、资源页补分配/解除与代理认证替换入口、占用拒绝带运行跳转、mock 修正 `succeeded+partial` 矛盾组合。
- 未确定事项：POST 全部未接入；无轮询机制（接 API 时在门面层统一加）；错误状态“保留最近成功数据+过期横幅”模式待接 API 实现；`createWebHistory` 需要 Goal 8B 嵌入服务配置 SPA fallback，否则刷新子路径 404；模态框缺 Esc/焦点管理。

### 2026-08-12 Web 页面重构（页面先行第二轮）

- 输入与前置条件：用户指出第一版六页职责重复（概览/采集控制/运行互相重复）、资源页账号节点混排、市场数据不可点开。经确认的结构决策：三页职责切开（概览=正在运行实时列表；采集控制=纯开关与周期配置；运行=历史批次与诊断）；节点增加长短效代理模型；市场数据用抽屉式商品详情。
- 变更边界：概览删除“需要关注/异常运行”区块，只保留进行中运行列表 + 阻塞目标分区；采集控制删除“最近运行”列；资源页改为按平台分区（账号/节点/组合归属平台工作区 + 未分配节点区），节点新增 `lifetime`（stable/sticky）、`stickyTtlMin`、`lastRotatedAt`、`rotationState`；市场数据行可点击打开右侧抽屉（各平台行情卡片、详情快照、同游戏映射失败）；CONSOLE.md 页面结构表同步更新。
- 第二轮对抗审查后修复：删除账号/节点前检查显式组合引用；分配平台/替换认证/替换会话改为带输入的弹窗；行情单元格补数量口径（单/件）；平台筛选收敛列；详情快照按 `productId` 关联；概览运行与阻塞拆分区；空态区分三分；查表函数加枚举兜底；表单全部 v-model；修正 mock 容量限制文案因果。
- 验收命令与结果：`npm run build`（含 vue-tsc）通过；概览/资源/市场/抽屉截图核验通过。
- 未确定事项：POST 仍未接入；节点长短效模型为前端先行定义，Goal 5A 节点验证语义落地时需对齐 `stickyTtlMin` 与 `valid_until` 的关系；CONSOLE.md 本轮改动需在下次文档审查时确认。

### 2026-08-12 概览再调整（页面先行第三轮）

- 输入：用户明确概览要回答“哪个平台采集开着、开的是求购还是出售、有没有在跑、用什么账号和节点跑（无代理即本机直连）”。
- 变更：`OverviewData` 改为按采集目标聚合（目录 + 平台×方向，仅已开启），每行含方向、实际状态、执行组合（账号 + 节点）、分页进度、阻塞原因与恢复方式；不再拆“运行/阻塞”两个分区。
- 验收：`npm run build` 通过；截图核验：运行中目标显示组合与页数进度，等待中显示下周期时间，阻塞显示原因。
- 资源页（按平台分区看账号与代理使用）与运行页（状态 + 已抓页数）经用户描述确认与现状一致，本轮未改。

### 2026-08-12 资源节点占用 + 运行执行单元

- 输入：节点跨平台共享出口、限频按平台+出口；执行单元=账号+出口 IP+平台。
- 变更：
  - 资源：节点详情补归属与按平台在用/空闲、同出口 peer 在途、跳转 runs；`nodeUsage` 用 `nodeId`。
  - 运行：`workerKey=account|ip|platform`，平台筛选、同出口其他工人/节点、抽屉节点信息。
  - mock：账号单请求一致；`occupiedBy` 与 live runs 对齐；hk-1/hk-5 共享出口；Run 补 `accountId`/`nodeId`。
- 验收：`npm run typecheck` 通过；浏览器点验资源 modal、runs 抽屉与同出口链路。
- 未确定：节点列表 busy 列；`occupiedBy` 仍是单 id 粗锁（多平台以 runs 为准）；历史 idle 工人会膨胀。

### 2026-08-12 运行页可读性重做

- 输入：用户反馈运行页不清楚、看不到。
- 变更：默认「在跑+异常」；按出口 IP 分组；行内放大游戏/方向/进度；同出口写明对端；失败上板；右侧固定详情不挡列表。
- 验收：`npm run typecheck`；浏览器可见 .11 两路对抢、失败 .12、进度 3/12。

### 2026-08-12 账号编辑 / 更新 Cookie

- 输入：账号可编辑登录账号+密码（列表不展示）；可更新 Cookie。
- 变更：列表仅别名 / 登录已配置 / Cookie 状态 / 占用；编辑弹窗写凭据不回填；Cookie 弹窗独立；占用中禁用 Cookie。
- 验收：`npm run typecheck`；资源页编辑弹窗与占用锁点验。

### 2026-08-12 代理：长短效 + 多商 + 三地域水位

- 输入：长效/短效（有效期内 IP 不变）；短效探测丢弃；多商；国内/国外/香港最少可用短效补齐。
- 变更：REFERENCE/ARCHITECTURE/CONSOLE/README 去掉粘性轮换；类型 `stable|short` + ProxyProvider + 水位；资源页短效池与代理商样机。
- 验收：`npm run typecheck`；资源页水位/代理商展示。
- 未确定：真供应商 API 与探测闭环后置。

### 2026-08-12 配置页独立

- 输入：短效池/代理商不必塞在资源页。
- 变更：新增 `/config` ConfigPage；资源页只留账号节点分配；导航增加「配置」。
- 验收：`npm run typecheck`；配置/资源分页点验。

### 2026-08-12 平台 × 节点地域

- 输入：Steam 禁国内代理；BUFF/IGXE 用国内/香港；国外不划国内站。
- 变更：文档矩阵；`platformRegion.ts`；资源分配过滤/拦截；概览 usable 含地域。
- 验收：`npm run typecheck`。

### 2026-08-12 资源列表重设计

- 账号一张表 + 平台筛选；节点筛选（地域/时效/划入）+ 属性标签列；分配改为表格式未划/游戏池/平台方向。
- 验收：`npm run typecheck`；资源页点验。

### 2026-08-13 Goal 0B

- 输入与前置条件：Steam 登录 Cookie 在 `platforms/private/steam/`；此前只有可达性/分桶，字段未钉死。
- 变更边界：只补 `platforms/steam/` 文档与脱敏实测，不写半套猜测适配器。
- 产物位置：`platforms/steam/`（search-render、orderbook、rate-limit 等）。
- 验收命令与结果：文档结论已写入：稳定键 `(appid, market_hash_name)`；ask=`sell_price`；bid 登录 `orderbook` `eCurrency=23` 的 `amtMaxBuyOrder`；坏 Cookie 采集 path 不跟随 302。
- 未确定事项：详情深度字段；冷却时长不可写成固定短间隔。

### 2026-08-13 Goal 5A 分配纠偏 / 7E / 7F

- 输入与前置条件：文档三层分配与代码 `assigned_platform` 冲突；生产 `accounts==nil`。
- 变更边界：迁移去掉节点平台独占；游戏配额 + 游戏×平台×方向；账号/节点/组合 API 经 Coordinator；占用中禁删改。
- 产物位置：`internal/storage/postgres/migrations/000007_*.sql`、`internal/app/control.go`、`internal/api/`、OpenAPI。
- 验收命令与结果：`go test ./internal/api ./internal/app ./internal/resource ./internal/storage/postgres`。
- 未确定事项：出口 IP 仍手工填写，自动探测未做。

### 2026-08-13 Goal 6A～6C / 7G / 7H

- 输入与前置条件：Goal 0B 已钉字段；摘要 unique `(appid, platform, side)`。
- 变更边界：`internal/platform/steam` 实现 `PageFetcher`；ask=`search/render`，bid=按目录扫 orderbook；Daemon 装配进进程；目标开关 API。
- 产物位置：`internal/platform/steam/`、`internal/app/collection.go`、`internal/api/collection.go`。
- 验收命令与结果：`go test ./internal/platform/steam ./internal/collection ./internal/api ./internal/app ./cmd/buffgo`。
- 未确定事项：本会话未用真实 Steam 账号做网页启停手工验收。

### 2026-08-13 Goal 8A / 8B / 9A～9E / 10A

- 输入与前置条件：控制面 API 已注入；网页曾走 mock。
- 变更边界：删 mock；CSRF 客户端不静默重放 POST；页面接真 API；Vite 产物嵌入 `internal/webui`；失败保留上次数据。
- 产物位置：`web/`、`internal/webui/`。
- 验收命令与结果：`cd web && npm run build`；`go test ./internal/webui ./internal/api`。
- 未确定事项：配置页水位/代理商仍未接入。

### 2026-08-13 Goal 11A（部分）/ 11D（未开始）

- 输入与前置条件：BUFF Cookie 在 `platforms/private/buff/`；IGXE 无 Cookie。
- 变更边界：BUFF 只写摸底文档；限流/空买卖/非 CS2 game 码未过门，不写适配器。IGXE 未发请求。
- 产物位置：`platforms/buff/`、`platforms/igxe/README.md`。
- 验收命令与结果：`go test ./platforms/buff ./platforms/igxe`。
- 未确定事项：11B 在限流与空数据验证前禁止开工。

### 2026-08-13 Goal 12A（部分）

- 输入与前置条件：调度已拒绝 detail target。
- 变更边界：`GET /api/capabilities` 固定 `detail_unavailable`；规则页只读该标志；不创建详情运行。
- 产物位置：`internal/api/handler.go`、`web/src/pages/RulesPage.vue`、OpenAPI。
- 验收命令与结果：`go test ./internal/api -run Capabilities`。
- 未确定事项：规则引擎与 12B 详情字段未做。

### 2026-08-13 Goal 13A（未完整勾选）

- 输入与前置条件：阶段 0～7 代码路径已落地。
- 变更边界：更新 `todo.md` / `docs/ARCHITECTURE.md` 过时描述；诚实记录缺口。
- 产物位置：本记录。
- 验收命令与结果：前端构建 + 相关 Go 测试。未做真实 Steam 网页启停、429、杀进程恢复的手工证据；BUFF/IGXE 无采集；详情按设计不可用。
- 未确定事项：13A 完整勾选依赖真实平台手工验收。

### 2026-08-13 Steam 首采闭环

- 输入与前置条件：Steam Fetcher 与控制台已装配；新建账号 `unverified` 占不到租约，调度误报 `egress_unavailable`。BUFF/IGXE 暂停。
- 变更边界：`ValidateForUse` 放行 unverified、拒绝 invalid；`acquireLease` 区分会话与出口；控制台会话中文与阻塞短句；API 拒绝国内节点划 Steam。不新增手工标 valid 接口。
- 产物位置：`todo.md`、本节、`internal/resource/`、`internal/collection/schedule.go`、`internal/api/nodes.go`、`web/src/pages/`。
- 验收命令与结果：`go test ./internal/resource ./internal/collection ./internal/api ./internal/platform/steam ./internal/app ./internal/webui` 通过；`cd web && npm run typecheck && npm run build` 通过。真机启停未跑。
- 未确定事项：真机启停由使用者本机验收。

### 2026-08-21 采集恢复与代理出口收口

- 输入与前置条件：账号 × 出口 IP × 接口是 Steam 限频身份；跨平台独立；队列只补任务，实际吞吐由可用组合决定。
- 变更边界：手工出口改为原子确认，失败保留旧证据且 `unavailable` 可恢复；拒绝并迁移历史文档保留 IP；替换 Cookie 后自动恢复 `session_invalid` 目标；429 首次反馈即展示策略冷却截止；补真实子进程 `SIGKILL`、owner epoch、claim generation 恢复验收；补代理商与水位 PostgreSQL 集成测试并修正冲突映射。
- 验收命令与结果：`go test -race ./... -count=1`、真实 PostgreSQL `go test -race ./internal/storage/postgres -count=1`、`go vet ./...`、`npm --prefix web run typecheck`、`./build.sh` 全通过；本机新版本已启动，控制台桌面/移动端点验通过，非法出口不会覆盖原证据。
- 未确定事项：当前有效 Steam Cookie 与精确桶真实 429 尚缺，固定 2 秒 / 60 秒仍是运行参数而非已证明极限；代理商拉号、可信出口探测、长效/短效生命周期等待供应商协议与逻辑代理槽位决策。

## 历史记录

### 2026-08-12 Goal 7E（账号管理 API，进行中）

- 当时切片：账号安全 DTO 与 GET/POST 路由草稿。生产装配与 Coordinator facade 在 2026-08-13 完成，见下方 Goal 7E 验收。

> 以下内容只说明当时发生的工作，不代表目标架构中的对应能力已经完成；后续验收以 Goal 记录为准。

| 日期 | 内容 |
|------|------|
| 2026-08-10 | 库层能力落地 |
| 2026-08-11 | 常驻 + 运维后台方向；文档三份 |
| 2026-08-11 | 定单机单进程；扩吞吐靠代理；整理架构/目录约定 |
| 2026-08-11 | 修复核心目录跟踪、跨币种比价和任务禁用语义 |
| 2026-08-11 | 收敛单进程任务循环，完成全量抓取与失败续页 |
| 2026-08-11 | 重新设计采集平台系统架构并重写 README |
| 2026-08-11 | 建立平台接口资料目录与开发 Cookie 存放边界 |
| 2026-08-11 | 核验 Steam 接口样本证据，重写架构、参考文档与实施计划 |
| 2026-08-11 | 确定 Go + TypeScript Web 控制台，整理文档边界与分阶段 Goal 计划 |
