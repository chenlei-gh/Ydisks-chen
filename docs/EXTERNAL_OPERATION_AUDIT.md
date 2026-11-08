# 外部操作状态与商品发布可靠性审计

> 审计基线：main `4f066bb20962657f4969e9af819b47f65f3a7918`（2026-11-08）
> 范围：自动发货、普通聊天文本/图片发送、单商品发布、批量商品发布。
> 方法：静态调用链审计与现有测试源码审阅；本报告不声称本轮新运行了测试，也不代表闲鱼真实平台 E2E。

## 1. 结论摘要

**不需要新增全局 ExternalOperationService。** 四条链路已有各自的持久化和恢复边界；其状态名称不完全相同，但业务语义并非可以简单合并。真正明显的缺口是单商品发布缺少远端调用前的持久化操作检查点，导致进程在远端请求开始后中断时，服务端没有本地记录可区分“未调用”和“可能已创建商品”。

普通聊天消息此前的初步判断需要修正：它并非只有 WebSocket echo。应用层在调用发送端口前先创建本地 `sending` 消息，发送后持久化 `sent / failed / uncertain` 状态；消息具有本地 MessageKey。自动化发送额外使用平台回显确认。故聊天消息已有持久化操作记录，不应重复建设。

## 2. 状态模型对照

| 链路 | 外部调用前持久化 | 外部成功证据 | 本地提交失败 | 超时/断线/中断 | 恢复与重复副作用保护 |
|---|---|---|---|---|---|
| 自动发货 | run、attempt、action cursor/start、加密 proof | 消息发送结果/平台回显；发货确认 | proof、游标和终态收口；失败隔离 | 结果未知进入 needs_review | lease + attempt fencing；未知动作不盲目重放 |
| 普通文本/图片消息 | 创建 sending 消息和 MessageKey；图片先上传，再创建待发送媒体消息 | 普通人工消息使用 sender 返回；自动化发送等待自身 WS echo | 返回 ErrStatusSave，保留消息记录/Key | ErrSendUncertain 并尝试写 uncertain | 本地消息记录与状态可查询；图片上传与消息发送是两个不同外部副作用 |
| 单商品发布 | **没有独立持久化发布运行记录** | MTOP 返回含 ItemID 的结果 | 返回 remote_published_local_save_failed 或 cookie_save_failed，并提示勿重复发布 | 远端调用错误/响应丢失时，缺少可恢复检查点 | 当前主要依赖用户提示人工核对；不能证明硬中断后不会再次提交 |
| 批量商品发布 | 批次 lease + 行 worker token；最终发布前写 remote_started | 保存 ItemID/URL/raw JSON | post_publish 检查点后继续本地后处理 | remote_started 且无 ItemID 接管时转 uncertain_remote | 只重试安全行；有 ItemID 的 post_publish 行可恢复后处理 |

## 3. 关键代码证据

### 3.1 自动发货

- `internal/automation/run_coordinator.go`：动作执行、动作游标推进、未知结果隔离。
- `internal/automation/run_lease.go` 与 DB lease 实现：运行租约与 attempt fencing。
- `internal/automation/action_executor.go`：动作前执行权检查、发货凭证 proof。
- `internal/automation/card_delivery.go`：卡密发送和传输未知分类。

结论：本地数据库执行权可阻止旧 attempt 覆盖新状态，但不能撤销已经发出的远端请求；外部平台未证实提供幂等键或 fencing。保留 needs_review 是正确边界。

### 3.2 普通聊天文本和图片

- `internal/application/chat/send.go` 的 `OutgoingRepository` 明确提供 `CreateOutgoing`、`CreateOutgoingMedia` 和 `SetOutgoingStatus`。
- `SendText` 在调用 Sender 前先创建本地 sending 消息；传输未知时尝试写 uncertain，明确失败时写 failed，成功后写 sent。
- `SendImage` 先上传图片，再创建含平台图片 URL 的 sending 媒体消息，然后发送并收口状态。上传成功但创建消息记录失败会留下平台侧孤立上传资源风险，但不会等同于已向买家发送图片。
- `internal/engine/outgoing_message_coordinator.go` 的自动化路径另有 WS 回显 waiter 和 request MID；回显超时返回未知结果。回显 tracker 本身是内存态，但消息业务记录在应用层持久化。

