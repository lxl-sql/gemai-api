# API Key 使用来源聚合、计数与弹窗展示 — 实施说明

> 2026-07-31 最终变更：生产尚未发布旧方案，已取消日志实时 rollup、历史日志回填和
> 切换点。本文件保留为早期设计记录，不再作为实施依据。最终实现、发布和回滚以
> `docs/修改需求/260731/API Key使用来源直接聚合与生产切换.md` 为准。

## 1. 目标与边界

在 API Key 行操作菜单增加「使用来源」，弹窗展示去重后的 IP、客户端、首次/最后出现、
最近成功和最近错误时间，以及该来源的成功、错误和总请求记录次数。

明确不做：

- 不在用户打开弹窗时查询 5 亿级 `logs` 表。
- 不用 Redis 承担聚合写缓冲、水位或查询缓存。
- 不建立按请求或永久分钟桶增长的计数明细表。
- 不把客户端版本号折叠进同一个来源；仅复用日志已有的 UA 空白归一化和 512 字符截断。

`token_usage_sources` 本身就是日志表之上的持久化物化缓存。弹窗只查询这张有界小表，
通过水位时间向用户说明数据的新鲜程度。

## 2. 数据模型

### 2.1 `token_usage_sources`

唯一键为 `(token_id, source_key)`，其中 `source_key` 是规范化 IP 和完整规范化 UA
经长度前缀编码后的 SHA-256。

保存字段：

- `user_id`、`token_id`、`source_key`
- `ip`、`user_agent`
- `first_seen_at`、`last_seen_at`
- `last_success_at`、`last_error_at`
- `success_count`、`error_count`
- `forward_counted_through`、`backfill_counted_from`、`backfill_counted`
- `updated_at`

接口中的 `request_count` 不重复落库，固定由 `success_count + error_count` 计算。
如果已有未计数版本的来源表，Master 会先用跨 SQLite/MySQL/PostgreSQL 的 `ALTER TABLE ADD COLUMN`
逐列补入带零默认值的计数和游标列，再执行常规 `AutoMigrate`；步骤可在中断后幂等重跑。

索引：

- UNIQUE `(token_id, source_key)`
- `(token_id, last_seen_at)`
- `(user_id, token_id)`

### 2.2 `token_usage_source_meta`

每个 Token 一行：

- `token_id` 主键
- `user_id`
- `tracking_enabled`
- `tracking_start`
- `purged_at`
- `truncated`
- `updated_at`

这张表同时承担三种职责：

1. 记录用户是否允许来源追踪以及追踪起点。
2. 作为聚合与删除共用的协调行。
3. Key 删除后保留永久墓碑，阻止历史回填重新生成来源。

聚合和删除只锁 meta 行，不锁计费请求频繁更新的 `tokens` 行。

### 2.3 全局状态

复用 `log_stat_rollup_states`，新增状态名 `token_usage_source_v1`：

- `watermark`：实时连续覆盖上界。
- `backfill_cursor`：历史回填下界。
- `coverage_start`：当前能够继续回填的目标下界。

分钟统计的 prune/replace 逻辑不复用于来源表。

### 2.4 `token_usage_source_reconcile_states`

单例状态行保存滚动发布对账游标和最近一次完整巡检时间：

- `token_cursor`：当前 Token/meta 合并扫描位置。
- `reconciled_at`：最近一次完整扫描结束时间。

对账每个事务最多锁 100 个 meta，默认每分钟执行最多 10 个短事务。它会补齐旧实例创建但缺失
meta 的 Key、墓碑化旧实例删除的 Key/用户。主节点启动时
的初始 meta 回填只查询缺失行，不再对所有已有 meta 重复执行批量 `INSERT ... DO NOTHING`。

### 2.5 `token_usage_source_count_progresses`

单例状态行只保存当前正在计数的精确时间范围：

- `direction`：`forward` 或 `backfill`
- `range_start`、`range_end`
- `merge_started`
- `updated_at`

它不会随日志量增长。计数块一旦开始分批写入，后续任务必须复用同一范围；所有 Token 批次
完成后，水位推进和进度清空在同一主库事务内提交。
如果进程中断后管理员推进了日志清理边界，只废弃与已删除区间相交的历史回填进度；仍位于
保留日志区间内的精确进度继续复用，不能跨过尚未聚合的日志。

