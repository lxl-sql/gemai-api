# PostgreSQL 持久化预扣费与余额一致性修复

> 日期：2026-07-15  
> 场景：约 3 万日活、累计约 2 亿条日志、每日约 200 万条日志、多实例部署，共享 PostgreSQL 与 Redis  
> 目标：解决模型调用后“当前余额”短时间大幅下降、稍后又恢复，以及退款或结算在实例退出后丢失的问题

## 1. 问题现象与结论

用户反馈的典型现象是：调用模型前余额为几百，调用一次后立即查看只剩几十，退出重进或等待一段时间后余额又恢复。

全链路检查确认：`/api/user/self` 本身读取主数据库，但普通非流式 Relay 会先把完整成功响应写给客户端，再执行精确结算；钱包页面拿到的是一次性余额快照，恰好在这个窗口查询后不会自行恢复显示。OpenAI 兼容余额接口和部分预扣前置检查还存在读取缓存的路径。响应时序、缓存读取与旧预扣生命周期叠加后，会让用户看到“余额突然大幅降低、刷新或稍后又恢复”：

- 请求开始时会先扣除估算额度；实际用量较低或请求失败时，需要在后续结算中退回差额。
- 非流式适配器原来会设置 `Content-Length` 并主动 `Flush` 成功 body，客户端可在结算事务开始前认为调用已经完成；此时立即打开钱包会读取到预扣后的低余额，并把该快照保留到下次刷新。
- 旧退款由后台 goroutine 异步执行。HTTP 请求已经结束后，退款可能仍未完成，因此用户立即查看时看到的是扣除预估额度后的可用余额。
- 钱包/订阅额度和 Token 额度原来是分步修改，任一步骤失败都可能留下部分成功状态。
- 预扣状态只保存在当前进程的 `BillingSession` 中。进程退出、实例重启或负载均衡切换后，其他实例无法判断一笔预扣应该结算还是退款。
- 已有 `billing_settlement_failures` 只能记录结算失败，不能表示一笔仍在请求中的预扣，也不能可靠区分请求是否已经发往上游。
- 统一异步任务、实时任务查询和 Midjourney 原来会先把任务改成终态，再单独退款或补扣；进程在两步之间退出时，终态任务不会再被轮询，最终计费永久丢失。

因此本次不采用 Redis 预扣方案。Redis 即使开启 AOF/RDB，也无法与 PostgreSQL 中的钱包、订阅和 Token 额度形成同一个原子事务；故障恢复时仍会出现“双写成功一半”的问题。

## 2. 最终架构

### 2.1 数据来源

- PostgreSQL 是余额和预扣状态的唯一事实来源。
- Redis 只保留原有鉴权/用户/Token 缓存，不保存预扣状态。
- 每次余额变化提交后主动失效 Redis 用户和 Token 缓存。
- 钱包 `/api/user/self` 和 OpenAI 兼容余额接口强制查询主数据库，因此不会将 Redis 中的额度当作“当前余额”返回。
- 钱包、订阅和 Token 余额查询响应统一返回 `Cache-Control: no-store`，防止浏览器、网关或 CDN 复用旧的已认证余额快照。
- 付费非流式 Relay 和统一异步任务的成功响应必须等主库结算（异步任务还包括任务入库）成功后才能提交给客户端；结算失败时丢弃已缓冲的成功响应并返回错误。

### 2.2 新增表

本次新增一张主库业务表：

```text
billing_reservations
```

该表保存活跃预扣，以及尚未完成审计确认的短期 receipt：

- 普通同步请求结算后短暂进入 `completed`，消费日志成功后立即删除。
- 统一异步任务和 Midjourney 的 receipt 会绑定任务 ID，保留到任务成功/失败的最终计费和审计完成。
- 日志写入失败或进程退出时 receipt 会留下，由 repair 重试；不会因为余额已经修改就丢失审计依据。

表中不保存已完成历史。其规模由瞬时请求、当前未完成异步任务和异常积压决定，不会随 2 亿条历史日志或每日 200 万条日志无限增长。

同时在日志数据库新增一张小型技术幂等表：

```text
billing_audit_markers
```

它只保存 `billing_audit_key`、reservation ID、审计类型、实际额度和创建时间。marker 与对应消费/退款日志在日志库同一个事务内写入，主键冲突直接判定为已记录，因而不需要给 2 亿行 `logs` 增加 `request_id` 或 JSON 内容索引。receipt 确认后，repair 会按保留窗口分批删除 marker；该表不是财务历史表，也不会无限累积。日志库与主库共用 PostgreSQL 时，两张新表位于同一数据库；配置独立日志库时 marker 位于日志库。

