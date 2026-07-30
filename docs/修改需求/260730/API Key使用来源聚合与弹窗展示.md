# API Key 使用来源聚合与弹窗展示 — V1 实施说明

## 1. 目标与边界

在 API Key 行操作菜单增加「使用来源」，弹窗展示去重后的 IP、客户端、首次/最后出现、
最近成功和最近错误时间。

V1 明确不做：

- 不存精确请求次数，避免历史重扫造成重复计数。
- 不在用户打开弹窗时查询 5 亿级 `logs` 表。
- 不用 Redis 承担聚合写缓冲、水位或查询缓存。
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
- `updated_at`

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
meta 的 Key、墓碑化旧实例删除的 Key/用户，并按当前 `record_ip_log` 收敛追踪状态。主节点启动时
的初始 meta 回填只查询缺失行，不再对所有已有 meta 重复执行批量 `INSERT ... DO NOTHING`。

## 3. 幂等聚合

日志查询只处理 type=2 消费日志和 type=5 错误日志，以 `created_at` 时间块驱动：

```sql
SELECT user_id, token_id, ip, user_agent,
       MIN(created_at),
       MAX(created_at),
       MAX(CASE WHEN type = 2 THEN created_at ELSE 0 END),
       MAX(CASE WHEN type = 5 THEN created_at ELSE 0 END)
FROM logs
WHERE created_at >= ? AND created_at < ?
  AND token_id > 0
  AND type IN (2, 5)
  AND (ip <> '' OR user_agent <> '')
GROUP BY user_id, token_id, ip, user_agent
LIMIT max_groups + 1
```

`min/max` 在重复扫描下天然幂等，因此实时任务可以重复修复最近 5 分钟，历史坏块也可以安全重跑。

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

来源批次可以独立提交；全部批次成功后才推进全局水位。进程中断时旧水位不会前进，
下一轮重扫得到相同结果。

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

`record_ip_log` 作为来源追踪的用户授权：

- 关闭时删除该用户全部来源，并把所有未删除 Key 的 meta 设为关闭。
- 开启时只对之前关闭的 meta 设置新的 `tracking_start=当前时间`。
- 边界时间块如果跨过 `tracking_start`，整组跳过，不用近似时间污染首次出现记录。
- 关闭后即使原始日志仍有 IP/UA，聚合任务也不会写入来源表。

Key 生命周期：

| 操作 | 来源处理 |
|---|---|
| 创建 | 同事务创建 meta，读取用户当前 `record_ip_log` |
| 改名、额度、状态 | 不变 |
| 轮换密钥 | 保留，Token ID 未变化 |
| 单个删除 | 锁 meta、写永久墓碑、删除来源、软删 Token |
| 批量删除 | Token ID 排序后逐个执行相同流程 |
| 用户软删/硬删 | 为该用户所有 Token 写墓碑并删除来源 |

聚合先锁 meta 时，删除等待本批短事务结束后清空；删除先锁时，后续聚合看到墓碑直接跳过。
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
- meta 尚未被滚动对账补齐时返回不可用空结果，不把混合版本窗口暴露为接口错误。

default 前端：

- `/api/status` 仅在 `token_usage_source_setting.enabled=true` 且未触发紧急熔断时发布能力标志。
- Key 行操作菜单只在拿到当前后端的能力标志后显示「使用来源」；旧后端不返回该字段时默认隐藏。
- 本地缓存的旧能力值仅作为占位数据，不会在当前后端状态请求完成前打开入口。
- React Query 客户端缓存 15 秒。
- 弹窗展示 IP、客户端、首次/最后出现、最近成功、最近错误。
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

1. 先升级主节点完成三张新表迁移、缺失 meta 初始回填和 ClickHouse 补列，保持使用来源设置关闭。
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
- 超过单 Key 上限后始终保留确定性的最近 K 个来源。
- 删除 Key 后来源立即不可见、物理行被删除，历史重放无法复活。
- 关闭 `record_ip_log` 后来源被删除，重新开启不会回填开启前数据。
- 混合版本期间由旧实例创建、删除 Key，或修改 `record_ip_log`，对账完整跑完后状态正确收敛。
- 聚合查询超时时块大小按 300/60 秒逐级缩小到 10 秒，不重复卡在同一大块。
- 旧后端、状态占位缓存或 UI 开关关闭时不展示入口。
- 非归属用户不能读取来源。
- Redis 不可用不影响接口。
- SQLite、MySQL、PostgreSQL 主库语义一致。
- ClickHouse 新建表与既有表均具有 `user_agent`。
- 生产启用回填前完成真实日志库只读查询耗时验证。