## 3. 幂等聚合

日志查询只处理 type=2 消费日志和 type=5 错误日志，以 `created_at` 时间块驱动：

```sql
SELECT user_id, token_id, ip, user_agent,
       MIN(created_at),
       MAX(created_at),
       MAX(CASE WHEN type = 2 THEN created_at ELSE 0 END),
       MAX(CASE WHEN type = 5 THEN created_at ELSE 0 END),
       SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END),
       SUM(CASE WHEN type = 5 THEN 1 ELSE 0 END)
FROM logs
WHERE created_at >= ? AND created_at < ?
  AND token_id > 0
  AND type IN (2, 5)
  AND (ip <> '' OR user_agent <> '')
GROUP BY user_id, token_id, ip, user_agent
LIMIT max_groups + 1
```

`min/max` 在重复扫描下天然幂等，因此实时任务仍可重复修复最近 5 分钟。次数只在实时水位之后
或历史回填游标之前累计，修复区间只更新时间字段。
时间水位无法证明已经计数的区间不会再出现迟到日志，因此当前版本保守地固定返回
`counts_complete=false`，不把覆盖范围完成等同于次数绝对精确。

首次初始化状态时执行一次有界索引查询
`SELECT created_at FROM logs ORDER BY created_at ASC LIMIT 1`，把配置的历史目标夹到当前原始日志
实际下界以内；不执行会聚合全表的 `MIN(created_at)`。

主库按最多 25 个 Token 一批处理：

1. 按 Token ID 排序并锁对应 meta 行。
2. 跳过关闭追踪或已经 `purged_at > 0` 的 Token。
3. 读取当前最多 K 个来源。
4. 与本块来源按 `source_key` 合并。
5. 按 `last_seen_at DESC, source_key ASC` 确定性排序。
6. 保留前 K 个，删除被挤出的旧来源。
7. 首次超限时将 `truncated` 从 false 改为 true。

来源被淘汰后再次出现时，次数从重新进入保留集合时开始。因此 `truncated=true` 时接口固定
返回 `counts_complete=false`，前端同时展示“最近来源”和“次数统计尚未完整”，不把有界数据
声明为完整历史计数。

来源批次可以独立提交。每个来源保存实时和历史两个计数游标；任务在写入前持久化本次精确
计数范围。如果进程在部分批次提交后中断，下一轮复用同一范围，已提交来源跳过计数、未提交
来源继续累计。全部批次成功后，推进全局水位并清空计数进度。
实时任务发现中断遗留的历史计数范围时，会先完成该固定范围再继续推进实时水位，即使管理员
已经关闭新的历史回填，也不会让遗留进度永久阻塞实时聚合。

日志清理如果遇到已经开始合并、且与删除边界相交的历史范围，会清零来源计数游标并从当前
实时水位重新回填仍保留的 `[清理边界, 实时水位)`，避免部分 Token 已计数、部分 Token 未计数
却被声明为完整。来源身份和时间字段不因此删除。

成功次数是 type=2 消费日志行数，错误次数是 type=5 错误日志行数。Token 尚未解析、
`token_id=0`、IP/UA 均为空或消费日志功能关闭的请求不进入对应来源计数；内部上游重试不单独计次。

## 4. 实时与历史任务

新增任务类型：

- `token_usage_source_rollup`：固定每分钟调度。
- `token_usage_source_backfill`：非定时任务，由实时任务在追上水位后按需入队。
- `token_usage_source_reconcile`：固定每分钟调度，按游标对账滚动发布期间的混合版本 CRUD。

实时和历史任务复用 `log_stat_maintenance` 锁，与日志清理和现有日志统计互斥，但不进入现有
分钟统计的事务或失败域。对账任务只访问主库并使用独立游标，不占用日志维护锁。

配置保存在 `options` 表的 `token_usage_source_setting.*` 分组中，由“系统设置 → 运营管理 →
日志维护”动态管理，各节点按现有配置同步机制收敛：