主要字段：

| 字段 | 用途 |
|---|---|
| `request_id` | 请求级幂等键，唯一索引 |
| `user_id` / `token_id` | 资金和 Token 归属 |
| `task_id` / `midjourney_id` | 与长周期异步任务绑定 |
| `billing_source` | `wallet` 或 `subscription` |
| `subscription_id` | 实际使用的订阅记录 |
| `model_name` / `group` | repair 审计所需的计费上下文 |
| `reserved_quota` | 当前已经预扣的总额度 |
| `wallet_quota_reserved` | 充值额度预扣拆分 |
| `wallet_gift_quota_reserved` | 赠送额度预扣拆分 |
| `token_quota_reserved` | Token 已预扣额度 |
| `token_quota_enabled` | 是否需要修改 Token 额度，持久化 Playground 策略 |
| `desired_quota` | 最终希望结算或退款到的额度 |
| `audited_quota` | 已经由不可变日志表示的额度，用于精确补记差额 |
| `status` | 当前生命周期状态 |
| `expires_at` | 数据库时钟下的租约到期时间 |
| `attempts` / `last_error` | 修复尝试和最近错误 |

索引：

- `request_id` 唯一索引：防止同一个请求重复预扣。
- `(expires_at, id)`：修复任务只扫描到期的小表区间。
- `(status, updated_at, id)`：日志库故障产生大量 receipt 时，提交日志、终态日志和同步日志 repair 仍按小范围有序扫描。
- `task_id`、`midjourney_id`：定位异步任务 receipt。
- 主键 `id` 使用 GORM 跨数据库生成方式。

虽然生产环境使用 PostgreSQL，模型和迁移仍兼容 SQLite、MySQL 5.7.8+、PostgreSQL 9.6+。

### 2.3 状态机

```text
reserved
  ├─ 上游调用前持久化成功 ──> dispatched
  ├─ 请求在发送前失败 ──────> refunding ──> completed ──> 删除
  └─ 正常结算 ─────────────> settling  ──> completed

dispatched
  ├─ 获得精确用量 ─────────> settling  ──> completed
  └─ 明确请求失败 ─────────> refunding ──> completed

completed
  ├─ 同步请求日志成功 ─────────────────────> 删除
  ├─ 异步任务未结束 ───────────────────────> 保留并绑定任务
  └─ 异步任务终态审计 ──> auditing ───────> 删除
```

状态含义：

- `reserved`：额度已经预扣，但尚未记录上游发送动作。
- `dispatched`：即将调用上游；只有该状态成功落库后才允许执行 provider 请求。
- `settling`：精确结算意图已经持久化。
- `refunding`：退款意图已经持久化。
- `completed`：资金和 Token 已一致，receipt 等待日志确认或异步任务终态。
- `auditing`：某个实例已取得短租约，正在写终态审计日志；租约超时后可被其他实例接管。

结算和退款采用“两阶段数据库状态”而不是分布式事务：

1. 先锁定 reservation 并持久化最终状态和 `desired_quota`。
2. 再在一个事务内修改钱包/订阅、Token 和 reservation。
3. 所有资金变化成功后转为 `completed`；消费日志持久化后才确认并删除。

即使进程在第 1、2 步之间退出，其他实例也能根据已持久化意图继续执行，不需要依赖原实例内存。

## 3. 请求生命周期

### 3.1 预扣

预扣事务同时完成：

1. 插入 `billing_reservations`。
2. 按赠送额度优先规则扣减用户钱包，或锁定并扣减订阅额度。
3. 非 Playground 请求扣减 Token 额度。
4. 将实际钱包拆分、订阅 ID 和 Token 策略写回 reservation。

任一步失败，整个事务回滚，不会出现“钱包已扣但 reservation 未创建”或“钱包已扣但 Token 未扣”的部分状态。

重复 `request_id` 不会再次扣费；只有字段和状态完全一致的活跃 reservation 才允许幂等复用。

### 3.2 上游发送

普通 Relay、统一异步任务提交、Midjourney 提交和换脸入口，在执行上游调用前都必须调用 `MarkDispatched`。

- 数据库更新失败：中止请求，不调用上游。
- 重试其他渠道：重复标记 `dispatched`，同时续租，不重复预扣。
- 每次 `dispatched` 同步保存实际渠道 ID，repair 补记日志不会丢失渠道归属。
- 上游调用后发生实例崩溃：修复任务能够识别该请求已经进入结果不确定区间。

### 3.3 正常结算

