# Ydisks Enhanced 优化路线图

> 本文件是 `chenlei-gh/Ydisks-chen` 后续优化的项目级执行清单。所有优化按本文顺序推进；代码、测试和实际平台行为必须分别举证。本文记录的是待验证工作计划，不代表相关缺陷已经确认，也不代表任何测试已经通过。

## 项目目标与边界

在保留上游 Ydisks 现有业务能力和 Go / React / 数据库架构的基础上，优先提高自动发货、订单处理、商品发布和 AI 客服的可靠性、可恢复性与可验证性。

执行原则：

1. **先审计，后改代码**：先追踪真实调用链、持久化状态、数据库约束和现有测试；不能仅凭文件名、注释、README 或测试名称判定能力成立。
2. **复用优先**：优先扩展现有 coordinator、lease、checkpoint、repository、锁和恢复机制；没有证据证明现有机制无法承载前，不新建平行框架。
3. **结果必须区分三态**：
   - 已确认执行：只继续尚未完成的后续步骤，不重复执行已完成的外部副作用。
   - 已确认未执行：在前置条件仍成立时允许重试。
   - 结果未知：进入 `unknown / needs_review`，禁止自动重放可能重复的外部副作用，等待核对。
4. **不虚称 exactly-once**：只有平台提供并验证了幂等键或等价保证，才可声明外部操作 exactly-once；本地锁和数据库事务本身不能证明远端恰好执行一次。
5. **小步提交**：每个阶段限定范围，先补测试，再改实现；每项完成后记录 commit、测试命令、结果和未覆盖边界。
6. **不随意改变范围**：新发现先登记到“候选任务”；只有阻塞当前任务或涉及安全/数据完整性风险时，才说明证据并调整顺序。
7. **不在真实买家、真实订单或真实账号上做破坏性验证**；平台端到端测试必须单独标注环境、账号授权和验证边界。

## 状态定义

- [ ] 待办
- [~] 进行中
- [x] 已完成（必须附验证证据）
- [!] 受阻（必须说明阻塞原因和解除条件）
- [-] 经审计确认不需要做（必须记录依据）

## 执行顺序

### 阶段 0：建立可复核的基线

- [x] 0.1 建立本路线图，作为项目内唯一的优化顺序与状态记录入口。
- [x] 0.2 记录本次优化基线：上游仓库、个人 Fork、默认分支、开发分支、基线 commit SHA、CI Go / Node 版本。
- [x] 0.3 盘点现有测试入口和 CI 数据库测试环境，明确单元测试、真实数据库集成测试、平台 E2E 的边界。
- [x] 0.4 建立每阶段的证据记录格式：代码路径、关键调用链、数据库状态/约束、测试名称与命令、实际结果、未覆盖风险。

**验收标准**：基线信息可从仓库和 CI 复现；测试类型和边界清楚；没有把尚未运行的测试标成通过。

**基线记录（2026-11-08，GitHub 仓库核对）**：

- 上游：`Christ9038/Ydisks-Xianyu-Helper`，默认分支 `main`。
- Fork：`chenlei-gh/Ydisks-chen`，默认分支 `main`；开发分支：`ydisks-enhanced/p0-delivery-safety`。
- 上游 `main` 与 Fork `main` 当前均为 `081e7f56b5a50c8e98db5309042f0fa5bd8119b7`；本开发分支基于该 SHA，目前仅领先 1 个文档提交 `67d0a567ae5009dd310cc8fbb4aa541cfc2c7cec`（新增本路线图），没有业务代码改动。
- Go：`go.mod` 声明 `go 1.26.4`；CI 使用 `actions/setup-go` 的 `go-version-file: go.mod`。这是项目/CI 目标版本，不代表已测得本地安装版本。
- Node：CI 使用 Node.js 24；前端依赖锁文件为 `frontend/package-lock.json`。
- CI 工作流：`.github/workflows/ci.yml`。Go job 包含格式、注释、架构、OpenAPI、文档、vet、golangci-lint、`go test ./...` 和 server race 子集；另有真实二进制 + 临时 SQLite smoke。
- 数据库测试：普通 `newTestDB` 使用每测试独立临时 SQLite 文件；`allTestTargets` 默认 SQLite，配置 `TEST_MYSQL_URL` / `TEST_POSTGRES_URL` 时追加真实 MySQL / PostgreSQL。CI 的 multidb job 启动 MySQL 8.4 与 PostgreSQL 17，并通过 `make test-multidb` 设置 `REQUIRE_MULTIDB=1`，缺任一 URL 会失败。
- 测试边界：SQLite 仓储测试和多方言数据库测试不等于闲鱼平台 E2E；CI smoke 是真实服务二进制 + 临时 SQLite，不证明闲鱼外部接口真实发货。当前开发分支只有文档提交，尚无本分支代码测试结果；本次审计没有声称测试已通过。
- 证据来源：`go.mod`、`frontend/package.json`、`.github/workflows/ci.yml`、`Makefile`、`internal/db/repository_test.go`、`internal/db/multidb_test.go`。


### 阶段 1：P0 自动发货与补发安全审计（先审计，不预设要重写）

重点核对现有实现，尤其是以下已发现的代码入口（仅为审计入口，不代表缺陷已确认）：

