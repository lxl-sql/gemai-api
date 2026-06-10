# 充值额度与赠送额度拆分架构方案

> 当前文档已按本次实际改造后的代码状态更新。核心合同是：`users.quota` / API `quota` 均表示充值额度和升级前历史余额，`users.gift_quota` / API `gift_quota` 表示赠送额度，`total_quota = quota + gift_quota` 表示剩余总额度。旧客户端如果继续把 `quota` 当总余额，会低估用户可用余额。

## 背景与目标

当前项目主钱包余额只有 `users.quota` 一个字段。在线充值、兑换码、签到、注册赠送、邀请奖励划转、管理员加减额度、API 预扣费和结算都混用这一余额池，后续无法区分“用户真实充值额度”和“系统赠送额度”。

本方案目标：

- 保留历史数据，不回溯拆分旧余额。
- `users.quota` 保持原字段，作为充值额度和历史旧余额。
- 新增 `users.gift_quota`，作为赠送额度。
- 从新版本开始，兑换码、签到、注册赠送、邀请码赠送、邀请奖励划转等全部进入赠送额度。
- 真实充值、支付回调、管理员补单继续进入充值额度。
- 消费时按总余额校验，优先扣赠送额度，不足再扣充值额度。
- 所有余额变动通过统一钱包服务和钱包流水表完成，避免分散直接更新 `quota`。
- 对充值、扣费、退款、订阅余额购买、任务退款等资金敏感路径加入业务幂等，避免支付回调、网络重试、轮询重试导致重复加减额度。
- 对 Realtime、异步任务、Midjourney 等非普通文本请求单独处理扣费明细，避免预扣、流式扣费、最终结算混用导致多扣或漏扣。

## 现状分析

### 改造后的余额字段

- `model/user.go` 的 `User.Quota`：充值额度，也包含升级前历史旧余额。注意：该字段不再表示总余额。
- `model/user.go` 的 `User.GiftQuota`：赠送额度，消费时优先扣减。
- `model/user.go` 的 `User.TotalQuota()`：运行时计算的剩余总额度，等于 `Quota + GiftQuota`，不落库。
- `model/user.go` 的 `User.UsedQuota`：累计已用额度。
- `model/user.go` 的 `User.AffQuota`：邀请奖励待划转额度，当前不能直接消费。
- `model/token.go` 的 `Token.RemainQuota` / `Token.UsedQuota`：API Key 级额度，和用户钱包不同。
- 订阅额度由 `model/subscription.go` 管理，已经通过 `BillingSourceSubscription` 与钱包区分。

### 当前增额入口

这些入口已按来源分流：

- 在线充值：`model/topup.go` 的 `Recharge`、`RechargeCreem`、`RechargeWaffo`、`RechargeWaffoPancake` 进入充值额度 `quota`。
- 易支付回调：`controller/topup.go`，入账事务成功后才返回 `success`，进入充值额度 `quota`。
- 管理员补单：`model/topup.go` 的 `ManualCompleteTopUp` 进入充值额度 `quota`。
- 兑换码：`model/redemption.go` 的 `Redeem` 进入赠送额度 `gift_quota`。
- 签到：`model/checkin.go` 的 `UserCheckin` 进入赠送额度 `gift_quota`。
- 新用户注册赠送：`model/user.go` 的 `Insert`、`InsertWithTx`、`FinalizeOAuthUserCreation` 进入赠送额度 `gift_quota`。
- 邀请被邀请人奖励：`model/user.go` 中 `QuotaForInvitee` 对用户加额，进入赠送额度 `gift_quota`。
- 邀请奖励划转：`model/user.go` 的 `TransferAffQuotaToQuota`，从 `aff_quota` 划转到 `gift_quota`。
- 管理员调额：`controller/user.go` 的 `ManageUser`，`action=add_quota`，通过 `quota_type=recharge|gift` 选择额度池，默认 `recharge`。
- 消费退款和结算修正：`service/funding_source.go`、`service/billing_session.go`、`service/task_billing.go` 等。

### 当前扣费入口

主扣费链路：

- `service/billing.go` 的 `PreConsumeBilling` / `SettleBilling`。
- `service/billing_session.go` 的 `BillingSession`。
- `service/funding_source.go` 的 `WalletFunding`。
- 旧兼容链路：`service/pre_consume_quota.go`、`service/quota.go` 的 `PostConsumeQuota`。
- 异步任务扣费/退款：`service/task_billing.go`、`controller/task_video.go`、`controller/midjourney.go`。
- 用钱包购买订阅：`model/subscription.go` 中余额扣减逻辑。

当前主钱包路径已经收敛到统一钱包服务。仍需关注的是旧兼容路径、Midjourney、异步退款和历史任务等边界链路，避免同一业务事件重复扣费或退款。

## 目标数据模型

### `users` 表

新增字段：

```text
gift_quota INT NOT NULL DEFAULT 0
```

字段语义：

```text
quota       = 充值额度，也包含升级前历史旧余额
gift_quota  = 赠送额度
used_quota  = 累计已用额度，保持现有语义
```

