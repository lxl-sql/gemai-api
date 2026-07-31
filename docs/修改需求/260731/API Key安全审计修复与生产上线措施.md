# API Key 安全审计修复与生产上线措施

> 使用来源部分已被“请求终态直接批量聚合”方案取代：不再扫描或回填 `logs`，也没有
> 切换时间和历史回填开关。本文中涉及来源日志聚合、`count_generation` 切换、
> watermark/backfill 的内容仅保留为审计历史，不再作为发布步骤。来源发布以同目录
> `API Key使用来源直接聚合与生产切换.md` 为准；安全策略与 Redis 部分继续有效。

日期：2026-07-31  
适用范围：`controller/`、`dto/`、`model/`、`service/`、`web/default/` 中 2026-07-30 API Key 安全与使用来源改造  
生产拓扑：固定 1 个 master、多个 slave，共用 PostgreSQL 与 Redis；当前日在线用户约 2 万，业务峰值约 5000 RPM

## 1. 实施结论

本次只修复审计确认的七项问题，不调整现有安全策略默认值、不扩大数据采集范围、不改变计费规则。

生产上线必须经过“功能保持关闭 → 全实例升级 → 预发布验证 → 小范围启用”的闸门。自动化测试通过只能证明代码级回归，不替代真实 PostgreSQL、Redis Cluster、反向代理和多实例流量验证。

## 2. 已实施修改

### 2.1 安全策略的字段掩码与并发保存

- `UpsertTokenSecurityProfileWithFieldMask` 的读取、行锁、字段合并、校验和保存已合并到同一个数据库事务。
- 更新已有 scope 时先按 `scope_type + scope_value` 执行 `SELECT ... FOR UPDATE`，再以数据库最新值补齐请求中未提供的 RPM 和用户共享字段。
- 创建、普通更新和带字段掩码更新共用同一保存入口，避免事务边界和缓存处理分叉。
- 事务提交后删除共享 Redis 缓存，让任意实例下一次读取数据库最新值；缓存删除失败会记录错误并通过接口返回 `cache_synchronized=false`，但不会回滚已经成功提交的数据。
- 平台、分组和用户 profile 缓存统一使用 `{profiles}` Redis Cluster hash tag；一次 `MGET` 的所有 key 位于同一 slot，避免集群代理返回 `CROSSSLOT` 后每个请求都回退 PostgreSQL。

这消除了两个管理员并发保存同一 scope 时，后提交请求用事务外旧值复活已清零限制的窗口。

### 2.2 `null` 与显式 `0` 的兼容语义

- 请求 JSON 中未出现字段或字段值为 `null`：保留数据库现值。
- 明确传入数值 `0`：清除对应限制。
- 新建策略仍按零值创建。

覆盖字段：`sustained_rpm`、`user_sustained_rpm`、`user_burst_capacity`、`user_max_concurrency`、`user_hourly_quota`、`user_daily_quota`。

### 2.3 Redis 额度窗口结算

- 删除只被测试调用、且只能处理第一个窗口的 `finalizeWindow`。
- 生产与测试统一通过 `TokenBudgetReservation.Finalize` 结算全部用户级和单 Key 窗口。
- 删除不可达的无 hash tag reservation key 兜底。每个 Lua 调用的小时、每日和 reservation key 都由对应窗口构造函数生成，并共享 `{userId}` 或 `{tokenId}` hash tag。
- 用户窗口和 Key 窗口分别执行 Lua；单次 Lua 的全部 `KEYS` 位于同一个 Redis Cluster slot。
- 集成测试覆盖同一 reservation 重复结算时，两个窗口都保持幂等且最终计数一致。

### 2.4 并发租约

- 删除 `trackedConcurrencyKeys` 的不可达兜底。
- 租约只续期和释放实际成功获取并记录在 `concurrencyKeys` 中的 Redis key。
- 测试构造租约时也必须明确提供已获取的 key，不再依赖生产代码猜测。

### 2.5 日志清理与使用来源计数