- 获得实际用量后持久化 `settling + desired_quota`。
- 实际用量低于预扣：按原钱包拆分退回差额，同时恢复 Token 额度。
- 实际用量高于预扣：在同一事务内补扣资金来源和 Token。
- 精确相等：无需重复修改余额，转入 `completed` 等待消费日志确认。
- 结算失败：保留 settlement intent，由 repair 任务继续处理。
- 消费日志失败：保留 completed receipt，不丢失“余额已扣但日志未记”的审计证据。
- 普通付费非流式响应先缓存在事务响应 writer 中；只有精确结算成功后才写给客户端，因此客户端收到成功响应后再查询钱包，不会撞上预扣未结算窗口。日志库失败不阻断已结算响应，completed receipt 会交给 repair 补记。
- 大型非流式音频/图片 body 直接保留已经读取的字节切片，不再为响应缓冲额外复制一份大对象。
- 非流式 padding 仍可在上游等待阶段直写空格保活；上游返回后，真实 body 会重新进入缓冲，结算完成才发送。若上游切换为 SSE/流式响应，则自动进入直通模式。
- 流式请求执行期间显示扣除预扣后的“可用余额”属于正确语义；流结束后同步结算，receipt 负责异常恢复。

### 3.4 失败退款

- 退款改为同步执行，不再放入 goroutine。
- API 返回前最多进行 3 次有限重试。
- 退款意图先持久化为 `refunding + desired_quota=0`。
- 钱包/订阅、Token 恢复和 reservation 完成状态在同一个事务中提交。
- 同步退款仍失败时，保留 reservation，并在已有 `billing_settlement_failures` 中登记，交给跨实例 repair 任务。

这消除了正常失败请求在响应结束后仍等待异步退款的主要余额跳变窗口。

## 4. 崩溃恢复策略

repair 任务先处理已有精确 settlement failure，再扫描过期 reservation。每一行都会在数据库事务中重新加锁并重新判断到期时间，避免“扫描时已到期、处理前已被心跳续租”导致错误退款。

repair 还会扫描尚未确认的 receipt：同步请求在默认 60 秒 grace 后扫描；异步任务提交日志在默认 60 秒后即使任务仍运行也会补记，任务终态后再扫描最终差额。同步请求日志、异步提交日志和终态日志都必须先以 `completed -> auditing` 短租约跨实例抢占，再写日志并确认 receipt；这避免“提交日志尚未完成，终态日志已经按全额补记”的重复记账竞态。租约到期时间同时作为 claim 所有权令牌，旧实例不能释放或删除已被新实例接管的 receipt。异步终态遇到未过期的提交日志 claim 时会回滚任务终态并在下一轮重试；claim 到期后还会等待一个有上限的日志事务 grace 才允许接管。日志事务在开始后重新校验 claim；同库部署时校验行锁、marker 和日志共用一个事务，独立日志库部署时事务超时与接管 grace 配合，防止暂停的旧实例在失去租约后补写提交日志。

日志中写入由业务类型、request ID、reservation 行 ID、已审计额度和实际额度共同组成的确定性 `billing_audit_key`；reservation 行 ID 可避免 receipt 删除后复用同一 request ID 时误命中旧审计。该 key 先作为 `billing_audit_markers` 主键与日志原子写入；若日志已经提交但进程在删除 receipt 前退出，下一实例只需按 marker 主键查询即可确认完成，不会重复写退款/消费日志，也不会扫描大日志表。同步请求缺少原始 token 明细时会补一条最小财务审计日志，至少完整保留 request、用户、渠道、模型、资金来源和最终额度。

过期策略：

| 状态 | 恢复行为 |
|---|---|
| `reserved` | 证明尚未记录上游发送，自动全额退款 |
| `dispatched` | 上游结果无法确认，保守保留预扣估值并完成结算 |
| `settling` | 按已持久化的精确额度继续结算 |
| `refunding` | 按已持久化的退款意图继续退款 |
| `completed` | 资金已完成；同步请求保留审计证据，异步任务等待终态或补记日志 |
| `auditing` | 审计租约到期后重新接管并幂等补记 |

不能将所有过期 `dispatched` 请求直接退款。进程可能在上游成功后、写入精确用量前崩溃；直接退款会形成可被利用的免费调用窗口。对于不支持幂等查询或结果对账的 provider，“保留预扣估值”是当前更安全的财务策略。

默认配置：