不新增 `paid_quota`。`quota` 本身就是充值额度。

不在数据库中存总余额。总余额统一计算：

```text
total_quota = quota + gift_quota
```

### API 响应

```text
quota          = users.quota
gift_quota     = users.gift_quota
used_quota     = users.used_quota
total_quota    = users.quota + users.gift_quota
```

说明：

- `quota` 在数据库层和 API 响应层都表示充值额度。
- `gift_quota` 表示赠送额度。
- `total_quota` 是剩余总额度，等于 `quota + gift_quota`。
- 新 UI 使用 `quota` 和 `gift_quota` 展示明细。
- 后端内部不得再直接依赖响应层 `quota` 语义。

## 钱包流水与幂等表

已新增 `quota_transactions` 表。用户表只存当前余额，流水表记录每次余额变化。

注意：

- 该表不会自动回填历史充值/消费记录。
- 上线后发生的充值、赠送、管理员调额、兑换码、签到、邀请划转、订阅余额购买、显式退款等"入账侧"事件会写入该表。
- `idempotency_key` 是资金安全核心字段，所有写入都必须提供稳定且非空的幂等键。

重要：消费链路不写该表。

- Relay 预扣/结算/退款（`WalletFunding`、`PreConsumeQuota`/`PostConsumeQuota`）、异步任务差额结算与退款、Midjourney 退款均走 `DebitQuotaPreferGiftNoLedger` / `RefundQuotaByBreakdownNoLedger`，只更新 `users` 余额，不插入 `quota_transactions`（出于高频写入性能考虑）。
- 因此 `quota_transactions` 不是完整资金流水，`balance_after` 与用户当前余额之间会被消费/消费退款拉开差距，`balance_before/after` 在时间线上也不连续。
- 对账口径：不能用「最新流水 after 值 == 当前余额」校验。正确口径为按入账侧聚合：`sum(quota_delta)`/`sum(gift_quota_delta)` 只能对账入账类事件总量；消费总量需从消费日志（`logs`）或 `used_quota` 维度核对。
- 若未来需要完整流水对账，需为消费链路补轻量异步流水（可批量聚合写入），属于后续扩展项。

### 字段设计

```text
id
```

主键。

```text
user_id
```

余额归属用户。

```text
type
```

流水类型。建议稳定枚举：

- `topup`：真实充值入账。
- `gift`：赠送入账。
- `consume_pre`：预扣费。
- `consume_settle`：结算补扣或退差额。
- `refund`：失败退款、取消退款、回滚。
- `admin_adjust`：管理员调整。
- `aff_transfer`：邀请奖励划转。
- `subscription_buy`：用钱包购买订阅。
- `migration`：迁移或初始化。

```text
quota_delta
```

充值额度变化量。正数表示增加，负数表示扣减。

```text
gift_quota_delta
```

赠送额度变化量。正数表示增加，负数表示扣减。

```text
balance_before
```

变动前充值额度，也就是 `users.quota` 更新前值。

```text
gift_balance_before
```

变动前赠送额度。

```text
balance_after
```

变动后充值额度。

```text
gift_balance_after
```

变动后赠送额度。

```text
total_delta
```

总变化量，等于 `quota_delta + gift_quota_delta`。可选但建议保留，便于查询和统计。

```text
source
```

业务来源，例如：

- `stripe`
- `creem`
- `waffo`
- `waffo_pancake`
- `epay`
- `redemption`
- `checkin`
- `register`
- `invite`
- `admin`
- `relay`
- `task`
- `subscription`

```text
reference_type
```

关联对象类型，例如：

- `topup`
- `redemption`
- `checkin`
- `user`
- `log`
- `task`
- `subscription`
- `manual`

```text
reference_id
```

关联对象 ID。可以是订单号、兑换码 ID、任务 ID、请求 ID、订阅 ID 等。为了兼容不同来源，建议使用字符串。

```text
request_id
```

API 请求 ID。Relay 请求、异步任务、支付回调都可以写入，方便排查。

```text
idempotency_key
```

幂等键。必须加唯一索引，用于防止支付回调、任务退款、重试请求重复加减额度。

幂等键必须由业务来源生成稳定值，不能使用每次都不同的随机值。当前实际格式见下一节。

```text
operator_id
```

操作者 ID。系统自动操作为 `0`，管理员调整时记录管理员 ID。

```text
metadata
```

扩展信息，使用 `TEXT` 存 JSON 字符串，保证 SQLite、MySQL、PostgreSQL 兼容。JSON 序列化必须使用项目 `common` 包封装。

```text
created_at
```

创建时间。

### 索引建议

- `idx_quota_tx_user_created`：`user_id + created_at`
- `idx_quota_tx_type_created`：`type + created_at`
- `idx_quota_tx_source_created`：`source + created_at`
- `idx_quota_tx_reference`：`reference_type + reference_id`
- `request_id` 普通索引
- `idempotency_key` 唯一索引