本节描述日志聚合阶段的清理兼容。启用
`token_usage_source_setting.direct_counting_cutover_at` 并到达切换点后，新请求
改为直接批量计数，日志清理不再轮换使用来源计数代次；具体步骤见同目录
`API Key使用来源直接聚合与生产切换.md`。

原清理事务会对 `token_usage_sources` 执行全表计数清零，行数较大时会延长 PostgreSQL 主库写事务、制造 WAL 和副本回放压力。

现改为逻辑计数代次：

- `log_stat_rollup_states`、`token_usage_source_count_progresses`、`token_usage_sources` 新增 `count_generation`，默认值为 `1`。
- 只有当日志清理与已经开始合并的回填区间相交时，才在小型状态行上递增代次，并把回填游标重置到 watermark。
- API 只展示当前代次的计数；旧代次计数立即按零展示。
- 后台重新扫描仍保留的 `[cleanup_target, watermark)` 日志。某来源第一次在新代次合并时，原计数和幂等游标在该来源行内重置，再写入新计数。
- 没有保留日志的旧来源不会触发批量 UPDATE，其旧计数保持不可见。

因此日志清理事务不再按来源表规模持锁；代价是发生交叉清理后，使用来源计数会短暂显示为零并异步恢复，这是刻意的安全降级。

### 2.6 i18n

已通过项目 i18n 脚本从 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi` 删除不再被源码引用的旧 RPS 摘要 key，并保留新 `Rate {{rate}}, ...` key。

## 3. 数据库兼容与迁移

迁移使用 GORM 和项目已有的跨数据库迁移路径，兼容 SQLite、MySQL 5.7.8+、PostgreSQL 9.6+。

新增列均为非空、默认 `1` 的整数列：

```text
token_usage_sources.count_generation
token_usage_source_count_progresses.count_generation
log_stat_rollup_states.count_generation
```

PostgreSQL 生产发布时由唯一 master 先执行迁移。slave 在 master 迁移成功前不得接收新版本任务流量。

## 4. 多实例生产发布顺序

### 4.1 发布前

1. 在管理设置中把以下三项全部设为 `false`，并等待至少两个配置同步周期：
   - `token_usage_source_setting.enabled`
   - `token_usage_source_setting.reconcile_enabled`
   - `token_usage_source_setting.backfill_enabled`
2. 冻结管理员安全策略编辑，不新增 RPM 或用户共享限额。
3. 记录 master/slave 镜像 digest、数据库备份点、Redis 拓扑、401/403/429/5xx、P95/P99、PostgreSQL 锁等待和副本延迟基线。
4. 确认所有应用实例连接同一 PostgreSQL 主库写入口和同一 Redis 集群，且 `NODE_TYPE`、`NODE_NAME` 配置正确。

### 4.2 发布

1. 先更新唯一 master，等待启动迁移完成。
2. 在 PostgreSQL 核对三个 `count_generation` 列存在且默认值为 `1`。
3. master 健康后逐台更新 slave；每台更新完成后检查健康、数据库、Redis，再更新下一台。
4. 确认旧版本实例全部退出后，解除管理员策略编辑冻结。
5. 在预发布或一个低风险用户 scope 上启用 RPM/用户共享限制；不要直接修改平台默认策略。
6. Redis Cluster 验证通过后再扩大范围。
7. 最后启用使用来源聚合；先启用 `reconcile_enabled`，确认元数据收敛，再启用 `enabled`，最后按需启用 `backfill_enabled`。

混合版本期间不得启用使用来源聚合或新增安全字段。旧版本任务不知道计数代次，可能推进游标但无法写入新代次。

## 5. 必须执行的生产验收

### 5.1 PostgreSQL

```sql
SELECT table_name, column_name, column_default, is_nullable
FROM information_schema.columns
WHERE (table_name, column_name) IN (
  ('token_usage_sources', 'count_generation'),
  ('token_usage_source_count_progresses', 'count_generation'),
  ('log_stat_rollup_states', 'count_generation')
)
ORDER BY table_name;

SELECT name, coverage_start, watermark, backfill_cursor, count_generation
FROM log_stat_rollup_states
WHERE name = 'token_usage_source_v1';