- `internal/automation/run_coordinator.go`
- `internal/automation/run_lease.go`
- `internal/automation/run_uncertainty.go`
- `internal/automation/delivery_replay_plan.go`
- `internal/db` 中的自动化运行、订单并发、卡密库存与故障注入实现
- 手动补发 API / application service 到 repository 的完整调用链

待办：

- [x] 1.1 绘制自动发货端到端调用链：事件接收 → 订单/规则校验 → 运行创建或恢复 → 动作领取 → 卡密准备 → 消息发送 → 平台发货确认 → 本地检查点 → 终态与通知。
- [x] 1.2 绘制手动补发调用链：入口鉴权与归属校验 → 订单锁/运行锁 → 补发计划 → 库存操作 → 外部发送 → 状态收口。
- [x] 1.3 验证自动发货与手动补发是否使用同一订单级互斥边界；检查所有入口是否都经过该边界，是否存在绕过路径。
- [x] 1.4 审计执行租约：初次领取、续租、过期、执行代次/fencing、进程崩溃、数据库暂时不可用、旧 worker 恢复后继续执行等情况。
- [x] 1.5 审计每个外部副作用前后的持久化顺序，确认“发送前失败”“发送成功但响应丢失”“远端成功但本地提交失败”分别如何分类。
- [x] 1.6 审计卡密库存预占、消耗、释放和补取；验证并发订单、重复事件、补发重入和事务失败时不会重复售出或错误回滚。
- [x] 1.7 审计 `unknown / needs_review` 的持久化、后台恢复扫描、管理端可见性、人工核对入口和最终收口；验证未知结果不会被普通重试路径重新发送。
- [x] 1.8 审计动作检查点与任务快照：进程在每个关键边界退出后，恢复是否从正确动作继续，是否可能跳过未完成动作或重复已完成动作。
- [x] 1.9 审计现有自动化测试是否真正验证数据库并发、事务回滚、进程中断和恢复；区分 mock 测试与真实数据库测试。
- [x] 1.10 输出 P0 审计报告：按“已证实成立 / 已证实缺口 / 证据不足 / 不适用”分类，列出最小修复项和对应验收测试。

**阶段门槛**：完成审计报告前，不进行大规模重构；若现有实现已满足某项要求，将其标记为已覆盖并附证据，不重复开发。

**审计结果**：详见项目根目录 [`P0_AUDIT.md`](P0_AUDIT.md)，提交 `c32c8ce84c8a5b9ffb00b8172f19c8ded7eba0ae`。已确认现有订单锁/租约/检查点/未知结果隔离机制，不需要另建协调框架；发现 `ReleaseBatchData` 重复释放可能重复恢复库存（P0），以及人工补发成功收口缺少 reserved 台账兜底结算（P1）。审计期间未修改业务代码，未声称测试通过。

### 阶段 2：P0 自动发货与补发缺口修复