结论：不需要再造消息发送台账。需要在后续 AI/消息阶段单独验证 sending 长期悬挂扫描、图片上传孤立资源清理及状态查询/人工核对 UX；这不是当前阶段新增全局协调服务的理由。

### 3.3 批量商品发布

- `internal/db/item_publish_batches_worker.go`：`ClaimBatch`、`ClaimRow`、`MarkClaimedRemoteStarted`、`SaveClaimedRemoteResult`、`MarkClaimedRowSuccess`。
- 过期批次接管时，旧 worker 的 `remote_started` 且无 ItemID 行先转 `uncertain_remote`；其他可恢复 running 行转 interrupted/pending。
- 已有 ItemID 的 post_publish 行可被重新领取，后续 publisher 应复用 ItemID 执行本地后处理而不重新调用远端创建。
- `ResetFailed` 明确排除 validation 与 uncertain_remote，避免对结果未知的远端创建自动重放。

结论：该域已有较完整的检查点。需保留针对 worker 接管、post_publish 继续、uncertain_remote 不可重试的测试；不要把不同 failure_kind 统统折叠成一个状态。

### 3.4 单商品发布

- `internal/application/items/single_publish.go`：先调用 PublishPort，获得带 ItemID 的结果后才调用 ItemRepository.Upsert。
- `internal/adapter/item_publish.go`：读取凭证快照后调用 MTOP PublishItem；远端调用前没有对应的数据库操作记录或远端开始检查点。
- HTTP handler 对“远端已发布但本地保存失败”及“Cookie 写回失败”会返回明确错误和 ItemID/URL，并提示不要重复发布。
- 现有 `single_publish_test.go` 主要覆盖平台成功后本地写入、平台失败不写入；未见持久化远端开始检查点或硬中断恢复测试。

结论：这是阶段 4 的优先缺口。不能只在应用服务中加一个内存状态，亦不能把任何网络错误都简单标成确定失败。修复至少需要稳定的客户端操作身份/幂等提交策略、持久化开始检查点、ItemID 结果检查点、状态查询/人工核对路径、迁移与并发/故障注入测试。若闲鱼发布 API 没有可验证的幂等键或按请求身份查询能力，系统最多做到“未知结果隔离”，不能自动证明是否创建成功。

## 4. 共享抽象决策

阶段 3.3：**不提取全局操作服务（审计结论：不需要）。**

理由：
1. 自动发货依赖订单所有权、卡密 ledger、动作 cursor 和 proof。
2. 聊天发送依赖消息记录、MessageKey、媒体上传和 WS echo。
3. 批量发布依赖批次/行级 worker lease 与远端 ItemID 检查点。
4. 单商品发布的缺口是缺少持久化发布操作记录；这应先复用商品 repository/事务能力解决，而不是创建能包办所有业务域的抽象。

可以共享的是文档化的状态语义与测试场景，不是强行共享数据库 schema 或 service。

## 5. 执行权与状态语义

统一描述建议采用以下概念，不要求各域数据库字段同名：

- `not_started`：尚未进入不可逆远端副作用；允许在条件仍成立时启动。
- `started`：已持久化记录即将/正在调用远端；中断后不能推断未执行。
- `remote_confirmed`：获得平台可验证成功证据，例如商品 ItemID 或消息回显。
- `local_confirmed`：本地业务状态已可靠提交。
- `unknown / needs_review`：远端结果无法判定；禁止自动重放可能重复的副作用。

lease/token/attempt 只负责本地写入 fencing；不得将其描述成远端 exactly-once 保证。

## 6. 下一批实施范围