`idempotency_key` 必须唯一。为空值是否允许重复要按数据库差异处理，建议所有写入都生成非空幂等键。

### 当前实际使用的幂等键

钱包流水常见格式：

```text
topup:{provider}:{trade_no}
redemption:{redemption_id}:{user_id}
checkin:{user_id}:{date}
wallet_pre:{request_id}
wallet_settle:{request_id}:{seq}
wallet_refund:{request_id}:{amount}
legacy_wallet_pre:{request_id}
legacy_wallet_post:{request_id}:{amount}
task_settle:{task_id}:{target_quota}
task_refund:{task_id}:{refund_amount}
task_legacy_refund:{task_id}:{refund_amount}
midjourney_refund:{mj_id}:{quota}
admin_adjust:{idempotency_key}
subscription_buy:{idempotency_key}
```

订阅差额常见格式：

```text
subscription_settle:{request_id}:{delta}
subscription_reserve:{request_id}:{target_quota}
subscription_reserve_rollback:{request_id}:{delta}
subscription_extra_refund:{request_id}:{amount}
subscription_pre_refund:{request_id}
legacy_subscription_post:{request_id}:{delta}
legacy_subscription_post_rollback:{request_id}:{delta}
task_subscription_delta:{task_id}:{target_quota}
```

### `subscription_delta_records` 表

已新增 `subscription_delta_records` 表，用于订阅后置差额调整的幂等。

该表解决的问题：

- `BillingSession` 结算订阅差额时，如果请求超时或重试，不能重复扣订阅额度。
- 异步任务完成后补扣/退还订阅额度时，轮询重复执行不能重复扣退。
- Reserve 回滚、额外预留退款、旧 `PostConsumeQuota` 兼容订阅路径也需要幂等。

字段：

```text
id
idempotency_key
user_subscription_id
delta
amount_used_before
amount_used_after
created_at
```

其中 `idempotency_key` 为唯一索引。

## 并发与事务设计

### 核心原则

余额更新和流水写入必须在同一个数据库事务内完成。

所有钱包变动必须满足：

```text
锁定用户余额行
读取当前余额
计算变动明细
更新 users
插入 quota_transactions
提交事务
```

MySQL/PostgreSQL 使用行级锁：

```text
SELECT ... FOR UPDATE
```

GORM v2 必须使用 `clause.Locking{Strength: "UPDATE"}`。注意：旧写法 `Set("gorm:query_option", "FOR UPDATE")` 是 GORM v1 API，在 GORM v2 下是静默 no-op（不加任何锁），已全量替换为统一封装 `model.LockForUpdate(tx)`（SQLite 下自动跳过该子句）。新代码一律使用该 helper，禁止再写 `gorm:query_option`。

SQLite 没有真正的行级锁，但事务写会串行化。要避免依赖数据库特定语法，锁逻辑封装在 model 层。

### 扣费事务流程

```text
BEGIN

1. 用 user_id 锁定 users 行
2. 读取 quota、gift_quota
3. 检查 quota + gift_quota >= need
4. 计算：
   deduct_gift = min(gift_quota, need)
   deduct_quota = need - deduct_gift
5. 更新 users：
   gift_quota -= deduct_gift
   quota -= deduct_quota
6. 插入 quota_transactions：
   quota_delta = -deduct_quota
   gift_quota_delta = -deduct_gift
   before/after 全部记录
7. 提交

COMMIT
```

只要两类额度总和足够，就允许消费。不能先看赠送额度不足就拒绝，也不能只看充值额度。

### 入账事务流程

充值入账：

```text
quota_delta = +amount
gift_quota_delta = 0
```

赠送入账：

```text
quota_delta = 0
gift_quota_delta = +amount
```

流程：

```text
BEGIN

1. 校验 idempotency_key 未处理，或直接尝试插入唯一幂等流水
2. 锁定 users 行
3. 更新对应余额
4. 插入流水
5. 提交

COMMIT
```

支付回调必须幂等。订单状态更新和余额入账也应在同一事务中完成。

### 退款事务流程

退款必须按原扣费明细反向退回。

```text
BEGIN

1. 查原扣费流水
2. 校验退款幂等键未处理
3. 锁定 users 行
4. 按原流水反向写入：
   quota_delta = -original.quota_delta
   gift_quota_delta = -original.gift_quota_delta
5. 插入 refund 流水，metadata 中关联 original_transaction_id
6. 提交

COMMIT
```

如果某次请求预扣时从赠送扣了 300、充值扣了 200，失败退款必须退回赠送 300、充值 200。否则长期会造成来源不准确。

### Redis 缓存策略

Redis 只能作为缓存，不能作为最终扣费依据。

建议：

- 数据库事务提交后再更新或失效 Redis。
- 缓存字段新增 `GiftQuota`。
- `GetUserQuota()` 返回总余额时可以从缓存计算 `Quota + GiftQuota`。
- 钱包扣费事务不能只依赖 Redis 中的余额判断。

### 批量更新策略

现有 `BatchUpdateTypeUserQuota` 会聚合用户余额变化，不适合拆分钱包和流水，因为它会丢失：