```env
BILLING_RESERVATION_TTL_SECONDS=900
BILLING_RESERVATION_RETRY_DELAY_SECONDS=60
BILLING_RESERVATION_REPAIR_BATCH_SIZE=1000
BILLING_AUDIT_CLAIM_TTL_SECONDS=60
BILLING_AUDIT_LOG_TIMEOUT_SECONDS=30
BILLING_AUDIT_MARKER_RETENTION_SECONDS=300
BILLING_STANDALONE_AUDIT_GRACE_SECONDS=60
BILLING_TASK_SUBMISSION_AUDIT_GRACE_SECONDS=60
BILLING_SETTLEMENT_REPAIR_ENABLED=true
BILLING_SETTLEMENT_REPAIR_INTERVAL_SECONDS=15
BILLING_SETTLEMENT_REPAIR_BATCH_SIZE=1000
BILLING_SETTLEMENT_RETRY_DELAY_SECONDS=60
```

活跃请求每隔约三分之一 TTL 自动续租。默认 900 秒可以覆盖长流式请求；进程崩溃后的最迟自动处理时间约为 TTL 加一次调度间隔。

## 5. 多实例与并发安全

### 5.1 统一数据库时钟

reservation 创建、发送、续租、结算和修复使用数据库时间：

- PostgreSQL：`FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))`
- MySQL：`UNIX_TIMESTAMP()`
- SQLite：`strftime('%s','now')`

关键状态转换查询数据库时间失败时直接回滚事务，不回退到应用实例时钟，从而避免多个实例存在时钟偏差时提前退款或延迟修复。

### 5.2 锁顺序

- 同一实例先使用用户级本地锁，减少同一热点用户占满数据库连接池。
- 数据库事务使用 reservation 行锁和用户/订阅行级原子更新，保证跨实例一致性。
- settlement failure 和过期修复统一按 reservation → failure 的顺序访问，避免相反锁顺序导致死锁。
- PostgreSQL 钱包扣减继续使用带余额条件的原子 `UPDATE ... RETURNING`。

### 5.3 额度边界

- 创建、补扣、结算和 failure intent 都拒绝负数或超过 int32 数据库范围的额度。
- Token 增减同时检查 `remain_quota`、`used_quota` 上下界。
- 每次 Token 预扣、补扣和退款都在 SQL 条件中同时校验 `token_id` 与 `user_id`；归属错配会使整个资金事务回滚。
- 退款不会将 `used_quota` 减为负数，也不会让 `remain_quota` 溢出。
- Token 在预扣前已删除时拒绝请求；在请求发送后被删除时，允许资金结算/退款继续完成，避免用户余额永久卡住。
- PostgreSQL 对软删除用户使用 unscoped 原子退款，保证用户恢复后余额仍正确。
- 用户充值额度与赠送额度分列读取后在 Go 的 64 位整数中相加，避免 PostgreSQL `int4 + int4` 在两个字段都合法时发生表达式溢出。

## 6. 对当前生产规模的影响

每日 200 万请求/日志折算平均约 23 次/秒。reservation 是活跃态加短期 receipt 表，正常行数近似：

```text
瞬时请求速率 × 平均同步请求时长 + 当前未完成异步任务数
```

即使累计日志达到 2 亿，repair 也不会扫描或按无索引条件查询 `logs`，只会使用 `billing_reservations(expires_at, id)`、`billing_reservations(status, updated_at, id)` 和小表 `billing_audit_markers` 的主键/时间索引。因此：

- 不需要将 reservation 放入日志库。
- 不需要在 2 亿行 `logs` 上由 AutoMigrate 创建新的 `request_id` 索引。
- 不需要按日分区。
- 不需要 Redis 持久化或分布式锁。
- 不应把已完成且审计确认的 reservation 长期保留在该表。

每个普通请求会增加若干主库短事务和行更新，这是换取财务一致性的必要成本。当前平均吞吐量对 PostgreSQL 不高；需要重点关注峰值并发、热点用户以及所有实例连接池总和，而不是 2 亿日志表大小。

非流式响应缓冲只覆盖已经持久化预扣的付费请求。适配器已经将大多数非流式响应完整读入内存，本次对这类 body 采用字节切片保留而不是二次复制；流式和 padding 空格保持直通，因此不会把长流响应累积在内存中。

生产连接池仍建议每实例约 20～50 个连接，并确保：

```text
实例数 × SQL_MAX_OPEN_CONNS < PostgreSQL max_connections
```

PostgreSQL 启动迁移会为 `billing_reservations` 和 `billing_audit_markers` 设置更积极的 autovacuum 和 `fillfactor=80`，用于控制频繁插入、状态更新和删除产生的死元组。`completed` 的正常同步 receipt 只跨越一次日志写入；只有未完成异步任务或异常审计才会停留较久。marker 默认至少保留一个完整 claim 与日志事务周期，随后每批最多删除 1000 行。