1. 单商品发布：设计持久化 operation/checkpoint 与稳定请求身份；先验证现有 API 是否支持可用的幂等参数或查询能力。没有远端幂等/查询能力时，硬中断只能进入人工核对。
2. 单商品发布：远端返回 ItemID 后尽可能先持久化远端结果，再执行 Cookie/商品目录后处理；本地后处理可恢复且不重复创建。
3. 批量发布：增加/核对 post_publish 恢复路径与状态迁移测试，确认 ItemID 存在时重试只做本地后处理。
4. 单商品与批量发布：覆盖开始检查点写入失败、请求超时、远端成功但响应丢失、远端成功后 DB 保存失败、旧 worker 失权。
5. 聊天消息：不新建台账；把 sending 悬挂恢复、媒体上传后中断和 uncertain 人工收口列入后续专项。
6. 更新 API 文档与 ROADMAP；完整 CI 验证后提交 PR。真实闲鱼平台测试继续暂停。

## 7. 验证边界

本报告基于指定 commit 的源码静态审计及测试源码审阅。本轮没有运行新的测试命令，没有真实闲鱼账号/订单 E2E 结果。现有 main 上 PR #1 的历史 CI 结果仅能证明该 CI head 的自动化测试通过，不自动覆盖本报告基线后的新改动。


## 8. MTOP 发布身份与批量 post_publish 复核（2026-11-08）

### 8.1 单商品 MTOP 没有可依赖的请求幂等身份

- `internal/xianyu/mtop/publish.go` 的 `PublishItemRequest` 没有调用方提供的 operation/request ID。
- `publishItemOnce` 在每次最终发布请求中自行生成 `uniqueCode = strconv.FormatInt(time.Now().UnixMicro(), 10)`。它是每次调用重新生成的时间值，不是由业务操作稳定持有的身份；重试时会变化，不能据此证明服务端会去重。
- 当前仓库没有发现将该 `uniqueCode` 作为幂等键的契约、回查接口或“按请求身份查询发布结果”的实现。MTOP 发布成功响应主要通过 `data.itemId` 取回远端商品 ID。
- 仓库已有 `FetchItemsPage`（`mtop.idle.web.xyh.item.list`）读取卖家商品列表，但它不是按客户端请求身份查询。仅凭标题/时间/图片等字段做相似匹配存在误匹配与漏匹配风险，不能作为自动重发或自动认定成功的依据。
- 外部开源协议笔记将 `uniqueCode` 描述为来源不明的 opaque tracking value，而非已证实幂等键；这只能作为旁证，最终仍以本仓库代码和真实平台契约证据为准。

**结论**：当前证据不支持依赖 MTOP 幂等键或 request-ID 查询。单商品未知结果必须持久化为 `unknown/needs_review`，禁止自动再次调用发布接口。后续若要改变此结论，必须取得可复现的平台契约证据与重复请求实验结果；真实账号实验仍需单独授权。

### 8.2 批量 post_publish 路径已有明确的防重复创建实现与测试

- `internal/adapter/item_batch_publish.go` 的 `PublishRemoteRow` 在进入图片读取、类目准备和远端发布前，先检查行内 `ItemID`；存在时直接由已保存行构造结果返回，不调用 MTOP 创建接口。
- `internal/adapter/item_batch_publish_test.go` 的 `TestItemBatchPublishPortPersistsRemoteCheckpoint` 验证远端 ItemID 检查点可恢复，并断言恢复调用后平台发布调用次数仍为 1。
- `internal/db/item_publish_batches_worker.go` 的租约接管逻辑将 `remote_started` 且无 ItemID 的旧行隔离为 `uncertain_remote`；已有 ItemID 的行则可进入后续处理。该测试覆盖已保存 ItemID 后的端口恢复，但仍应补充 worker 层 post_publish 行接管/最终收口的集成测试，验证状态迁移全链路。