- 扣费来源。
- 扣费明细。
- before/after。
- 幂等键。
- 事务边界。

建议：

- 所有钱包余额变化不再走 `BatchUpdateTypeUserQuota`。
- `used_quota`、`request_count` 仍可按现有策略批量更新。
- Token 额度是否继续批量更新可暂时不变，但需要注意与用户钱包扣费一致性。

## 统一钱包服务设计

建议在 `model` 或 `service` 层新增统一入口。底层事务和表更新建议放在 `model`，业务语义可放在 `service`。

### 核心类型

```go
type QuotaBucket string

const (
    QuotaBucketRecharge QuotaBucket = "recharge"
    QuotaBucketGift     QuotaBucket = "gift"
)

type QuotaTransactionType string

type QuotaTransactionRef struct {
    Type           string
    Source         string
    ReferenceType  string
    ReferenceID    string
    RequestID      string
    IdempotencyKey string
    OperatorID     int
    Metadata       map[string]interface{}
}

type QuotaDelta struct {
    QuotaDelta     int
    GiftQuotaDelta int
}

type QuotaBreakdown struct {
    QuotaDelta       int
    GiftQuotaDelta   int
    QuotaBefore      int
    GiftQuotaBefore  int
    QuotaAfter       int
    GiftQuotaAfter   int
    TransactionID    int
}
```

### 当前实际函数

```go
CreditRechargeQuota(userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
CreditRechargeQuotaTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
CreditGiftQuota(userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
CreditGiftQuotaTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
```

入账。

```go
DebitQuotaPreferGift(userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
DebitQuotaPreferGiftTx(tx *gorm.DB, userId int, amount int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
```

消费扣费，优先扣赠送额度。

```go
RefundQuotaByTransaction(originalTransactionID int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
RefundQuotaByBreakdown(userId int, breakdown QuotaDelta, ref QuotaTransactionRef) (*QuotaBreakdown, error)
```

退款或回滚。

```go
AdjustQuota(userId int, bucket QuotaBucket, mode string, value int, ref QuotaTransactionRef) (*QuotaBreakdown, error)
```

管理员调整。`bucket` 默认为 `recharge`。

读取余额：

```go
GetUserQuota(userId int, fromDB bool) (int, error)
```

`GetUserQuota` 保持兼容名称，但返回的是剩余总额度，即 `users.quota + users.gift_quota`。如果需要充值额度明细，应读取 `User.Quota`；如果需要赠送额度明细，应读取 `User.GiftQuota`；API 响应使用 `total_quota` 表示总余额。

`User.TotalQuota()` 是代码内统一计算总余额的辅助方法。

### 旧函数兼容

现有函数可以短期保留，但内部改为调用统一钱包服务：

```go
GetUserQuota(id, fromDB) => User.TotalQuota()
IncreaseUserQuota(id, quota, db) => CreditRechargeQuota(...)
IncreaseUserGiftQuota(id, quota, db) => CreditGiftQuota(...)
DecreaseUserQuota(id, quota, db) => DebitQuotaPreferGift(...)
```

不建议让新代码继续调用旧函数。旧函数仅作为兼容层。尤其需要注意：`IncreaseUserQuota` 默认增加充值额度，`DecreaseUserQuota` 会按赠送优先扣减，而不是只扣充值额度。

## 各业务入口改造清单

### 充值额度入口

以下继续进入充值额度 `users.quota`：

- `model/topup.go`
  - `Recharge`
  - `RechargeCreem`
  - `RechargeWaffo`
  - `RechargeWaffoPancake`
  - `ManualCompleteTopUp`
- `controller/topup.go`
  - 易支付成功回调入账
- 用外部支付真实购买的所有充值产品。

改造要求：

- 订单状态更新和 `CreditRechargeQuota` 放在同一事务里。
- `idempotency_key` 使用支付渠道和订单号。
- `RecordTopupLog`、`RecordTopupOperationLog` 保留，同时流水表记录结构化来源。
- 易支付回调必须在事务成功后再返回 `success`；验签失败、订单不存在、支付网关不匹配、入账事务失败时返回 `fail`，让上游支付平台继续重试。

### 赠送额度入口

以下改为进入 `users.gift_quota`：

- `model/redemption.go` 的 `Redeem`。
- `model/checkin.go` 的 `UserCheckin`。
- `model/user.go` 的新用户注册赠送 `QuotaForNewUser`。
- `model/user.go` 的被邀请人奖励 `QuotaForInvitee`。
- `model/user.go` 的邀请奖励划转 `TransferAffQuotaToQuota`，从 `aff_quota` 划转到 `gift_quota`。
- 后续所有活动赠送、营销赠送、系统奖励。

改造要求：

- 使用 `CreditGiftQuota`。
- 兑换码是否可区分付费兑换码和赠送兑换码可后续扩展。如果当前统一需求是兑换码全部作为赠送额度，则 `Redeem` 全部入 `gift_quota`。
- `TransferAffQuotaToQuota` 事务内同时更新 `aff_quota` 和 `gift_quota`，并插入 `aff_transfer` 流水。