| 配置 | 默认值 |
|---|---:|
| `token_usage_source_setting.enabled` | `false` |
| `token_usage_source_setting.reconcile_enabled` | `false` |
| `token_usage_source_setting.backfill_enabled` | `false` |
| `token_usage_source_setting.reconcile_batch_size` | `100` |
| `token_usage_source_setting.reconcile_max_batches_per_run` | `10` |
| `token_usage_source_setting.backfill_days` | `90` |
| `token_usage_source_setting.backfill_chunk_seconds` | `300` |
| `token_usage_source_setting.chunk_timeout_seconds` | `30` |
| `token_usage_source_setting.max_groups_per_chunk` | `20000` |
| `token_usage_source_setting.max_live_chunks_per_run` | `60` |
| `token_usage_source_setting.late_log_lag_seconds` | `300` |
| `token_usage_source_setting.max_watermark_lag_seconds` | `1800` |
| `token_usage_source_setting.max_sources_per_token` | `500` |

不保留旧环境变量兼容。Master 首次启动会把默认值补入缺失的
`token_usage_source_setting.*` 选项，此后统一以数据库设置为准。

分组结果超过上限或查询到达超时时间时不落库，时间块自动减半，最小 10 秒；只有最小块仍然
超限或超时才把本轮任务标记为失败。回填每次只处理一个块，
下一分钟由实时任务再次决定是否入队，因此不会形成无休止的自调度循环。

## 5. 隐私与 CRUD

`record_ip_log` 同时作为来源追踪授权。关闭时在同一主库事务内关闭该用户全部 meta 并删除
已有来源；开启时清空旧来源、把有效 Key 的 `tracking_start` 设为当前时间，只统计重新授权
后的日志。新建 Key 和滚动对账均读取用户当前设置，不能由历史任务绕过该开关。
设置切换、Key 创建、用户删除和滚动对账对 User 与 meta 使用一致的加锁顺序；即使旧实例已
删除全部 Key，关闭授权仍会清理按用户遗留的来源和 meta。
Master 启动补齐缺失 meta 时会锁定 User、在事务内重新确认有效 Key，并统一创建为关闭状态；
旧节点全部下线后再由滚动对账按用户当前授权启用，避免旧版本并发删除留下可聚合的 meta。

边界时间块如果跨过 `tracking_start`，整组跳过，不用近似数据污染首次出现时间和次数。

Key 生命周期：

| 操作 | 来源处理 |
|---|---|
| 创建 | 同事务按用户 `record_ip_log` 创建启用或关闭状态的 meta |
| 改名、额度、状态 | 不变 |
| 轮换密钥 | 保留，Token ID 未变化 |
| 单个删除 | 锁 meta、写永久墓碑、删除来源、软删 Token |
| 批量删除 | Token ID 排序后逐个执行相同流程 |
| 用户软删/硬删 | 为该用户全部 meta（包括已无 Token 的遗留 meta）写墓碑并删除来源 |

聚合先锁 meta 时，删除等待本批短事务结束后清空；删除先锁时，后续聚合看到墓碑直接跳过。
用户删除会先按 Token ID 锁定该用户全部 meta，再补齐缺失墓碑，避免与聚合任务发生锁顺序反转。
计费链路不访问 meta 表。

## 6. API 与前端

接口：

```text
GET /api/token/:id/usage-sources?p=1&page_size=50
```

- 仅注册在 `UserAuth` Token 路由，不暴露给 delegated OAuth Token 管理路由。
- 必须校验 Token 属于当前用户。
- 受现有搜索限流保护。
- 只读聚合表，不查询原始日志。
- 返回追踪状态、起点、水位、回填和截断状态。
- 返回 `success_count`、`error_count`、派生的 `request_count`，以及计数覆盖起止；当前版本不承诺迟到日志下的绝对完整计数。
- meta 尚未被滚动对账补齐时返回不可用空结果，不把混合版本窗口暴露为接口错误。
- 系统关闭 `token_usage_source_setting.enabled` 时，接口返回不可用空结果，不返回已聚合的 IP/UA。

default 前端：