## 7. 已覆盖入口

- 普通 OpenAI/Claude/Gemini 等 Relay。
- 流式与非流式请求。
- 多渠道重试。
- Realtime 追加预扣。
- 钱包和订阅计费偏好。
- Playground 不扣 Token 策略。
- 统一异步任务提交。
- 统一异步任务的轮询终态、超时终态和实时查询终态。
- Midjourney Submit 和 SwapFace。
- Midjourney 最终失败时的钱包、订阅和 Token 原子退款。
- 缺少上游任务 ID 的异常任务通过原子终态事务退款，不再用 bulk update 绕过计费；渠道暂时不可读时保持非终态等待下轮，避免误失败和漏退款。
- 上游已经接受统一任务或 Midjourney 后，即使本地“结算 + 入库”失败，也持久化精确结算意图并禁止错误退款；最差由 `dispatched` repair 保留预估扣费，避免形成免费任务。
- 请求成功后的精确结算和请求失败后的同步退款。
- 普通非流式成功响应、统一异步任务成功响应都在对应主库事务提交后才对客户端可见；失败渠道的已缓冲响应会在重试前清空。

旧 `PreConsumeQuota` 仅保留为兼容函数，生产主链路已经改为 `PreConsumeBilling/BillingSession`。

## 8. 测试与二次审计

发布前要求执行：

```text
go test ./... -count=1 -timeout 180s
go vet ./...
git diff --check
BILLING_TEST_POSTGRES_DSN='...' go test -tags=integration ./model -run TestBillingReservationPostgreSQLLifecycle -count=1 -v
CGO_ENABLED=1 go test -race ./model ./service ./controller ./relay -count=1
```

真实 PostgreSQL 集成测试使用 `BILLING_TEST_POSTGRES_DSN` 显式启用，并在随机独立 schema 中运行；测试不会再 DROP 共享库中的 `users`、`tokens` 等生产同名表。未配置 DSN 时该项会明确 skip，不能把 skip 记录为 PostgreSQL 已验证。

当前工作区最终验证结果：`go test ./... -count=1 -timeout 240s`、`go vet ./...`、`git diff --check` 全部通过；使用临时 PostgreSQL 16 实例运行真实方言集成测试通过；使用 Linux Go 1.26 + CGO 对 `model/service/controller/relay` 运行 `-race` 通过。PostgreSQL 测试使用随机独立 schema，并实际验证迁移索引、数据库时钟、生命周期、软删除退款和大余额相加。

SQLite 回归测试覆盖：

- 钱包预扣、增加预扣、结算、退款和重复调用幂等。
- 订阅预扣、差额结算和退款。
- Token 不足、Token 预扣前删除、Token 请求中删除，以及 Token 与用户归属错配时全事务回滚。
- Playground 不修改 Token 额度。
- settlement intent 写入后重试。
- 退款事务失败时钱包不发生部分回滚。
- 续租后 repair 不会按旧扫描结果退款。
- `dispatched` 过期后保留预扣估值。
- 软删除用户退款。
- 信任额度环境变量开启时，durable billing 仍执行真实预扣。
- 统一任务“提交结算 + 任务入库”单事务。
- 统一任务“终态 CAS + 钱包/订阅 + Token + task quota 快照”单事务，失败全部回滚并允许下次轮询重试。
- Midjourney 最终失败同时恢复资金来源和 Token，重复终态不会二次退款。
- 同步、提交和终态审计 claim 的串行化、所有权校验、超时接管和确定性 audit key 去重。
- marker 与日志原子提交、重复 key 不重复插入日志，以及 receipt 确认前不清理 marker。
- 审计 claim 到期与日志事务并发时的写前校验、同库行锁和独立日志库接管 grace。
- 发布前已经存在、缺少 reservation request ID 的订阅异步任务仍可按旧 delta 语义原子完成终态退款。
- 缺少上游任务 ID 的异常任务走原子终态计费，渠道缓存/数据库瞬时错误不再 bulk 标记失败。
- 订阅追加预扣同步更新 pre-consume 快照，最终结算校验 reservation 与订阅预扣记录归属一致。
- 非流式成功响应在结算前不可见、结算失败可丢弃、渠道重试清空上一轮 body/header，以及 padding 停止后真实 body 延迟到结算完成。
- 统一异步任务成功响应在“任务入库 + 结算”事务提交前不可见，提交失败不会泄漏成功 header/body。
- 上游成功后的精确结算一旦开始，后续错误清理不会把其覆盖成退款；异步任务本地提交失败会留下精确 settlement failure 供跨实例 repair。
- 异步任务和 Midjourney 的幂等重放会校验实际额度，额度不一致时拒绝复用且不重复修改余额。
- 清理任务不会删除仍被 durable reservation 引用的订阅 pre-consume 记录。
- 非终态异步任务缺失提交日志时，repair 在 grace 后按 marker 幂等补记并保存已审计额度。