### 管理员调额

当前 `controller/user.go` 的 `action=add_quota` 请求只有：

```json
{
  "id": 1,
  "action": "add_quota",
  "mode": "add",
  "value": 1000
}
```

需要新增参数：

```json
{
  "id": 1,
  "action": "add_quota",
  "mode": "add",
  "value": 1000,
  "quota_type": "recharge"
}
```

`quota_type` 可选值：

- `recharge`：充值额度，默认值。
- `gift`：赠送额度。

行为：

- `add`：给指定额度池增加。
- `subtract`：从指定额度池减少。减少不能让对应额度池为负。
- `override`：覆盖指定额度池。默认覆盖充值额度，不影响赠送额度。

不建议管理员 `override` 总余额，因为总余额不是数据库字段。若未来确实需要“覆盖总余额”，必须定义清楚优先减少哪个池，建议避免。

操作日志需要增加：

```json
{
  "quota_type": "recharge",
  "mode": "add",
  "value": 1000,
  "transaction_id": 123
}
```

### 钱包消费

改造点：

- `service/funding_source.go` 的 `WalletFunding` 不再直接 `DecreaseUserQuota`。
- `WalletFunding` 增加本次预扣明细：

```go
type WalletFunding struct {
    userId int
    requestId string
    consumed int
    consumedQuota int
    consumedGiftQuota int
    transactionIds []int
}
```

- `PreConsume` 调用 `DebitQuotaPreferGift`。
- `Settle(delta)` 正数补扣时继续调用 `DebitQuotaPreferGift`；负数退差额时必须按原扣费明细或最近补扣明细反向退。
- `Refund()` 调用 `RefundQuotaByTransaction` 或按 `consumedQuota/consumedGiftQuota` 退回。
- `RelayInfo` 同步保存 `WalletConsumedQuota`、`WalletConsumedGiftQuota`、`WalletTransactionIds`，用于日志、异步任务退款和 Midjourney 退款。

### Realtime / WSS 扣费

Realtime 需要特殊处理，因为连接过程中可能多次返回增量 usage，结束时又有最终总 usage。

当前原则：

- 入口仍创建 `BillingSession`。
- 流式阶段调用 `PreWssConsumeQuota` 时，如果存在 `BillingSession`，不再直接走旧 `PostConsumeQuota` 重复扣费，而是调用 `Billing.Reserve(targetQuota)` 将预留额度补到累计目标值。
- 结束阶段 `PostWssConsumeQuota` 调用 `SettleBilling`，只对最终用量和已预留额度做差额结算。
- 如果是没有 `BillingSession` 的旧路径，则将流式阶段已扣的钱包额度计入 `FinalPreConsumedQuota`，最终结算只处理差额，避免按最终总量重复扣一次。

必须测试：

```text
预扣 0 / 预扣 > 0
流式 usage 多次上报
最终 usage 大于累计流式 usage
最终 usage 小于累计流式 usage
连接异常结束 total_tokens = 0
```

### 旧兼容扣费链路

以下路径已尽量收敛到统一钱包服务，但仍作为兼容入口存在：

- `service/pre_consume_quota.go`
- `service/quota.go` 的 `PostConsumeQuota`
- `service/billing_session.go`
- `service/task_billing.go`
- `controller/task_video.go`
- `controller/midjourney.go`
- `model/subscription.go` 中用钱包购买订阅的扣减逻辑

原则：

- 扣费统一 `DebitQuotaPreferGift`。
- 退款统一 `RefundQuotaByTransaction` 或 `RefundQuotaByBreakdown`。
- 不再新增直接 `Update("quota", ...)`。
- 后续新增计费入口优先接入 `BillingSession`，只有无法接入时才走兼容 `PostConsumeQuota`。

### 订阅系统

订阅额度不属于本次拆分范围，但用钱包购买订阅时属于钱包扣费。

当前实现：

- `PurchaseSubscriptionWithBalance` 使用 `DebitQuotaPreferGift`。
- 交易类型为 `subscription_buy`。
- 若订阅购买失败或事务回滚，钱包流水也必须回滚。
- 余额购买订阅必须传入 `idempotency_key`，后端拒绝空 key。
- 服务端根据 `user_id + idempotency_key` 生成稳定 `trade_no`，避免重复点击或网络重试导致双扣、双开订阅。
- 在同一事务中锁定用户行，串行化同一用户余额购买订阅，避免 `MaxPurchasePerUser` 并发突破。
- 订阅结算差额、任务订阅退款、Reserve 回滚等不写 `quota_transactions`，而是写 `subscription_delta_records` 做幂等。

注意：

- 订阅资金来源不走 `gift_quota/quota` 拆分，订阅自身使用 `user_subscriptions.amount_used`。
- `subscription_first` / `wallet_first` 由 `BillingSession` 根据用户偏好选择。
- 订阅不能启用信任额度旁路，因为订阅预扣记录需要明确 `request_id` 和预扣数量。