- `/api/status` 仅在 `token_usage_source_setting.enabled=true` 时发布能力标志。
- Key 行操作菜单只在拿到当前后端的能力标志后显示「使用来源」；旧后端不返回该字段时默认隐藏。
- 本地缓存的旧能力值仅作为占位数据，不会在当前后端状态请求完成前打开入口。
- React Query 客户端缓存 15 秒。
- 弹窗展示 IP、客户端、成功/错误/总次数、首次/最后出现、最近成功、最近错误。
- 次数可能受历史回填、来源截断和迟到日志影响，统一展示“次数统计尚未完整”和当前计数起点。
- 明确提示功能未启用、用户未授权、消费日志关闭、历史回填中或只保留最近来源。

## 7. ClickHouse 修复

ClickHouse `logs` 建表 DDL 必须包含：

```sql
user_agent String DEFAULT ''
```

升级时在 `CREATE TABLE IF NOT EXISTS` 后执行：

```sql
ALTER TABLE logs ADD COLUMN IF NOT EXISTS user_agent String DEFAULT ''
```

这样同时覆盖新表和既有 ClickHouse 日志表。

## 8. 上线顺序

1. 先升级主节点完成四张新表迁移、缺失 meta 初始回填和 ClickHouse 补列，保持使用来源设置关闭。
2. 滚动升级全部后端节点；此阶段入口仍隐藏，旧实例缺少新接口不会影响用户。
3. 在系统设置中开启“对账 API Key 来源元数据”，旧节点全部摘除后至少等待一次新的完整对账周期完成。
4. 开启“API Key 使用来源”，同时启用实时聚合和前端入口，观察实时水位、超时缩块、日志库耗时和主库写入。
5. 验证弹窗只读取聚合表；多节点配置同步最长等待现有 `SYNC_FREQUENCY` 周期。
6. 观察至少 24 小时后开启“回填历史使用来源”。
7. 从默认 5 分钟块开始，根据系统任务耗时逐步调整，不增加请求时原始日志查询。

回滚时先在系统设置中关闭历史回填、对账和使用来源，再回滚二进制。新增表和 ClickHouse
新列保留，旧版本会忽略。

## 9. 验收

- 同一日志时间块重复执行，来源行数与时间字段不变化。
- 实时计数块在任意 Token 批次后中断并重试，成功和错误次数均不重复。
- 历史计数任务中断后，下一次实时任务先恢复遗留范围，不会被方向锁永久阻塞。
- 最近 5 分钟修复扫描只修复时间字段，不重复累计次数。
- 即使历史覆盖完成，接口也不会把无法识别的迟到日志计数声明为绝对完整。
- 实时和回填在切点使用半开区间，次数不重叠。
- 超过单 Key 上限后始终保留确定性的最近 K 个来源。
- 达到来源上限后 `counts_complete=false`，被淘汰后重新出现的来源不声明为完整历史计数。
- 删除 Key 后来源立即不可见、物理行被删除，历史重放无法复活。
- 删除用户时，无 Token 对应的遗留 meta 也会立即墓碑化，历史重放无法复活来源。
- 关闭 `record_ip_log` 后来源立即删除；重新开启只接受新 `tracking_start` 之后的数据。
- 用户没有活动 Key 但存在混合版本遗留来源时，关闭 `record_ip_log` 仍立即清理。
- 日志清理只收缩已删除区间，不跨过仍存在但尚未回填的日志。
- 日志清理与部分提交的历史计数相交时，计数重置并重新覆盖保留区间，不声明混合计数完整。
- 混合版本期间由旧实例创建、删除 Key，对账完整跑完后状态正确收敛。
- Master 启动补齐的 meta 在滚动对账确认前保持关闭，旧节点并发删除不能产生可聚合状态。
- 聚合查询超时时块大小按 300/60 秒逐级缩小到 10 秒，不重复卡在同一大块。
- 旧后端、状态占位缓存或 UI 开关关闭时不展示入口。
- 系统关闭使用来源时，直接请求接口也不返回历史 IP/UA。
- 非归属用户不能读取来源。
- Redis 不可用不影响接口。
- SQLite、MySQL、PostgreSQL 主库语义一致。
- ClickHouse 新建表与既有表均具有 `user_agent`。
- 生产启用回填前完成真实日志库只读查询耗时验证。