真实 PostgreSQL 集成测试覆盖：

- AutoMigrate 创建表和索引。
- `(expires_at, id)` 与 `(status, updated_at, id)` 两组 repair 索引存在。
- PostgreSQL 数据库时间表达式。
- `quota` 与 `gift_quota` 同时为 `MaxInt32` 时仍可正确返回 64 位总余额，不触发 `int4` 表达式溢出。
- 预扣和同 request ID 幂等复用。
- `dispatched` 崩溃恢复。
- 未发送 reservation 自动退款。
- PostgreSQL 软删除用户退款。
- 完成 receipt 经审计确认后被删除。
- 每次测试使用独立 schema，不破坏 DSN 指向数据库中的现有表。

二次审计修复了以下问题：

- repair 扫描与心跳续租之间的 TOCTOU。
- 正额度预扣时 Token 不存在却被当作成功。
- 预扣为 0 时未检查 Token 的边界。
- 上游成功但结算状态未持久化的崩溃窗口。
- 多实例使用应用时间判断 lease。
- Playground/Token 扣费策略只存在内存中。
- PostgreSQL 软删除用户退款失败。
- Token 退款后出现负 `used_quota` 或整型溢出的风险。
- 统一异步任务在任务入库前提前删除 reservation，导致入库失败只能走旧退款路径。
- Midjourney 提交仍使用旧分步扣费和退款路径。
- 统一任务和 Midjourney 先写终态、后退款/补扣的崩溃窗口。
- 任务提交时保存的是结算前钱包拆分，后续退款可能按错误快照计算。
- 实时任务查询可以绕过轮询的原子终态结算。
- repair 使用 NoLedger 修改余额后立即删除 reservation，主库和日志库之间硬崩溃会失去审计证据。
- OpenAI 兼容余额和预扣前置检查读取 Redis/内存缓存，可能显示或拒绝基于旧额度的结果；`/api/user/self` 经审计确认原本就是主库查询，继续保持该语义。
- 普通非流式适配器在精确结算前已经 `Flush` 完整成功响应，钱包查询可能捕获预扣低余额；现将真实响应延迟到结算成功，padding 只允许保活空格提前发送。
- 统一异步任务在“任务入库 + 结算”事务提交前已经返回成功；现缓冲成功响应，提交失败时丢弃成功 header/body。
- PostgreSQL 集成测试直接 DROP DSN 默认 schema 中的核心表。
- 异步提交日志与终态日志未串行化，极快完成或进程卡顿时可能重复记账。
- 审计 claim 没有所有权校验，旧实例可能释放或删除其他实例重新接管的租约。
- 提交审计进程崩溃后，非终态任务对应的过期 `auditing` receipt 无法被终态结算接管。
- 订阅追加预扣只更新订阅和 reservation，没有同步更新 pre-consume 快照。
- 余额兼容接口在 Token 查询失败时先解引用空对象，且用户余额查询错误可能被后续查询覆盖。
- 曾计划在 2 亿行 `logs` 上增加 `request_id` 索引；最终改为小型 `billing_audit_markers` 主键幂等表，避免生产启动时大表 `CREATE INDEX` 锁表和长时间回填。
- 审计租约刚过期时终态可能与尚在执行的日志事务交叉，形成“提交日志 + 全额终态日志”；现增加写前租约校验和日志事务 grace。
- 统一任务、Suno、视频和 Midjourney 的 null task/channel error bulk 终态更新绕过退款；现改为逐任务原子终态，瞬时渠道错误不落终态。
- 升级时遗留订阅任务没有原始 pre-consume request ID，无法匹配新 receipt 校验；现仅对无 reservation 的遗留任务保留原有订阅 delta 兼容路径。
- `/api/user/self` 为避免敏感字段泄漏必须保持 `selectAll=false`；余额权威性通过其原有主库查询保证，不以加载密码或 access token 为代价。
- 订阅 pre-consume 的 7 天清理可能删除仍在运行的长任务结算依据；现对被 `billing_reservations` 引用的记录执行保留。
- 异步任务主库提交后、提交日志确认前崩溃时，运行中的任务原来无法触发审计 repair；现增加非终态提交 receipt 扫描和 60 秒 grace。
- 文本、音频、图片、Embedding、Rerank、Gemini、Claude、Responses 和 Realtime 的后结算错误原来只写日志仍返回成功；现统一向 Relay 传播本地结算错误、禁止渠道重试，并丢弃尚未提交的成功响应。