### Token 额度

Token 额度保持现有逻辑，不拆充值/赠送。

注意：

- 用户钱包扣费成功但 Token 扣费失败时，当前 `BillingSession` 有资金和 Token 两阶段一致性风险。改造时尽量保持现有回滚顺序。
- 如果用户钱包已扣，Token 调整失败，必须记录错误并避免重复退款。

### 异步任务和 Suno 轮询

异步任务提交时会保存计费上下文：

```text
Task.PrivateData.BillingSource
Task.PrivateData.SubscriptionId
Task.PrivateData.TokenId
Task.PrivateData.WalletQuotaConsumed
Task.PrivateData.WalletGiftQuotaConsumed
Task.PrivateData.WalletTransactionIds
Task.PrivateData.BillingContext
```

失败退款和完成后的差额结算都依赖这些字段。

Suno 专用批量轮询必须遵循：

- 任务状态进入 `FAILURE` 或 `SUCCESS` 时使用 `Task.UpdateWithStatus(oldStatus)` 做 CAS。
- 只有 CAS 成功的 worker 才能执行退款或终态结算。
- 状态不变的普通字段更新不能触发退款。

订阅任务的后置差额调整必须使用稳定幂等键：

```text
task_subscription_delta:{task_id}:{target_quota}
```

钱包任务退款使用 `task_refund:{task_id}:{refund_amount}`。

### Midjourney

Midjourney 已增加钱包扣费明细字段：

```text
wallet_quota_consumed
wallet_gift_quota_consumed
wallet_transaction_ids
```

失败退款优先按这些明细退回。旧任务没有明细时，只能走 legacy 退款策略，当前实现会退到赠送额度，需在上线说明和对账中标记。

注意：Midjourney 当前仍未完全接入 `BillingSession`，仍属于需要专项回归的链路。

## API 改造清单

### `GET /api/user/self`

当前返回 `quota`、`used_quota`、`aff_quota` 等。

建议返回：

```json
{
  "quota": 1000,
  "gift_quota": 200,
  "used_quota": 300,
  "total_quota": 1200
}
```

含义：

- `quota`：剩余充值额度，也就是 `users.quota`。
- `gift_quota`：剩余赠送额度。
- `used_quota`：已用额度。
- `total_quota`：剩余总额度，等于 `quota + gift_quota`。

兼容说明：

- 新前端和新集成必须使用 `total_quota` 展示剩余总额度。
- 旧客户端如果只读取 `quota`，不会影响后端真实扣费，但会在用户拥有赠送额度时低估可用余额。
- 不建议为了兼容旧客户端把 API `quota` 改回总余额，否则会让 API `quota` 和数据库 `users.quota` 语义分裂，长期更难维护。

### `GET /api/user/`、`GET /api/user/:id`、搜索用户

用户列表和详情也需要返回同样字段，供用户管理展示。

### `POST /api/user/manage`

`action=add_quota` 新增参数：

```text
quota_type
```

默认 `recharge`。前端不传时保持原行为。

### 可选：新增钱包流水查询接口

长期建议新增：

```text
GET /api/user/quota-transactions
GET /api/user/:id/quota-transactions
```

支持筛选：

- `type`
- `source`
- `reference_type`
- `start_time`
- `end_time`
- `user_id`

## 前端改造清单

### Default 前端

#### 类型

需要改：

- `web/default/src/stores/auth-store.ts`
- `web/default/src/features/users/types.ts`
- `web/default/src/features/wallet/types.ts`
- 其他引用用户余额的类型文件

新增字段：

```ts
gift_quota?: number
total_quota?: number
```

注意当前已有 `aff_quota`，不要和 `gift_quota` 混淆。

#### 钱包页

涉及：

- `web/default/src/features/wallet/index.tsx`
- `web/default/src/features/wallet/components/wallet-stats-card.tsx`
- `web/default/src/features/wallet/components/affiliate-rewards-card.tsx`

展示建议：

- 主卡片默认展示“当前余额”，使用 `total_quota ?? quota + gift_quota`。
- 增加明细：
  - 充值额度
  - 赠送额度
  - 已用额度
  - 剩余总额度
- 邀请奖励卡片继续展示 `aff_quota`，但文案要说明划转后进入赠送额度。

#### 用户管理列表

涉及：

- `web/default/src/features/users/components/users-columns.tsx`

当前 `quota` 列默认展示：

```text
remaining = user.quota
total = used_quota + remaining
```

新逻辑：

```text
recharge = user.quota
gift = user.gift_quota
remaining = user.total_quota ?? recharge + gift
used = user.used_quota
usage_total = used + remaining
```

默认列标题仍可为“剩余额度/总额度”。鼠标移入展示：

```text
已用额度：xxx
充值额度：xxx
赠送额度：xxx
剩余额度：xxx
总额度：xxx
```

#### 用户管理调额弹窗

涉及：

- `web/default/src/features/users/components/user-quota-dialog.tsx`
- `web/default/src/features/users/api.ts`
- `web/default/src/features/users/types.ts`

