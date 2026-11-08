# P0 自动发货与手动补发安全审计报告

> 审计日期：2026-11-08  
> 审计分支：`ydisks-enhanced/p0-delivery-safety`  
> 代码基线：`081e7f56b5a50c8e98db5309042f0fa5bd8119b7`（与上游 `Christ9038/Ydisks-Xianyu-Helper:main` 当前一致）  
> 本报告是静态代码与现有测试证据审计，不表示本次已运行测试，也不表示闲鱼平台 E2E 已通过。

## 1. 审计结论

当前代码已有较完整的安全骨架：订单级归属锁、跨触发器防重、执行租约、attempt fencing、动作游标、加密发货快照、未知结果隔离、人工核对入口，以及数据卡密预占台账。**不需要另建一套发货协调框架。**

但本次发现至少一项已证实的数据完整性缺口，应该进入阶段 2 优先修复：

| 优先级 | 结论 | 证据与影响 |
|---|---|---|
| P0 | `ReleaseBatchData` 对同一 reserved 台账记录不具备幂等恢复保护 | 更新库存发生在删除台账之前；删除不检查影响行数。重复调用同一 `recordID` 会再次把相同内容前插库存，删除 0 行仍提交，可能导致同一张卡密被重复售出。 |
| P1 | 人工补发成功收口没有兜底结算本运行遗留的 reserved 台账 | 普通 `FinishRun` 通过 `finishRunTail` 原子确认 reserved 台账；`CompleteDeliveryReplay` 只更新运行状态。若 `markSold` 的旁路写入失败，补发成功后台账仍可能停留在 reserved，售出统计不准确。 |
| 风险边界 | 租约 fencing 只能约束本地数据库状态，不能给闲鱼 WebSocket/MTOP 外部请求提供远端 fencing | 每次外部动作前会检查租约，但检查与网络副作用不是同一原子操作；外部接口未见可依赖的幂等键。不能宣称 exactly-once。 |
| 风险边界 | 人工补发每次从快照第一条消息重新发送，没有逐消息补发游标 | 若一次补发中前几条已成功、后续失败，下一次用户再次发起补发会从头发送快照；可能重复发送前面成功的内容。当前属于用户主动完整补发语义，是否接受需产品层明确，不能描述成无重复恢复。 |
| 相邻范围 | `status_only` 手动确认发货路径不走完整发货运行锁 | 该路径不消耗卡密，但可以与自动发货运行并行；是否必须阻止其在自动发货进行中确认订单，需要单独确认业务语义。此项不擅自扩入本轮 P0 修复。 |

## 1.1 审计后修复进度（2026-11-08）

以下是审计后在本分支已提交的最小修复；它们尚未经过本地或 CI 测试运行，不应标记为已验证通过。

| 原审计项 | 分支修复 | 回归测试 | 状态 |
|---|---|---|---|
| P0 `ReleaseBatchData` 重复释放 | `internal/db/card_sale_records.go`：先在同一事务中条件删除对应卡组的 reserved 台账；已不存在则 no-op；状态/卡组不匹配则报错；库存 CAS 冲突时事务回滚，删除与库存恢复一起重试。 | `TestCards_ReleaseBatchDataIsIdempotent`、`TestMultiDB_CardSaleReleaseIsIdempotent` | 已提交，待执行 |
| P1 人工补发成功收口未结算台账 | `CompleteDeliveryReplay` 改为事务收口，并复用 `finishRunTail`，成功状态与 reserved → sold 结算原子提交。 | `TestMultiDB_CardSaleReplayCompletionSettlesReserved` | 已提交，待执行 |