最终生产审计继续修复了以下问题：

- 审计 repair 按 `updated_at` 排序但缺少对应复合索引；日志库长时间故障时可能退化为大范围扫描和排序。
- PostgreSQL 中 `quota + gift_quota` 使用两个 `int4` 字段直接相加，字段分别合法时表达式仍可能溢出。
- 余额类 GET 响应未禁止中间缓存，浏览器、网关或 CDN 仍可能返回旧快照。
- 上游已经成功后，本地精确结算或异步任务入库失败时，通用错误 defer 仍可能尝试退款；现以本地 `finalizationStarted` 门闩阻止该错误状态转换，并持久化精确意图。
- 异步任务/Midjourney 幂等提交只校验任务 ID 和用户，没有校验实际额度；现拒绝额度冲突的重放。
- Token 额度更新只按 `token_id`，没有在资金事务内校验 Token 用户归属；现所有增减都同时约束 `user_id`。
- 并行视频轮询直接修改调用方持有的 Task 指针；现改为私有副本计算和落库，消除跨 goroutine 数据竞争。
- 并发日志输出对轮转计数和工作状态使用无锁全局变量；现改为原子计数与 CAS，并经 Linux `-race` 验证。

## 9. 上线步骤

1. 备份主库，并确认 PostgreSQL 主从/高可用状态正常；应用 `SQL_DSN` 必须指向可写主库或提供写后读强一致性的代理，余额权威查询不能被路由到异步只读副本。
2. 选择低峰期发布，先停止接入新请求或从负载均衡中逐个摘除旧实例。
3. 先启动一个 master，让 AutoMigrate 创建 `billing_reservations`、`billing_audit_markers` 和必要的小表索引；不会为现有 `logs` 新建索引。
4. 确认 migration 成功后，再滚动启动其他实例。
5. 确保至少一个 master 启动 system task runner。
6. 确认 repair 配置保持开启。
7. 将旧版本实例全部排空后下线，避免发布期间一部分请求走 reservation、一部分请求仍走旧异步退款。
8. 先进行小流量灰度，覆盖：正常流式调用、客户端中断、上游 4xx/5xx、渠道重试、任务提交、Midjourney 提交。
9. 观察至少 24 小时后再全量。

## 10. 生产监控

### 10.1 活跃 reservation

```sql
SELECT status,
       COUNT(*) AS rows,
       MIN(expires_at) AS earliest_expiry,
       MAX(attempts) AS max_attempts
FROM billing_reservations
GROUP BY status
ORDER BY status;
```

正常情况下总量应接近当前瞬时并发加未完成异步任务数。`settling/refunding` 持续增长、终态任务对应的 `completed/auditing` 超过一个修复周期仍未下降，或 `attempts` 持续升高，都表示数据库、日志库或 Token 数据存在异常。

### 10.2 已过期未修复

```sql
SELECT status, COUNT(*) AS rows
FROM billing_reservations
WHERE status IN ('reserved','dispatched','settling','refunding','auditing')
  AND expires_at <= FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint
GROUP BY status;
```

正常应接近 0。持续超过一个调度周期仍增长，需要检查 master、system task runner 和数据库锁等待。

### 10.3 settlement failure

```sql
SELECT reservation_managed, reservation_status, COUNT(*) AS rows,
       MAX(attempts) AS max_attempts
FROM billing_settlement_failures
WHERE status = 'pending'
GROUP BY reservation_managed, reservation_status;
```

### 10.4 repair 任务

```sql
SELECT status, COUNT(*) AS rows, MAX(updated_at) AS last_at
FROM system_tasks
WHERE type = 'billing_settlement_repair'
  AND updated_at >= FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint - 3600
GROUP BY status;
```

### 10.5 表膨胀

```sql
SELECT relname, n_live_tup, n_dead_tup,
       last_autovacuum, autovacuum_count
FROM pg_stat_user_tables
WHERE relname IN ('billing_reservations', 'billing_audit_markers');
```

如果 `n_dead_tup` 长期显著高于活跃行数并持续增长，需要检查 autovacuum 是否被长事务阻塞。

`billing_audit_markers` 正常只保留短时间窗口。可以同时监控：

```sql
SELECT COUNT(*) AS rows, MIN(created_at) AS oldest
FROM billing_audit_markers;
```

`oldest` 长期早于当前时间减去配置保留窗口，表示 repair runner 或日志库清理异常。

建议采集的日志关键字：