SELECT count(*) AS lock_waiters
FROM pg_stat_activity
WHERE wait_event_type = 'Lock';
```

验收标准：

- 三列均存在、`NOT NULL`、默认值为 `1`。
- 日志清理时不再出现对 `token_usage_sources` 的无条件全表 UPDATE。
- 清理事务期间无持续锁等待；PostgreSQL 副本延迟和 WAL 增长不显著偏离基线。
- 若清理命中已开始的回填，状态代次增加、计数短暂归零，随后 backfill cursor 单调回退并恢复计数。

### 5.2 Redis Cluster

使用真实 Cluster（不能只用单节点 Redis）执行：

- 同一用户两把 Key 共享 RPM、并发、小时和每日额度；
- 单 Key 额度与用户共享额度同时预留和结算；
- 成功、上游失败、客户端中断、重复 Finalize、预留中途失败；
- Redis 日志和应用日志中无 `CROSSSLOT`；
- 结算后用户窗口与 Key 窗口都不残留预扣量；
- Redis 暂时不可用时，`fail_closed` 的拒绝/降级行为与现有生产配置一致。

### 5.3 管理接口

- 两个管理员并发更新同一 scope 中受字段掩码保护的不同字段，最终结果同时保留双方更新。
- 旧管理端发送 `null` 不清除限制。
- 显式发送 `0` 能清除限制。
- 任一实例保存后，其他实例读取到相同策略；若响应为 `cache_synchronized=false`，先恢复 Redis 并重新保存，不继续扩大灰度。

## 6. 监控和回滚

重点监控：

- API：401、403、429、5xx，安全策略保存失败，`cache_synchronized=false`。
- Redis：`CROSSSLOT`、超时、连接失败、额度结算失败、租约续期/释放失败。
- PostgreSQL：锁等待、长事务、WAL 速率、主从复制延迟、`token_usage_sources` dead tuples。
- 后台任务：count progress 长时间不清空、watermark 不前进、backfill cursor 不回退。

回滚顺序：

1. 立即停止扩大安全策略灰度，并把使用来源三项开关设为 `false`。
2. 清空或改回本次新增的 RPM/用户共享策略值后，确认全部实例缓存一致。
3. 回滚应用镜像。不要删除新增列，不要清空 Redis，不要手工修改计数代次。
4. 如果已经发生计数代次切换，保持使用来源功能关闭，待修复版本重新完成回填后再开放。

数据库新增列是向后兼容列，常规回滚不做 DDL 回退。

## 7. 验证边界

仓库测试需要覆盖 Go 构建、目标包测试、前端类型检查、前端测试和 i18n 同步。以下项目只能在生产同构预发布环境确认：

- PostgreSQL 主从复制延迟和真实表规模下的清理行为；
- Redis Cluster slot 与故障切换；
- 多实例缓存传播；
- 真实反向代理、客户端中断和上游超时下的额度结算。

未完成上述生产同构验收前，不应把“单元测试通过”表述为已完成生产验收。

## 8. 本地验证记录

2026-07-31 已执行：

| 检查 | 结果 |
| --- | --- |
| `go build -p 1 ./...` | 通过 |
| `go test -p 1 ./... -count=1` | 通过 |
| `bun run typecheck` | 通过 |
| `bun run test` | 24 文件、101 用例通过 |
| `bun run build` | 通过 |
| `bun run i18n:sync` | 通过，重复执行无 locale diff |
| 旧 i18n key 全仓检索 | 无引用、无残留 |
| 审计所列死 helper、不可达 reservation key、并发 key 兜底和全表 UPDATE 模式检索 | 无残留 |
| 管理员 profile 缓存 Redis Cluster slot 回归 | 平台、分组、用户 cache key 共享 `{profiles}` tag，测试通过 |

`TestTokenSecurityRedisScriptsRemainIdempotent` 因本机未配置 `TOKEN_SECURITY_REDIS_TEST_URL` 而跳过。Redis Cluster、真实 PostgreSQL 主从复制和多实例缓存传播仍必须按第 5 节在生产同构预发布环境验收。