相关提交：
- `b4c93a70e1dd1995470a207526278037c19cd5d5`：库存释放幂等修复。
- `b31888978f64784661d6883314490c29ebf84a9a`：人工补发收口结算修复。
- `63e69f30ecdd6bc939ae06a72267aaca3f796888`：库存重复/并发释放与补发台账结算回归测试。
- 当前 PR：[#1](https://github.com/chenlei-gh/Ydisks-chen/pull/1)，open、ready for review，未合并。

**验证状态**：尝试在本地克隆仓库时，运行环境无法解析 `github.com`，因此没有本地测试结果。仓库所有者已在 `Actions permissions` 中选择 `Allow all actions and reusable workflows` 并点击 Save。此前提交（`be13da760d528a7a47e82388be3463fa7f893e71`）发生在设置保存确认之前，因此不能视为保存后的触发验证。保存后已推送文档提交 `1b14e4fd2c257b4a2bed24d9f5bcdaa26ac2d1be`，但该 head 仍未出现 workflow run 或 commit status。说明问题不是单纯的设置未保存；需要在仓库 Actions 页面确认是否有 Enable/approval 提示，并检查账户/组织策略是否覆盖仓库设置。

## 2. 自动发货调用链（1.1）

1. 自动事件进入自动化中心，按规则匹配后调用 `automationRunCoordinator.executeRule`。
2. `prepareRuleRun` 补全订单事实、检查付款订单状态与金额门禁，并创建或恢复运行。
3. `AutomationRules.TryStartRun` 在数据库事务内取得账号/订单归属锁，执行跨触发来源防重并持久化运行记录；提交成功后才返回执行权。
4. `executeRunActionLoop` 按 `ActionCursor` 调用 `StartRunAction` 领取当前动作。
5. `executeLeasedAction` 建立 5 分钟执行租约，心跳周期 1 分钟；外部动作入口与逐条消息/取卡入口调用 `checkRunExecution`。
6. `executeActionWithProof` 分派卡密、模板、文本、改价或确认发货动作。
7. 动作明确成功后，`AdvanceRunAction` 原子推进游标、累计数量并保存加密发货凭证；不确定结果则调用 `QuarantineRunResultWithProof` 转人工核对。
8. 正常终态由 `FinishRun` 写入；成功终态通过事务尾部 `finishRunTail` 同时确认本运行遗留的 reserved 台账。

关键文件：
- `internal/automation/run_coordinator.go`
- `internal/automation/run_lease.go`
- `internal/automation/action_executor.go`
- `internal/automation/card_delivery.go`
- `internal/db/automation_ownership.go`
- `internal/db/automation.go`
- `internal/db/automation_delivery_proof.go`

## 3. 手动完整发货/补发调用链（1.2）

1. HTTP：`POST /api/v1/orders/manual-ship` → `manualShipOrders` → `orderHTTPAdapter.ManualShip`。
2. 应用层 `ManualShipService.ManualShip` 检查当前用户拥有账号、订单归属和 `pending_ship` 状态。
3. `full_delivery` 分支检查自动化中心与账号在线状态，再调用 `Center.ManualFullDelivery`。
4. `prepareManualDeliveryTask` 补全订单事实；`handlePriorManualDelivery` 读取同订单既有自动/人工运行，决定拒绝、继续或使用既有加密快照补发。
5. 新人工运行使用稳定键 `manual_delivery:<orderID>`，通过与自动发货相同的 `TryStartRun` 创建。
6. 已有快照补发先校验 `deliveryReplayPlan`，再由 `ClaimDeliveryReplay` 以 attempt CAS 原子领取；随后启动同一租约守卫，按原顺序发送快照，必要时仅补齐缺失的直接卡密，最后确认平台发货并收口。

关键文件：
- `internal/server/versioned_order_routes.go`
- `internal/server/order_handlers.go`
- `internal/server/order_service.go`
- `internal/application/orders/manual_ship_service.go`
- `internal/automation/manual_delivery.go`
- `internal/automation/delivery_replay_plan.go`
- `internal/db/automation_delivery_proof.go`

## 4. 自动发货与人工完整补发互斥（1.3）

**已证实成立（代码路径层面）**：

- 自动和人工完整发货最终都经过 `TryStartRun`。
- `beginOwnershipWrite` / `lockAutomationOwnership` 按账号 → 订单顺序持有数据库写锁，检查订单归属；运行创建、重领、动作领取和延期写入复用该归属边界。
- `allowOrderDeliveryRun` 在同一事务锁内检查同账号同订单的 `order_paid` 运行，防止自动事件与人工完整发货使用不同幂等键时并发创建两条有副作用的运行。
- `StartRunAction` 再次通过归属锁与 attempt/cursor 条件更新领取动作。
- 人工快照补发不创建第二条发货运行，而是通过同一运行记录的 `ClaimDeliveryReplay` CAS 领取新 attempt。
- `TestManualDeliveryCannotRaceWithAutomaticDelivery` 覆盖了“人工预检查后自动先完成，人工随后执行”的交错场景，断言不会第二次发送。

**测试证据限制**：原有 `TestManualDeliveryCannotRaceWithAutomaticDelivery` 是 SQLite 确定性顺序交错。审计后新增 `TestMultiDB_AutomationPaidRunCrossTriggerConcurrency`，通过两个 goroutine 同时以不同 trigger key 竞争同一账号/订单，并使用 `allTestTargets` 覆盖 SQLite 与配置的 MySQL/PostgreSQL；该测试尚未运行，不能当作已验证结果。

`status_only` 手动确认发货不经过上述完整发货运行链路；本报告只将其记录为相邻范围风险，不混同于人工完整补发。

## 5. 租约、代次与恢复（1.4、1.8）

**已证实成立（本地持久化边界）**：

- 执行租约：5 分钟；每 1 分钟续租；每次租约 DB I/O 最多 5 秒。
- `RenewExecutingRunLease` 要求运行仍为 running、动作已领取、attempt 一致且旧租约尚未过期；过期租约不能复活。
- `StartRunAction`、`AdvanceRunAction`、`FinishRun`、隔离与延期操作都使用 attempt/cursor/status 条件更新，旧代次不能覆盖新代次。
- Scheduler 对过期且 `ActionStarted=true` 的运行直接隔离为 `needs_review`，不把可能已经发送的动作自动重放。
- `QuarantineRecoveryRun` 对扫描快照的状态、代次、游标、动作占用与租约截止时间做条件匹配；续租或状态变化后的陈旧扫描不能隔离新状态。
- 延迟任务快照携带运行 ID、规则 ID、延迟游标和待人工核对标记；恢复时从冻结的运行计划和游标继续。

**不能证明的边界**：数据库 attempt fencing 无法撤销已经发出的网络请求，也无法阻止一个在最后一次 `checkRunExecution` 之后被长时间暂停、恢复后继续执行的旧进程。平台侧未验证幂等键/远端 fencing 前，不能声称 exactly-once。当前正确策略是对未知结果隔离并人工核对。

关键文件：
- `internal/automation/run_lease.go`
- `internal/db/automation_execution_lease.go`
- `internal/db/automation.go`
- `internal/db/automation_recovery.go`
- `internal/automation/scheduler.go`

## 6. 外部副作用与持久化顺序（1.5）

- 运行记录先持久化；动作开始前先写 `action_started=1`；外部动作成功后再原子推进 cursor 与凭证。
- 消息发送前的账号/在线发送器缺失会返回 `ErrMessageNotSent`，被视为确定未发送；传输层返回其他错误时按不确定处理。
- 数据卡密先在一个事务中 CAS 扣库存并写入 reserved 台账；明确未发送时调用 `ReleaseBatchData`；传输结果未知时保留消费与快照，不再重新取卡。
- API 卡密区分 `Dispatched`：外部已派发但返回错误时进入未知隔离；请求明确未派发时进入不可重试失败；已取得内容但消息发送未知时保存该内容，补发不再次请求 API。
- 平台确认发货请求若响应丢失/调用错误，或远端成功但本地订单/Cookie 状态持久化失败，均进入未知隔离/补偿处理；“已发货”业务响应按幂等成功识别。

## 7. 卡密库存审计（1.6）——发现的缺口

### P0：重复释放同一 reserved 记录会重复恢复库存

代码位置：`internal/db/card_sale_records.go` 的 `ReleaseBatchData` / `tryReleaseBatchData`。

当前事务顺序是：

1. 读取卡密组当前库存。
2. 把待恢复内容前插到库存。
3. CAS 更新库存。
4. 若 `recordID > 0`，执行 `DELETE FROM card_sale_records WHERE id=? AND status='reserved'`。
5. **没有检查 DELETE 的 RowsAffected 是否为 1**，随后直接提交。

因此同一个 `recordID` 第一次恢复成功并删除台账后，第二次调用仍会先把同一内容再次前插库存；DELETE 影响 0 行，但代码忽略并提交。此问题不依赖极端并发：只要同一恢复操作因上层重试/提交结果不明而被再次调用，就可能把同一张卡密恢复多次。CAS 重试循环并没有解决“同一台账只能释放一次”的幂等约束。

**影响**：库存虚增，后续可能把同一张卡密售给多个订单。  
**最小修复方向**：在同一事务内先以条件状态迁移/领取 reserved 台账，确认恰好影响一行后再修改库存；或先锁定并校验台账，再执行库存 CAS，保证重复释放不会再改库存。不能只在调用方增加进程内 mutex。需要 SQLite、MySQL、PostgreSQL 三方言并发/重复调用测试。

原有测试 `TestCards_ReleaseBatchDataRestoresStockAndDeletesRecord` 只验证单次释放。审计后新增 `TestCards_ReleaseBatchDataIsIdempotent` 与 `TestMultiDB_CardSaleReleaseIsIdempotent` 覆盖重复调用和多方言并发释放；目前仅有测试代码，尚无运行结果。

### P1：人工补发成功收口缺少 reserved 台账兜底结算

- 普通成功运行的 `FinishRun` 在事务内调用 `finishRunTail`，把本 run 遗留的 reserved 台账确认为 sold。
- 人工快照补发的 `refillPartialDeliveryProof` 仍通过 `sendCardWithProof` 取卡和发送；`markSold` 的写入失败只记录日志。
- 随后 `replayDeliveryProof` 调用 `CompleteDeliveryReplay`；该方法只更新 `automation_runs` 状态，没有像 `FinishRun` 一样结算该 run 的 reserved 台账。

**影响**：人工补发实际成功但台账状态仍为 reserved，售出统计低估，异常台账长期残留。  
**最小修复方向**：已让 `CompleteDeliveryReplay` 在同一事务中执行运行终态更新与 reserved 台账结算，复用 `finishRunTail`。已新增 `TestAutomationCompleteDeliveryReplayRecoversFailedMarkSold`，通过 SQLite trigger 注入 `MarkSaleRecordsSold` 失败，再验证成功收口兜底结算；测试尚未运行。

## 8. unknown / needs_review 的持久化与人工入口（1.7）

**已证实成立**：

- 不确定动作调用 `QuarantineRunResultWithProof`，加密保存可用的原始消息/卡密凭证，设置 `needs_review` 并清空 lease/retry 时间。
- `ListIssues` 仅列当前用户拥有账号下的 `needs_review` 运行和 dead-letter 延期任务。
- HTTP 提供 `GET /automation-issues`、`POST /automation-runs/{run_id}/resolve` 和延期任务 resolve 入口。
- `automationIssuePolicy` 按动作占用、游标、发送数、冻结计划与错误类型计算允许的人工动作；人工补发失败（外部结果可能未知）只允许 cancel，不允许 retry/continue。
- `ResolveRunIssue` 再次按用户归属和 needs_review 状态条件更新，处理后不再出现在异常列表。

关键文件：
- `internal/db/automation_recovery.go`
- `internal/server/automation_handlers.go`
- `internal/application/automation`

## 9. 测试覆盖与证据等级（1.9）

| 测试 | 当前可确认的覆盖 | 类型/限制 |
|---|---|---|
| `TestAutomationRunAttemptFencesStaleWorker` | 旧 attempt 不能领取动作、收口或隔离新代次 | SQLite 仓储集成 |
| `TestMultiDB_AutomationExecutionLease` | 续租、过期拒绝、陈旧扫描与有效隔离 | SQLite + 配置时真实 MySQL/PostgreSQL |
| `TestMultiDB_AutomationOwnershipFence` | 运行/延期痕迹与订单归属修复的提交顺序互斥 | SQLite + 配置时真实 MySQL/PostgreSQL |
| `TestMultiDB_AutomationPaidRunCrossTriggerConcurrency` | 自动事件与人工完整发货不同 trigger key 同时竞争同一订单，只允许一个取得运行权 | 新增；SQLite + 配置时真实 MySQL/PostgreSQL，待执行 |
| `TestDeliveryReplayExpiredOwnerCannotSend` | 旧补发代次失权后不能继续发送或覆盖新代次 | SQLite + 消息/平台替身 |
| `TestManualDeliveryCannotRaceWithAutomaticDelivery` | 人工预检查与自动先执行的交错不重复发送 | SQLite + 消息/平台替身；不是并发多方言测试 |
| `TestDeliveryReplayProofFailureBlocksFurtherConsumption` | 补发结果快照保存失败后持久化占用，禁止再次取卡 | SQLite 故障注入 |
| `TestAutomationFinishRunSettlesReservedRecords` | 普通 FinishRun 成功收口兜底确认 reserved 台账 | SQLite 仓储集成 |
| `TestMultiDB_CardSaleLedger` | 单次预占/恢复、台账搜索、确认与统计 | SQLite + 配置时真实 MySQL/PostgreSQL |

测试环境证据：
- `newTestDB` 创建每测试独立临时 SQLite 文件，不是 mock DB。
- `allTestTargets` 默认 SQLite；设置 `TEST_MYSQL_URL` / `TEST_POSTGRES_URL` 时会创建隔离数据库并执行迁移。
- CI `.github/workflows/ci.yml` 启动 MySQL 8.4 与 PostgreSQL 17，执行 `make test-multidb`；该目标设置 `REQUIRE_MULTIDB=1`，缺少任一外部数据库会失败。
- CI 还执行 `go test ./...`、server race 子集和真实服务二进制 + 临时 SQLite smoke。

**覆盖缺口**：
- 重复/并发调用 `ReleaseBatchData` 的测试已新增，但尚未运行。
- 已新增 SQLite 故障注入测试，验证 `MarkSaleRecordsSold` 失败后由 `CompleteDeliveryReplay` 兜底结算；尚未运行，且该故障注入仅覆盖 SQLite。
- 自动/人工不同 trigger key 的真正同时竞争测试已新增，但尚无执行结果；需等待 CI 运行确认。
- 本次未运行 Go 测试，也没有本开发分支的 CI 结果；测试文件存在不代表通过。
- 没有闲鱼真实账号/订单的平台 E2E 证据。

## 10. 最小修复顺序建议

1. **已完成代码修复** `ReleaseBatchData` 的单记录单次释放约束，并添加重复调用与多方言并发竞争回归测试；等待 CI 运行。
2. **已完成代码修复** `CompleteDeliveryReplay` 成功收口时 reserved 台账结算；新增 SQLite 故障注入测试，等待运行。
3. 已添加自动发货与人工完整发货不同 trigger key 的真实并发领取测试（至少覆盖所有 CI 方言）；待 CI 执行确认。
4. 对人工补发逐消息重复发送行为明确产品语义；若要求失败后不重发已确认成功的消息，再设计逐消息持久化进度，不要只加内存游标。
5. 保留“远端未知结果必须人工核对”的现有安全边界；未证明平台幂等能力前不引入自动盲重试。

## 11. 本次执行边界

- 第一阶段审计先完成报告，再按报告进入最小修复；修复提交已在 1.1 节列明。
- 本次 GitHub 代码读取与测试结构检查不等于运行测试；本地运行环境无法解析 `github.com`，目前没有本地 Go 测试结果。
- PR #1（open，ready for review）已确认仓库所有者保存 Actions permissions，且保存后新 head `1b14e4f` 仍无可见 workflow run/status；需检查 Actions 页面提示及上级策略。不得把“PR 已创建”描述为“CI 已通过”。
- 本轮不在真实买家、真实订单或真实账号上执行发货/补发。


## 12. 2026-11-08 云端验证追加记录

- commit `af7bdf2` 的云端 Go 验证：`go vet ./...` 通过；`golangci-lint ./...` 通过（0 issues）；中文注释、架构命令、OpenAPI 和 docs 一致性步骤通过。
- `go test ./...` 实际运行，除 `tools/architecturecheck` 中 `TestStageTwoRealSourceGate` 因 `internal/notify/notifier.go` 808 行超过 800 行门槛而失败外，其余列出的 Go 包均通过。该失败不是业务测试失败，而是测试代码自身执行的真实源码架构门禁；race 步骤因此被跳过。
- 已把 `levelLabel`、`eventLabel`、`classifyAccountAlertEvent`、`statusOutcome`、`messageOutcome`、`formatEvent`、`eventAllowed`、`eventAllowedWithFallbacks`、`isAutomationEventType`、`parseEventTypes`、`sortStrings` 从 `notifier.go` 拆至 `notifier_events.go`。主文件从 808 行减至 552 行；新文件 262 行。`encoding/json` 随 JSON 解析逻辑迁移，`strings` 保留在主文件并在新文件独立导入。
- 新文件中文注释门禁最初报告 `levelLabel` 缺少注释，已补充说明性中文注释。拆分后的完整 Go 测试、race 测试、多方言矩阵及 PR 最新 head 状态尚待再次执行/确认。
- 不得将上述首轮结果描述为 P0 已验收或平台真实交易验证；无闲鱼真实订单发货/补发测试。