- `failed to renew billing reservation`
- `failed to repair billing reservation`
- `failed to record billing settlement failure`
- `refund billing reservation failed`
- `settle task billing error`
- `atomic task finalization failed`
- `failed to repair completed task billing audit`
- `billing_dispatch_failed`

## 11. 回滚要求

不能在仍有活跃 reservation 时直接把所有实例回滚到旧版本。旧版本不会消费 `billing_reservations`，但对应余额已经完成预扣，强制回滚可能使这些余额长期无法结算或退款。

安全回滚步骤：

1. 停止接入新请求。
2. 等待正常请求结束。
3. 确认 `billing_reservations` 已为空。
4. 确认无 pending 的 managed settlement failure。
5. 再回滚二进制。

紧急情况下至少保留一个新版本 master 继续执行 repair，直到 reservation 清空。新增表和字段可以保留，旧版本不会读取，不应在回滚窗口内执行 DROP TABLE。

## 12. 审计边界与已知限制

### 12.1 上游结果不确定性

调用上游后发生硬崩溃时，如果 provider 不提供幂等键、任务 ID 查询或账单对账接口，本地系统无法严格证明上游是否成功。当前对过期 `dispatched` reservation 按预扣估值结算，是防止成功请求免费使用的保守策略。

要获得精确结果，需要逐 provider 增加幂等请求键和对账适配器；这不是 Redis 或单一本地数据库表能够解决的问题。

### 12.2 长周期异步任务与日志库边界

本次已经覆盖统一任务和 Midjourney 的完整生命周期：提交结算与入库原子提交；轮询、超时和实时查询进入终态时，任务 CAS、钱包/订阅、Token 和任务额度快照在主库同一事务内提交。任何主库错误都会整体回滚，任务保持非终态供下一轮重试。

主库与独立日志库之间无法形成跨库 ACID 事务，因此采用 receipt + audit claim + 确定性 `billing_audit_key`：

- 主库资金完成后 receipt 不立即删除。
- 同步日志、异步提交日志和终态日志都先取得带所有权令牌的 audit claim；日志成功后才确认删除或释放为等待终态的 `completed`。
- PostgreSQL/MySQL/SQLite 日志库将 audit marker 与对应日志放在同一事务内提交；marker 主键提供原子去重。
- 崩溃后 repair 接管；若日志实际已提交，会按 marker 主键识别并只做确认，不重复记录，也不查询 2 亿行日志。

这保证主库财务 exactly-once，并使 PostgreSQL/MySQL/SQLite 日志库在可重试故障和“提交成功但客户端收到错误”的模糊结果下保持幂等。ClickHouse 日志库不参与 marker 事务，保留原有 request 局部查询兼容路径。若日志数据库永久损坏，receipt 会保留而不会伪装成审计完成，需要先恢复日志库再让 repair 清理。

### 12.3 余额展示语义

请求正在执行时，预扣额度已经从可用余额中锁定，因此钱包页面显示较低的“当前余额”是正确的可用余额语义。普通非流式请求和异步任务提交只有在主库结算成功后才向客户端提交成功响应；流式请求在流结束前仍属于执行中。正常请求结束后差额已经同步恢复；只有实例崩溃、数据库长时间不可用等异常情况才进入 TTL repair 窗口。

如果产品希望同时展示“账户总额度、处理中冻结额度、当前可用额度”，应新增独立展示字段，而不应把 reservation 加回可用余额，否则会允许并发超额消费。

## 13. 涉及文件

核心模型与迁移：

- `model/billing_reservation.go`
- `model/billing_settlement_failure.go`
- `model/db_time.go`
- `model/log.go`
- `model/quota_transaction.go`
- `model/subscription.go`
- `model/main.go`

服务与修复任务：

- `service/billing_session.go`
- `service/billing.go`
- `service/billing_settlement_repair.go`
- `service/funding_source.go`
- `service/task_billing.go`
- `service/task_polling.go`

请求入口：

- `controller/relay.go`
- `controller/user.go`
- `controller/billing.go`
- `controller/token.go`
- `controller/midjourney.go`
- `controller/task_video.go`
- `controller/system_task_handlers.go`
- `relay/relay_task.go`
- `relay/mjproxy_handler.go`
- `relay/common/billing.go`
- `relay/common/relay_info.go`
- `relay/channel/api_request.go`
- `service/http.go`

测试：

- `model/billing_reservation_test.go`
- `model/billing_reservation_postgres_test.go`
- `service/billing_reservation_repair_test.go`
- `model/task_cas_test.go`
- `service/task_billing_test.go`
- `controller/relay_task_response_test.go`

配置：

- `.env.example`