新增额度类型选择：

```text
额度类型：
  充值额度（默认）
  赠送额度
```

提交 payload：

```ts
{
  id,
  action: 'add_quota',
  mode,
  value,
  quota_type: 'recharge' | 'gift'
}
```

预览文案要按当前选择的额度池计算：

- 选择充值额度：展示当前充值额度变化。
- 选择赠送额度：展示当前赠送额度变化。
- 另可附带展示调整后的剩余总额度。

### Classic 前端

#### 用户管理列表

涉及：

- `web/classic/src/components/table/users/UsersColumnDefs.jsx`

当前 `renderQuotaUsage` 展示：

```text
已用额度
剩余额度
总额度
```

需要改成鼠标移入展示：

```text
已用额度：xxx
充值额度：xxx
赠送额度：xxx
剩余额度：xxx
总额度：xxx
```

默认仍展示：

```text
剩余额度 / 总额度
```

#### 用户编辑与调额

涉及：

- `web/classic/src/components/table/users/modals/EditUserModal.jsx`

新增调额类型选择：

```text
额度类型：
  充值额度（默认）
  赠送额度
```

提交 `/api/user/manage` 时增加：

```json
"quota_type": "recharge" | "gift"
```

预览逻辑同 default 前端。

#### 钱包页和邀请奖励

涉及：

- `web/classic/src/components/topup/index.jsx`
- `web/classic/src/components/topup/RechargeCard.jsx`
- `web/classic/src/components/topup/InvitationCard.jsx`
- `web/classic/src/components/topup/modals/TransferModal.jsx`

改造点：

- 展示总余额、充值额度、赠送额度。
- 邀请奖励划转文案改为划转到赠送额度。
- 兑换码成功后更新本地用户状态时，不能只做 `quota + data`；兑换码现在增加的是 `gift_quota`，`quota` 仍是充值额度，`total_quota` 才是总余额。最稳妥做法是按 API 返回或重新拉取用户信息。

## 日志与审计

### 消费日志

`model.Log.Other` 建议增加：

```json
{
  "billing_source": "wallet",
  "quota_transaction_id": 123,
  "deducted_quota": 200,
  "deducted_gift_quota": 300
}
```

用于排查为什么某次扣费影响了哪个额度池。

### 操作日志

`operation_logs.detail` 中增加：

```json
{
  "quota_type": "gift",
  "mode": "add",
  "value": 1000,
  "transaction_id": 123
}
```

充值操作日志增加：

```json
{
  "quota_type": "recharge",
  "quota": 1000,
  "transaction_id": 123
}
```

兑换码、签到、邀请奖励增加：

```json
{
  "quota_type": "gift",
  "quota": 1000,
  "transaction_id": 123
}
```

## 数据迁移

迁移策略：

```text
ALTER TABLE users ADD COLUMN gift_quota INT DEFAULT 0
```

当前实际通过 GORM `AutoMigrate` 完成增量迁移，涉及：

```text
users.gift_quota
quota_transactions
subscription_delta_records
midjourneys.wallet_quota_consumed
midjourneys.wallet_gift_quota_consumed
midjourneys.wallet_transaction_ids
tasks.private_data（如果存量库尚未存在）
subscription_pre_consume_records（如果存量库尚未存在）
```

三库兼容要求：

- 使用 GORM `AutoMigrate` 或项目现有迁移模式。
- SQLite 不使用不兼容的 `ALTER COLUMN`。
- MySQL/PostgreSQL/SQLite 都保持 `INT DEFAULT 0` 语义。
- 新增 JSON/扩展信息字段使用 `TEXT` 或现有兼容类型，不使用 MySQL/PostgreSQL 专有 JSONB 特性。

历史数据：

```text
已有 users.quota 保持不变，全部视为充值额度和历史旧余额。
gift_quota 初始为 0。
```

不做历史日志回放，不根据充值记录或系统赠送日志倒推来源。

部署影响：

- 不会主动清空或重写 `users.quota`。
- 不会自动将历史余额拆到 `gift_quota`。
- 不会自动回填 `quota_transactions` 历史流水。
- 新增字段在大表上可能产生短时间元数据锁，建议低峰部署并提前备份。
- 回滚应用时建议保留新增 schema，不建议删除 `gift_quota` 或 `quota_transactions`，避免失去上线后产生的审计数据。

项目启动时还存在既有迁移检查：

```text
subscription_plans.price_amount -> decimal(10,6)
tokens.model_limits -> text
```

这些检查不是额度拆分独有，但部署新版本时也会执行，需要在 staging 环境一并验证。

## 测试计划

### 后端单元测试

钱包服务测试：

- `CreditRechargeQuota` 只增加 `quota`。
- `CreditGiftQuota` 只增加 `gift_quota`。
- `GetUserTotalQuota` 返回 `quota + gift_quota`。
- `DebitQuotaPreferGift` 在赠送额度足够时只扣 `gift_quota`。
- `DebitQuotaPreferGift` 在赠送额度不足时先扣完 `gift_quota` 再扣 `quota`。
- 余额不足时不更新用户余额，不插入流水。
- `RefundQuotaByTransaction` 按原流水反向退回。
- 幂等键重复时不重复加减额度。