**结论**：批量发布不是缺少 post_publish 防重机制；当前缺的是 worker 层恢复状态迁移的更完整验证，不应重复实现已有 checkpoint 逻辑。

### 8.3 图片上传的副作用边界

商品主图和规格图在最终 `idleitem.publish` 请求前分别上传到图片服务。上传成功只证明图片资源已上传，不证明商品已创建或对买家可见。若后续类目准备、节流闸门或最终发布失败，可能留下孤立图片资源；当前代码没有看到对应的远端删除/清理契约。该问题应按资源清理/保留策略单独评估，不能把图片上传标记为“商品发布成功”。



## 9. 单商品发布持久化方案边界（实现前设计核查）

代码中已经存在一个适合精确落检查点的扩展点：

- `internal/xianyu/mtop/publish.go` 的 `PublishItemRequest.BeforePublish` 在主图/规格图上传和类目准备完成后、最终 `mtop.idle.pc.idleitem.publish` 请求发出前调用。
- 批量发布已经使用该 hook，在回调内原子预留时隙；单商品 `ItemPublishPort.publish` 当前没有传入该 hook。
- 因此单商品不应在开始上传图片前就把操作标记成远端创建 `started`。更准确的顺序是先持久化操作意图 `prepared`，再由 `BeforePublish` 持久化 `started`，之后才允许最终创建请求发出。图片上传阶段失败可安全重做商品创建流程（但可能遗留上传图片资源）；`started` 后进程中断或传输结果不明必须进入 `unknown/needs_review`。

建议最小商品域操作记录包含：

- 稳定的客户端 operation ID（HTTP 请求携带；不能由服务端每次重试重新生成）。
- `user_id`、`cookie_id`、规范化请求 fingerprint；同一 ID 携带不同 fingerprint 必须冲突拒绝。
- 状态：`prepared`、`started`、`remote_confirmed`、`local_confirmed`、`unknown`、明确拒绝的 `failed`。
- 远端确认后的 `item_id`、`item_url`、受控 `raw_json` 和非敏感错误分类。
- 不保存图片字节；fingerprint 计算时纳入图片内容摘要与规格图片摘要，避免同一 key 被不同图片负载复用。

推荐执行顺序：

1. 前端在一次发布意图开始时生成 operation ID，并在请求超时/结果未知时保留该 ID；明确开始另一条新商品发布时才生成新 ID。
2. 服务端鉴权并校验输入后，原子创建或读取 operation 记录；重复 ID + 相同 fingerprint 返回已有状态，重复 ID + 不同 fingerprint 返回冲突。
3. `PublishItemRequest.BeforePublish` 回调条件更新 `prepared -> started`。若写入失败，不发出最终创建请求。
4. MTOP 返回含 ItemID 的成功结果后，适配器立即写入 `remote_confirmed` 检查点，必须早于 Cookie 会话写回和本地商品目录 Upsert。
5. 后续 Cookie / 本地商品 Upsert 成功后标记 `local_confirmed`；后处理失败时保留 `remote_confirmed`，同 key 重试只做本地后处理，不再次调用 MTOP 创建。
6. `started` 后网络错误、进程硬中断、成功响应缺少 ItemID、远端成功但 ItemID 检查点写入失败，一律进入 `unknown` 或保持可判定为未知的 `started`；同 key 重试只能返回人工核对状态，不能再次发布。

关键设计约束：operation ID 只能防止同一用户端重试同一个操作被重复执行；它不会让闲鱼服务端去重，也无法阻止用户错误地为同一商品创建一个全新的 operation ID。界面必须让未知操作持续可见，不能用“重新发布”按钮静默生成新 ID。由于商品创建 API 的服务端幂等契约未证实，本方案不承诺 exactly-once，也不提供未知结果自动回查/自动重发。

该设计涉及三方言 migration、数据库原子状态转换、前端 operation ID 生命周期、OpenAPI/客户端契约和故障注入测试。应作为一个完整商品域实施批次，不先引入全局 ExternalOperationService。