**当前修复批次**：P0 重复释放库存与 P1 人工补发台账结算已提交；新增 SQLite/多方言回归测试。最新提交 `9e43bbd8e9b14f160dea394cde8f8d0e8adae8ca` 的 PR CI（run `42479315981`，2026-11-08）已全部通过：Go 完整测试与 server 并发 race、MySQL 8.4/PostgreSQL 17 多数据库回归、React 前端检查/测试/构建、真实服务二进制 + 临时 SQLite smoke 均成功；Go 格式、注释、架构、OpenAPI、文档一致性、vet、golangci-lint 也全部通过。通知模块拆分后的完整复验已完成。此结果不等于闲鱼真实账号/订单平台 E2E。当前 PR：[#1](https://github.com/chenlei-gh/Ydisks-chen/pull/1)，仍 open、未合并。


仅实施阶段 1 已证实的缺口。候选范围：

- [x] 2.1 自动发货、人工补发、后台恢复之间的订单级互斥和执行权一致性（主路径已核实；`TestMultiDB_AutomationPaidRunCrossTriggerConcurrency` 已由 CI 多方言回归通过）。
- [x] 2.2 未知远端结果的隔离与人工核对，防止不安全自动重放（现有机制已覆盖，证据见 P0_AUDIT.md）。
- [x] 2.3 卡密库存预占、消耗、释放、补取的原子性和可恢复性（重复释放修复及重复/并发回归测试已由 CI 多方言回归通过）。
- [x] 2.4 动作检查点与终态收口的一致性（人工补发成功收口 reserved 台账兜底结算及故障注入回归已通过 Go 测试）。
- [x] 2.5 补齐本轮范围内的并发、重复释放、补发收口及数据库故障回归测试（完整 Go 测试、server 并发 race 和 SQLite/MySQL/PostgreSQL CI 矩阵已通过；真实平台故障/交易行为仍未验证）。

**验收标准**：

- 相同账号和订单的自动发货、人工补发及恢复任务不能并发执行冲突副作用。
- 已确认成功的消息/卡密不被重复发送或重复消耗。
- 已确认未执行的动作可以在条件满足时安全重试。
- 结果未知时进入可查询、可人工处理的状态，不自动重复发送。
- 数据库事务失败、进程中断和 worker 失租后，恢复行为有测试证明。
- 不把 mock-only 测试描述成真实数据库或平台端到端验证。

### 阶段 3：统一外部操作状态与执行权（按审计结果决定是否需要抽象）

- [ ] 3.1 盘点发货、消息发送、商品发布等外部操作是否已有可复用的状态模型。
- [ ] 3.2 对照以下语义检查现有模型是否表达充分：`not_started`、`started`、`remote_confirmed`、`local_confirmed`、`unknown`。
- [ ] 3.3 只有当多个业务域确实存在重复且不一致的状态处理时，才考虑提取共享协调能力；优先复用现有 DB / application 层，不为抽象而抽象。
- [ ] 3.4 明确 lease token、attempt/generation、过期接管和旧 worker fencing 的责任边界。
- [ ] 3.5 为状态迁移、重复回调、超时和本地提交失败建立表驱动测试。

**验收标准**：每个状态都有明确进入条件、允许的后继状态、恢复策略和可验证持久化证据；不引入无法证明必要性的新服务层。

### 阶段 4：商品发布与批量铺货可靠性

- [x] 4.1 审计单商品发布、批量任务、逐行结果、图片上传、规格发布和任务恢复的调用链（静态调用链复核完成；单商品账本已合并，批量行检查点/接管路径已审阅）。
- [x] 4.2 核对远端创建成功但本地响应/提交失败时，是否能识别未知结果并防止重复创建（PR #7/#9：持久化操作账本、unknown 隔离、远端确认后本地恢复；CI 通过；平台端 exactly-once 仍不作保证）。
- [x] 4.3 核对批量任务每一行的独立状态、重试边界、失败隔离、断点续跑和结果导出（PR #16 补齐 worker 层 `post_publish` 租约接管到最终收口的集成回归；CI 通过，见下方记录）。
- [x] 4.4 核对图片/附件临时文件的生命周期、大小限制、失败清理和重复上传风险；已修复本地批次目录删除失败时数据库路径丢失的问题（PR #8，最新 head CI 4/4 通过）。远端闲鱼图片回收 API 未证实，仍不做猜测性清理。
- [ ] 4.5 在现有机制不足时，补充远端操作状态、逐项恢复和并发目标锁。
- [x] 4.6 增加批量部分成功、重复提交、超时、worker 中断、数据库提交失败等测试（现有 DB/adapter 回归测试覆盖；PR #8 全量 Go CI、PR #13 定向 Go CI 通过；真实平台 E2E 不在此结论内）。

**验收标准**：单项成功不被重复创建；单项失败不会污染其他行；未知结果不会盲目重发；恢复结果可追溯到原批次和原始输入。

#### 阶段 4.7：单商品发布持久化操作账本（PR #7）

- [x] SQLite / MySQL / PostgreSQL 增加单商品发布操作表与用户归属查询。
- [x] 应用层将最终创建请求闸门接入 prepared -> started；远端成功先记录 remote_confirmed，本地 Upsert 后记录 local_confirmed。
- [x] 闸门通过后遇到不确定结果转入 unknown；同一 operation ID 不允许自动再次创建。
- [x] 服务端按规范化商品字段、规格和图片内容 SHA-256 计算请求指纹；同一 ID 绑定不同指纹时拒绝。
- [x] HTTP 接收稳定 operation ID，并提供当前用户隔离的操作状态查询。
- [x] 前端在 sessionStorage 中按账号和完整表单指纹复用 operation ID；不保存图片或 Cookie。
- [x] 增加应用层未知结果隔离、远端确认后本地恢复、指纹复用冲突测试。
- [x] CI 通过 Go / 前端 / API 契约 / 多数据库回归门禁（PR #7 head `2cb9352fd3fe7de8ebd6ab35d37ed3908a4e8a99`，run `42542832914`，4/4 jobs success；合并提交 `48d77e9aba1a29da0b7ce64992bfaaa81c3f7831`）。
- [ ] 真实闲鱼平台行为验证（当前按要求暂停，不作为本 PR 合并前置条件）。

**边界说明**：started / unknown 只表示最终创建请求可能已发出，不等于远端 exactly-once；没有平台幂等键或按客户端 ID 查询能力时，未知结果必须人工核对。图片上传和类目准备发生在最终创建闸门之前，预闸门失败可能留下孤立上传资源。前端刷新恢复需要用户重新选择相同图片并恢复相同表单；sessionStorage 只保存操作 ID，不保存图片字节。

### 阶段 5：AI 客服能力增强（吸收有价值的 Fork 能力，独立审计后实现）

此前对独立 AI Fork 的比较发现了值得借鉴的方向，但不直接 cherry-pick；逐项与当前主线实现比较后决定是否吸收：

- [x] 5.1 商品级 AI 策略：已审计商品级启停/继承、专属上下文、归属校验、AI 回复覆盖和前端管理入口；现有实现覆盖目标，本轮没有证据支持重复开发。
- [~] 5.2 买家图片理解：PR #15 已实现可选视觉模型、图片消息提取、下载与格式/大小/像素限制、文本降级及图片不可信规则；Go、多数据库和冒烟 CI 已通过，前端构建产物同步仍阻塞最终验收与合并。
- [~] 5.3 图片网络安全：已核对 PR #15 下载器实际调用 `netguard.PublicHTTPClient`，并审阅 DNS 解析后直连已验证 IP、私网/回环/主要保留网段拒绝、禁用连接复用及同主机重定向策略；DNS rebinding 定向回归已由 PR #17 合并并通过 CI，买家图片下载入口的跨主机重定向定向回归已补入 PR #15，待该 PR 最新 CI 验证。
- [~] 5.4 人工接管：审计已确认当前没有会话级人工接管/恢复状态或聊天页开关；需设计最小会话级状态与在途 AI 回复的竞争处理，暂不直接加一个仅前端有效的开关。
- [ ] 5.5 会话上下文：把人工回复纳入上下文；建立消息条数、字符数和 token 预算、摘要与截断优先级。
- [ ] 5.6 AI 重试：仅对可恢复错误进行有限重试；处理 429 / 5xx / 网络错误、取消和整体超时；避免图片失败降级后再重试导致调用次数超预算。
- [ ] 5.7 成本与调用预算：按会话/消息限制模型调用次数、总耗时和 token；记录可审计的使用量，不记录敏感内容。
- [ ] 5.8 Prompt 注入边界：把买家文本、图片 OCR/视觉描述、商品资料和外部上下文作为不可信数据处理，明确系统指令与用户内容边界。
- [ ] 5.9 为图片、人工接管竞争、超长上下文、重试次数、预算耗尽和取消行为补齐测试。

**验收标准**：人工接管具有并发安全边界；图像下载具备可验证的 SSRF 与解码资源限制；AI 调用次数/预算有硬上限；上下文长度可预测；失败降级不会无限重试或静默越权。

### 阶段 6：系统级故障验证与发布前检查

- [ ] 6.1 汇总自动化、库存、订单、商品发布、AI 和数据库的故障矩阵。
- [ ] 6.2 对关键持久化边界执行进程中断/重启恢复测试。
- [ ] 6.3 在真实支持的数据库（SQLite / MySQL / PostgreSQL）上验证关键事务和并发语义；未覆盖的方言明确标注。
- [ ] 6.4 验证 worker 多实例、租约过期、旧执行者恢复、重复事件和重试竞争。
- [ ] 6.5 运行 Go、前端、lint、vet、构建和 CI 检查；记录命令、环境、退出码和日志摘要。
- [ ] 6.6 形成发布候选报告：已验证、未验证、已知限制、迁移/回滚风险。
- [ ] 6.7 需要平台真实验证的项目单独列出前置条件；未获得明确授权或安全测试环境时不执行真实交易操作。

**验收标准**：发布判断基于可复现证据；不以“测试文件存在”或“本地编译成功”替代运行验证；所有未覆盖边界明确披露。

## 候选任务池（不自动插入当前执行顺序）

- [ ] 研究其他 Fork 中尚未合入上游的独立改进，记录 commit、代码差异、测试质量和兼容性；不做整仓替换或无差别 cherry-pick。
- [ ] 评估管理后台操作审计、数据导出与运维体验的改进。
- [ ] 评估部署、安全默认值和敏感信息治理的改进。

候选任务只有在说明收益、证据、影响范围和优先级后，才可进入正式执行顺序。

## 每项任务的完成记录模板

每完成一个编号任务，在对应条目下追加：

- **结果**：完成 / 部分完成 / 不需要 / 受阻
- **代码**：涉及文件与关键函数
- **提交**：commit SHA / PR（如有）
- **验证**：执行命令、环境、测试类型、通过/失败数量
- **证据**：测试输出、CI 链接或可复现步骤
- **剩余风险**：未验证的数据库、平台行为或边界

> 更新本文件中的状态必须与代码和验证证据同步。不得仅因实现已提交就标记为完成。


### 2026-11-08 云端验证与通知模块拆分记录

- 首轮全量 `go test ./...`（commit `af7bdf2`）：所有业务包测试通过；唯一失败为 `tools/architecturecheck` 的 `TestStageTwoRealSourceGate`，明确指出 `internal/notify/notifier.go` 为 808 行。该测试失败使后续 race 步骤未执行。
- 同轮 `gofmt` 修复提交 `b1055dd`；`go vet ./...`、`golangci-lint ./...`、注释检查、架构命令、OpenAPI 和 docs 一致性检查均通过。注意：架构命令通过不等于架构包中的真实源码门禁测试通过。
- 已按职责拆分 `notifier.go`：事件级别/类型文案、事件格式化、订阅过滤、事件类型解析与排序函数迁入 `internal/notify/notifier_events.go`；主文件保留通知编排和发送入口。拆分后 `notifier.go` 为 552 行，新文件为 262 行（含 package/import/空行）；未修改通知语义。
- 后续 commentlint 发现新文件首个私有函数缺少中文注释，已补充 `levelLabel` 注释。当前拆分后完整 Go 复验仍待运行；race、PR 全矩阵和多方言结果以最新 CI 为准。
- 关键提交：`57b1283`（提取事件逻辑）、`20b283b`（缩减主文件）、`690df5d`（gofmt）、`2cac647`（补齐注释）。PR 保持 open，未合并；`main` 未修改。


### 2026-11-08 最新提交最终验收记录

- 最新 head：`9e43bbd8e9b14f160dea394cde8f8d0e8adae8ca`。
- PR CI run：[`42479315981`](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42479315981)，结论 `success`；四个 job 全部成功：冒烟测试、MySQL/PostgreSQL 多数据库回归、Go 检查、React 前端检查。
- Go job 的完整步骤全部通过：gofmt、中文注释、架构依赖、OpenAPI、文档一致性、`go vet`、`golangci-lint`、`go test ./...`、server 并发 smoke race。
- 本次 CI 对应最新 PR head；最终 head 相比前一个已完成完整复验的源码提交仅清理临时验证 workflow，没有额外业务源码变更。
- 验收边界：CI 证明自动化测试环境中的代码与数据库行为通过，不证明闲鱼真实账号/真实订单平台 E2E；本轮未执行真实买家、真实订单或真实账号操作。
- PR #1 仍保持 open、未合并；`main` 未修改。是否合并由仓库所有者另行决定。


### 2026-11-08 阶段 3 / 阶段 4 交叉审计记录

- 审计报告：[`docs/EXTERNAL_OPERATION_AUDIT.md`](docs/EXTERNAL_OPERATION_AUDIT.md)，基线 `4f066bb20962657f4969e9af819b47f65f3a7918`。
- **阶段 3.1 已完成（静态审计）**：自动发货、普通聊天文本/图片、单商品发布、批量商品发布的状态记录与恢复边界已逐条核对。
- **重要修正**：普通聊天不是“只有 WebSocket echo”。`internal/application/chat/send.go` 在发送前创建持久化 `sending` 消息，使用 MessageKey 关联，并收口为 `sent / failed / uncertain`；自动化发送额外等待 WS echo。图片上传与图片消息发送是两个独立外部副作用。
- **阶段 3.2 已完成（语义对照）**：各域可映射到 not_started / started / remote_confirmed / local_confirmed / unknown 的概念，但不要求字段同名。
- **阶段 3.3 已完成（决策）**：经审计不需要全局 ExternalOperationService。各域的订单 ledger/proof、消息记录/echo、批次行 lease/ItemID checkpoint 有不同业务约束；共享状态术语和测试场景即可。
- **阶段 3.4 已完成（责任边界）**：lease/token/attempt 只对本地 DB 写入提供 fencing，不构成远端 exactly-once。闲鱼远端幂等键/按请求身份查询能力尚未证实。
- **阶段 3.5 待完成**：需要把状态迁移、重复回调、超时和本地提交失败测试汇总，并在单商品发布修复后执行。
- **阶段 4.1 部分完成**：已审计单商品、批量发布、批次行恢复、聊天图片上传/发送边界；仍需补齐规格发布和临时图片文件生命周期审计。
- **阶段 4.2 已确认缺口**：单商品发布先调用 MTOP，获得 ItemID 后才本地 Upsert；没有远端调用前的持久化操作检查点。HTTP 已对已知 ItemID 的本地落库失败给出“勿重复发布”提示，但硬中断/响应丢失时缺少可恢复的本地身份记录。
- **阶段 4.3 部分完成**：批量行已有独立状态、worker token、remote_started、ItemID/post_publish 检查点；需补齐 post_publish 续跑和 lease takeover 的针对性测试证据。
- **阶段 4.4 待完成**：图片上传、附件临时目录清理和重复上传风险继续专项审计。
- **本次仅提交审计文档，没有新增业务代码，也没有运行测试**。真实闲鱼平台测试继续暂停。
- **下一批实现边界**：优先设计单商品发布的持久化操作身份/检查点和恢复；先核实 MTOP 是否存在可用幂等键或按请求身份查询能力。若平台不支持，则未知结果只能隔离人工核对，不得自动重发。随后与批量 post_publish 恢复测试、图片生命周期测试合并验证。


### 2026-11-08 单商品幂等能力与批量恢复二次核查

- **单商品请求身份核查**：`PublishItemRequest` 没有 caller-supplied operation ID；`publishItemOnce` 每次调用重新生成时间型 `uniqueCode`（Unix 微秒）。它不是稳定请求身份，当前没有证据证明其具备服务端去重语义。
- **平台回查能力核查**：仓库的 `FetchItemsPage` 只能分页读取卖家商品列表，没有按 `uniqueCode` / 客户端操作身份回查发布结果的实现。按标题等内容相似度自动匹配不足以安全决定重发。
- **决策**：单商品 MTOP 请求超时、响应丢失或进程硬中断后的结果应隔离为 `unknown/needs_review`；不得因“本地没记录到 ItemID”就自动重发。只有取得平台幂等/查询契约和可复现实验后，才重新评估自动恢复。
- **批量 post_publish 复核**：`ItemBatchPublishPort.PublishRemoteRow` 在发现已有 ItemID 时直接复用检查点；`TestItemBatchPublishPortPersistsRemoteCheckpoint` 断言恢复不会再次调用平台（调用次数仍为 1）。现有防重实现已存在，不重复造轮子；待补 worker 层 post_publish 租约接管与最终收口的集成测试。
- **图片资源边界**：主图/规格图先上传，再执行最终发布；上传成功不等于商品创建成功。发布前失败可能遗留孤立图片，当前未发现远端清理契约，后续按资源生命周期单独评估。
- **验证状态**：本次为源码与既有测试源码审阅，没有运行新测试，也没有真实闲鱼操作。PR #2 的文档 CI 4/4 已通过，但不覆盖上述平台行为。


### 2026-11-08 单商品发布持久化检查点实施前设计

- 核实 `PublishItemRequest.BeforePublish` 已是最终商品创建请求发出前的 hook，且触发时图片上传和类目准备已完成；批量发布已使用，单商品尚未接入。
- 设计原则：先持久化 `prepared`，hook 内再写 `started`，然后才发出最终创建请求；MTOP 返回 ItemID 后先写 `remote_confirmed`，再做 Cookie 写回和本地商品 Upsert，最后写 `local_confirmed`。
- operation ID 必须由客户端在一次发布意图开始时生成并在未知结果重试时复用；同 ID 不同请求 fingerprint 冲突拒绝。图片不落库，但其摘要必须参与 fingerprint。
- 远端请求已开始后遇到硬中断、网络未知、成功响应缺少 ItemID 或远端确认结果无法落库时，不得自动重发；同 ID 只能返回 `unknown/needs_review`。该 ID 不提供平台端 exactly-once 保证。
- 实施范围应一次性包含 SQLite/MySQL/PostgreSQL migration、DB 原子状态转换、应用/适配器 hook、HTTP/OpenAPI、前端 ID 生命周期、重复请求/故障注入测试；不新增全局协调服务。
- 本节为设计核查，不代表代码已实施或测试已运行。真实闲鱼平台测试仍暂停。


### 2026-11-08 批量发布租约接管回归测试补充

- 新增 `internal/db/item_publish_batches_recovery_test.go`：构造同一批次中两条不同远端阶段的明细，模拟旧 worker 租约到期和新 worker 接管。
- 覆盖 `remote_started` 且无 ItemID 的行必须转为 `failed/uncertain_remote`，普通 `ResetFailed` 不得重新打开该行。
- 覆盖已保存 ItemID/URL/raw JSON 的 `post_publish` 行在接管后回到可领取状态，保留远端检查点，并允许新 worker 完成 success 收口。
- 本测试是 SQLite 真实数据库层面的状态迁移回归，不是完整 worker + 闲鱼平台 E2E；多数据库覆盖与 Go 全量结果以新 PR CI 为准。

### 2026-11-08 单商品发布最终请求闸门重试保护

- `PublishInput` 新增 `BeforePublish` 回调，适配器将其传递到 MTOP 已有的 `PublishItemRequest.BeforePublish` 钩子。
- 适配器记录最终创建请求闸门是否已通过：闸门通过后，即使随后返回 Session 过期错误，也不再自动恢复凭证并重放完整商品发布流程。
- 闸门尚未通过时（例如图片上传或类目准备阶段遇到明确 Session 过期），原有一次凭证恢复重试仍保留；该路径不代表商品创建请求已发出。
- 新增回归测试模拟闸门通过后 Session 过期，验证平台发布方法仅调用一次。真实 MTOP/闲鱼平台行为未测试。
- 该回调为下一步接入持久化 `prepared -> started` 检查点预留应用层通道；当前 HTTP 流程尚未提供 operation ID，也尚未写入操作记录，因此不能宣称单商品发布的未知结果已完整闭环。


### 2026-11-09 单商品发布持久化账本合并验收

- PR：[#7](https://github.com/chenlei-gh/Ydisks-chen/pull/7)，已合并至 `main`。
- 合并提交：`48d77e9aba1a29da0b7ce64992bfaaa81c3f7831`；合并后的 `main` HEAD 与该 SHA 一致。
- PR head CI：run [`42542832914`](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42542832914)，Go 检查、MySQL/PostgreSQL 多数据库回归、React 前端检查、冒烟测试 4/4 全部成功。
- 合并后主分支附加工作流：Wiki 同步成功；GitHub Pages 部署失败，日志为 Pages deployment API 404，并提示需启用 GitHub Pages。该失败属于文档站点部署配置，不是 PR 核心质量门禁。
- 验收边界：CI 证明本地代码、数据库迁移和自动化测试通过；没有证明闲鱼真实账号/商品创建行为。真实平台验证仍暂停。
- 剩余风险：进程在远端商品创建成功后、写入 `remote_confirmed` 前硬中断，仍会留下 `started` 状态，必须人工核对；没有平台端幂等键/按 operation ID 查询能力，不得宣称远端 exactly-once。图片/类目准备在最终创建闸门之前，仍可能留下孤立上传资源。


### 2026-11-09 商品规格与图片资源生命周期审计

- **4.1 调用链复核**：单商品 multipart handler 将主图和规格图读入内存，转为应用图片模型；MTOP 先上传主图和规格图，再进行类目准备，最后经过 `BeforePublish` 闸门发出商品创建请求。多规格通过 `item_properties`、`item_sku_list` 与 `spec_images` 传入；服务端校验规格图索引，MTOP 层再次校验规格维度、值集合、SKU 笛卡尔积、价格/库存和图片索引。批量发布从持久化的批次图片目录读取主图/规格图，转换为相同 MTOP 请求结构；单行图片或规格解析失败只返回该行错误，由 worker 继续处理其他行。
- **本地资源生命周期**：批量预检先建立 `publish_batches/<batch_id>` 目录，预检未成功持久化时通过 defer 删除；预检成功后保留目录，供后台 worker 和失败行重试读取。用户删除非运行批次时删除目录；过期清理保留 7 天，只处理非 running/canceling 批次。
- **清理触发限制**：过期清理目前由批量预检 HTTP 请求触发，不是独立定时任务；若长期没有新的预检请求，超过 7 天的目录仍会留存。
- **已证实的清理缺口**：`ItemBatchRepository.DeleteUpload` 在 `os.RemoveAll` 失败后仍执行 `ClearUploadDir`。如果目录删除失败，数据库路径随即被清空，后续过期扫描无法再次定位该目录，形成无法自动重试的孤儿目录。单批次用户删除路径也先删数据库记录再删目录，文件系统删除失败时会留下失去数据库索引的目录。
- **远端资源边界**：MTOP 主图/规格图上传发生在最终商品创建闸门之前；后续类目失败、闸门拒绝或最终创建失败时，已上传图片可能成为平台侧孤立资源。仓库当前没有证据表明平台提供可靠的图片删除/回收 API，不能擅自自动删除或假设资源可回滚。
- **请求体与解压限制**：单品发布 multipart 总体限制 96 MiB；批量上传总请求 224 MiB，图片 ZIP 最多 500 个文件、单文件 10 MiB、解压总量 500 MiB；本地路径通过安全 ZIP 路径和受控根目录读取。
- **规格验证边界**：服务端验证图片索引范围，MTOP 层校验最多两个规格维度、至少两个非空且不重复的值、最多 1500 个组合、SKU 组合唯一且完整、正价格/库存及图片索引。现有测试覆盖多规格含规格图、单规格、图片读取失败、快照损坏及 SKU payload 构造；尚无真实闲鱼平台规格发布验证。
- **结论**：规格数据转换与校验具备本地防线；图片本地清理有可证实的重试缺口，远端孤立图片无已证实的回收能力。下一批优先做最小修复：删除失败时保留数据库路径供后续重试，并补充删除失败/重复清理测试；评估用户删除批次的文件系统失败收口，不引入全局资源生命周期框架。
- **验证边界**：本节为 GitHub 源码与既有测试审阅，没有运行新测试，没有真实平台上传或删除实验。


### 2026-11-09 批量上传清理失败可重试修复（PR #8）

- PR：[#8](https://github.com/chenlei-gh/Ydisks-chen/pull/8)，已 squash 合并至 `main`。
- 合并提交：`31939e4c9acd5162ecc1a3a449b7fa56f8cf7460`；合并后已核实 `main` HEAD 与该 SHA 一致。
- 最新 PR head：`c98168e90e70b8eab3664da8d7479d141e37cd7a`；CI run [`42546788164`](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42546788164)，Go 检查、React 前端检查、MySQL/PostgreSQL 多数据库回归、冒烟测试 4/4 全部成功。
- 修复 `DeleteUpload`：只有 `os.RemoveAll` 成功后才清空数据库中的 `upload_dir`；文件系统删除失败时保留路径，供现有 7 天过期清理扫描后续重试。
- 修复 `DeleteBatch`：先删除本地上传目录，成功后再删除数据库批次记录；文件系统删除失败时保留批次和路径。若目录已删除但数据库删除失败，记录仍在，重复 `RemoveAll` 对不存在目录可安全重试。
- 新增回归测试：`TestItemBatchRepositoryPreservesUploadPathWhenRemovalFails`、`TestItemBatchRepositoryPreservesBatchWhenUploadRemovalFails`。通过非法路径触发操作系统删除错误，验证数据库路径/批次记录保留。
- 边界：过期清理仍由批量预检 HTTP 请求触发，没有新增后台定时任务；远端闲鱼图片删除/回收能力没有证据，未实现远端清理。
- 验收范围：本地文件系统失败后的重试索引和批次记录完整性已通过 CI 验证；不等于真实闲鱼商品/图片平台 E2E。

### 2026-11-10 阶段 5 AI 客服审计进展

- **5.4 人工接管初审：缺口已确认，最小实现边界已明确，尚未编码**。`AIReplySettings` 只有账号级 `ai_enabled` / `ai_mode`，商品级另有启用/禁用/继承覆盖；`ChatSession` 没有会话级自动回复模式或版本字段，聊天页也没有人工接管/恢复入口。当前消息在防抖后调用 `canAutoReply`，通过本地商品归属与卖家身份门禁后直接进入 `ReplyService.Handle`；该门禁不是人工接管状态。最小实现应增加持久化会话级 `auto/manual` 模式，并在 `ReplyService.deliver` 的统一最终投递闸门覆盖商品首次回复、API、关键词、AI、默认回复。数据库条件更新作为原子 claim：若 `manual` 更新先提交，后续自动回复 claim 必须失败；若 claim 先成功，则该回复定义为已开始的操作，模式切换后允许它完成，但之后的新自动回复不得再 claim。AI 生成期间发生接管的请求会在最终 claim 被拒绝，不会发送。这样不需要全局锁或协调服务；后续仍需把会话归属校验、三数据库迁移、HTTP API/OpenAPI、聊天 UI 和并发竞态测试一并完成。


- **5.1 商品级 AI 策略审计完成**：已核对 `internal/application/items/item_ai_settings.go`、`internal/db/item_ai_settings.go`、`internal/application/items/item_ai_settings_test.go`、`frontend/app/features/items/components/ItemAISettingsModal.tsx` 与 `internal/engine/ai.go`。现有商品级启用/禁用/继承、商品上下文、owner checks、AI reply override 和管理入口均已有实现与测试；没有证据支持再造一套商品级策略系统。
- **5.2 买家图片理解仍在 PR #15**：最新已审阅 CI run [42645840126](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42645840126)：Go 检查、MySQL/PostgreSQL 多数据库回归、真实二进制 + SQLite 冒烟均成功；React 的 TypeScript、前端测试和 OpenAPI 产物检查成功，只有 `npm run build` 后嵌入式静态资源与仓库内容不一致导致 job 失败。PR #15 仍未合并，静态资源同步需等用户恢复电脑环境，不要求当前操作。
- **5.3 DNS rebinding 定向回归 PR #17**：已合并，合并提交 `953d2fbe0ad0d924bfbe5db6d987453b031cfdbe`。CI run [42717878420](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42717878420) 完整成功；新增可注入 DNS lookup / IP dial 的窄测试 seam，验证一次解析后只拨号到已校验的公网 IP 字面地址，DNS 仅返回回环地址时在拨号前拒绝。未改变公网访问策略。
- **5.3 图片网络安全审计部分完成**：`internal/engine/ai_image.go` 明确使用 `netguard.PublicHTTPClient(8s)`，不是用户可配置的普通 HTTP client；下载器还自行校验 HTTP(S)、拒绝 URL userinfo、要求 HTTP 200、限制 5 MiB、按字节解码 JPEG/PNG 并限制 16 MP。`internal/netguard/http.go` 的公网拨号会解析 DNS 后逐个校验 IP，并直接拨号到已验证 IP，避免校验后再次解析；公网客户端禁用 keep-alive，每次连接重新校验，限制最多 5 次重定向且拒绝跨主机跳转。现有 `netguard` 测试覆盖常见私网/回环/链路本地/文档地址、回环访问拒绝、跨主机重定向拒绝；PR #15 图片测试也验证生产公网客户端拒绝回环图片地址。
- **未完成的 5.3 证据**：DNS rebinding 定向回归已补齐并通过 CI。买家图片下载入口的跨主机重定向测试已追加到 PR #15 的 `internal/engine/ai_image_test.go`（提交 `6c82be5a2890b478014dec166473903a2c08ed07`），直接断言跳转目标未收到请求；待 PR #15 最新 Go CI 通过后再确认该证据。当前 5.3 保持进行中，不提前标记完成。
- **安全边界观察**：图片字节限制和 `DecodeConfig` 像素限制已存在；当前只读取图片配置头，不在本地完整解码/重编码图片。该实现限制了下载体积和声明尺寸，但不能把它描述为已对任意恶意图片完成完整解码安全验证。视觉模型服务端仍需按其自身规则解析 data URL。


### 2026-11-09 阶段 4 验收状态更新

- **4.2 完成（本地代码与数据库证据）**：PR #7/#9 已合并。单商品发布使用稳定 operation ID 和请求指纹；`prepared -> started -> remote_confirmed -> local_confirmed` 持久化迁移可在 SQLite/MySQL/PostgreSQL 验证；`started -> unknown` 会阻止同一操作自动重发。CI：PR #7 run [42542832914](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42542832914)，PR #9 run [42620227063](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42620227063)。远端 exactly-once 未获平台契约支持，硬中断停留 `started` 时仍需人工核对。
- **4.3 部分完成**：批量明细已有独立状态、worker token、租约、`remote_started`、ItemID/URL/raw JSON 检查点、失败行筛选与结果 CSV 导出；已有 DB 测试验证 lease takeover 时未知远端结果隔离、已保存 ItemID 的行恢复。尚缺 worker 层 `post_publish` 租约接管到最终收口的集成回归证据，暂不勾选完成。
- **4.3 完成（本地自动化测试范围）**：PR [#16](https://github.com/chenlei-gh/Ydisks-chen/pull/16) 已合并，合并提交 `d1f38e65975f60eb8a04f38d9a26d45fa6414947`。新增 `internal/adapter/item_batch_post_publish_recovery_integration_test.go`，使用真实 `BatchRunner`、`ItemBatchRepository`、`ItemBatchPublishPort` 与 SQLite 数据库，验证旧 worker 在远端成功且 ItemID 检查点已保存后失租，新 worker 接管并完成本地收口，且不会再次调用平台发布接口。CI run [42650479370](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42650479370) 的 Go 检查、按影响范围选择的 Go 测试、server 并发 smoke race、真实二进制 + 临时 SQLite 冒烟均通过；MySQL/PostgreSQL 与 React 按改动范围跳过。**边界**：本地 completion service 使用测试替身，替身写入真实 DB；因此这是 worker/adapter/repository 层集成回归，不是完整生产 service 或闲鱼平台 E2E。
- **4.5 部分完成/待决策**：单商品账本以唯一 operation ID 和条件状态迁移实现同一发布意图的并发闸门；批量发布以批次 lease + row claim/token 实现逐行并发隔离。复核 `frontend/app/features/items/singlePublishOperation.ts` 发现活动 operation ID 存在 `sessionStorage`，作用域是浏览器标签页；服务端唯一约束是 operation ID，而不是请求指纹，因此不同标签页/设备可为相同内容生成不同 ID。是否增加短期同指纹互斥需要产品语义决策：相同内容也可能是用户有意发布的第二条商品，不能直接把内容指纹当永久唯一商品身份。当前不新增全局锁或协调服务。
- **4.6 完成（自动化测试范围）**：批量部分成功/计数与失败行重置、worker claim 互斥和租约接管、未知远端结果隔离、超时及远端成功后 checkpoint 写入失败均有回归测试。PR #8 run [42546788164](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42546788164) 的 Go/React/多数据库/冒烟 4/4 通过；PR #13 run [42624548385](https://github.com/chenlei-gh/Ydisks-chen/actions/runs/42624548385) 通过。故障注入主要基于 SQLite；PR #7/#9 的多数据库结果另行记录，不将其扩大解释为批量全路径三方言验证。
- **边界**：上述结论只覆盖本地自动化测试与 CI，不代表真实闲鱼商品发布、图片上传或远端幂等行为已验证；真实账号/订单 E2E 继续暂停。