并发测试：

- 同一用户多个并发扣费请求，总扣费不能超过总余额。
- 支付回调重复调用只入账一次。
- 任务失败退款重复调用只退款一次。

兼容测试：

- 旧 `GetUserQuota` 返回总余额。
- 旧 `DecreaseUserQuota` 走优先扣赠送逻辑。
- 旧 `IncreaseUserQuota` 默认增加充值额度。

### 后端集成测试

- 在线充值成功后增加充值额度。
- 兑换码成功后增加赠送额度。
- 签到成功后增加赠送额度。
- 注册赠送进入赠送额度。
- 邀请奖励划转进入赠送额度。
- 钱包购买订阅优先扣赠送额度。
- Relay 预扣、结算、失败退款后两类余额都准确。
- 异步任务失败退款后两类余额都准确。

### 前端测试

- 用户管理列表默认展示剩余额度/总额度。
- 用户管理列表 hover 展示已用、充值、赠送、剩余、总额度。
- 调额弹窗默认选择充值额度。
- 选择赠送额度后提交 payload 带 `quota_type=gift`。
- 钱包页展示总余额、充值额度、赠送额度。
- 兑换码成功后余额刷新正确。
- Default i18n 的新增 key 必须位于 `translation` 对象内，`bun run i18n:sync` 后 `missingCount/extrasCount/untranslatedCount` 应为 0。
- Classic i18n 至少补齐 `en/zh/zh-CN/zh-TW/fr/ja/ru/vi` 中的 `充值额度`、`赠送额度`、`额度类型`、`剩余总额`、`划转到赠送额度`。

## 当前落地状态与后续建议

### 已落地：基础数据与钱包服务

- 新增 `gift_quota`。
- 新增 `quota_transactions`。
- 实现统一钱包服务。
- `GetUserQuota` 改为总余额。
- Redis 缓存支持 `GiftQuota`。

### 已落地：核心消费链路

- 改造 `WalletFunding`。
- 改造 `BillingSession` 钱包预扣、结算、退款。
- 改造旧 `PreConsumeQuota` / `PostConsumeQuota`。
- 已增加部分关键钱包/任务测试；并发测试、三库集成测试仍建议补充。

### 已落地：所有入账入口分流

- 充值进入充值额度。
- 兑换码、签到、注册赠送、邀请奖励进入赠送额度。
- 管理员调额支持 `quota_type`。
- 支付回调、兑换码、签到加幂等流水。

### 已落地：异步任务与订阅主要链路

- 异步任务保存扣费流水 ID 或扣费明细。
- 任务失败退款按原来源退回。
- 钱包购买订阅走统一扣费。
- 订阅后置 delta 使用 `subscription_delta_records` 做幂等。
- Suno 轮询失败退款使用 CAS 防重复。

### 已落地：前端展示

- Default 钱包页和用户管理页。
- Classic 钱包页和用户管理页。
- i18n 同步。

### 后续建议：审计与查询

- 消费日志写入 `quota_transaction_id` 和扣费明细。
- 操作日志写入 `quota_type` 和流水 ID。
- 可选新增钱包流水查询页面。
- 补充定期对账脚本：注意消费链路不写流水（见「钱包流水与幂等表」一节），不能用「最新流水 after 值 == 当前余额」做校验；应按入账侧事件总量聚合对账，消费侧用 `logs`/`used_quota` 核对。
- 补充 10k RPM 压测：热点用户行锁、`quota_transactions` 写入 TPS、Redis 用户缓存失效率。

## 关键注意事项

- 不要新增 `paid_quota`，避免字段冗余。
- 不要在数据库存总余额，总余额始终计算。
- 不要让业务代码继续直接更新 `users.quota`。
- 钱包余额和流水必须同事务。
- 支付回调、退款、任务重试必须幂等。
- API `quota` 和数据库 `users.quota` 都表示充值额度，不再表示总余额。旧客户端如果继续把 `quota` 当总余额，会在存在 `gift_quota` 时低估余额。
- API `total_quota` 是剩余总额度，等于 `quota + gift_quota`，旧前端/第三方集成应优先读取该字段。
- 内部 `UserBase.Quota` 是鉴权/计费缓存中的总余额，不是公开 REST 用户对象的 `quota` 合同，不能对外复用其语义。
- `aff_quota` 是邀请待划转奖励，不等于 `gift_quota`。划转后才进入赠送额度。
- `used_quota` 仍然是累计消费统计，不拆充值/赠送。若未来需要统计“消耗了多少赠送额度/充值额度”，应从 `quota_transactions` 聚合。
- `checkin` 统计接口中的 `total_quota` 表示累计签到获得额度，不是钱包剩余总额度，前端和第三方集成不要混用。
- `quota_transactions` 是上线后的资金审计依据，回滚应用时应保留新增表和字段，通过流水反向补偿处理资金异常。
