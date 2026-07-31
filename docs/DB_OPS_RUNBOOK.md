# 数据库运维手册（PostgreSQL）

针对多实例部署下 PostgreSQL 高并发问题的紧急恢复、日常巡检与生产配置清单。
背景：曾出现 `users`/`channels` 表行锁排队、语句被取消（`canceling statement due to user request`）、
认证超时（`canceling authentication due to timeout`）、autovacuum 被持续跳过（`skipping vacuum ... lock not available`）
导致表膨胀的连锁故障。代码层修复见 `model/quota_transaction.go`（PostgreSQL 原子扣费快路径 + 每用户并发闸门）。

---

## 一、紧急恢复（故障发生时）

### 1. 找出锁阻塞源头

```sql
SELECT blocked.pid  AS blocked_pid,
       blocked.query AS blocked_query,
       blocking.pid AS blocking_pid,
       blocking.state AS blocking_state,
       blocking.xact_start,
       blocking.query AS blocking_query
FROM pg_stat_activity blocked
JOIN pg_stat_activity blocking
  ON blocking.pid = ANY(pg_blocking_pids(blocked.pid));
```

若 `blocking_state` 为 `idle in transaction` 且 `xact_start` 很旧，则为僵尸事务，手动终止：

```sql
SELECT pg_terminate_backend(<blocking_pid>);
```

（已配置 `idle_in_transaction_session_timeout=60000` 后 60 秒会自动清理，此步一般只用于确认。）

### 2. 确认表膨胀程度

```sql
SELECT relname, n_live_tup, n_dead_tup,
       round(n_dead_tup * 100.0 / greatest(n_live_tup + n_dead_tup, 1), 1) AS dead_pct,
       last_autovacuum, last_vacuum
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC LIMIT 10;
```

### 3. 手动清理（低峰期执行）

```sql
-- 不锁表，可随时执行
VACUUM (VERBOSE) users;
VACUUM channels;
VACUUM tokens;
```

若 `dead_pct` 超过 50% 且普通 VACUUM 后查询仍慢，说明文件已膨胀，需在**最低谷**执行
（`VACUUM FULL` 会锁表几十秒到几分钟，期间所有请求报错）：

```sql
VACUUM FULL users;
REINDEX TABLE users;
```

或使用 pg_repack 在线重建（不锁表，需安装扩展）。

### 4. 定位热点用户（行锁集中在单一用户时）

日志中 `CONTEXT: while updating tuple (X,Y) in relation "users"` 里的 `(X,Y)` 是物理行号：

```sql
SELECT id, username, request_count, used_quota FROM users WHERE ctid = '(X,Y)';
```

找到后可在管理后台「运营设置 → 模型请求速率限制」对其限流。

---

## 二、日常巡检 SQL

```sql
-- 1. 连接数（应远小于 max_connections=500）
SELECT count(*) AS total,
       count(*) FILTER (WHERE state = 'active') AS active,
       count(*) FILTER (WHERE state = 'idle in transaction') AS idle_in_tx
FROM pg_stat_activity;

-- 2. 锁等待（正常应为 0 行）
SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock';

-- 3. 死元组比例（users/channels/tokens 的 dead_pct 应长期低于 10%）
SELECT relname, n_dead_tup, last_autovacuum FROM pg_stat_user_tables
WHERE relname IN ('users', 'channels', 'tokens', 'quota_transactions');

-- 4. 长事务（超过 1 分钟的事务需要关注）
SELECT pid, now() - xact_start AS duration, state, query
FROM pg_stat_activity
WHERE xact_start IS NOT NULL AND now() - xact_start > interval '1 minute'
ORDER BY duration DESC;
```

Postgres 日志中以下三类记录应长期为零，出现即需排查：

- `canceling statement due to user request`（语句执行过慢被应用放弃）
- `canceling authentication due to timeout`（连接风暴或资源耗尽）
- `skipping vacuum ... lock not available`（vacuum 被锁挤出）

宿主机层面：

```bash
dmesg -T | grep -i conntrack        # 出现 "table full" 需调大 nf_conntrack_max
docker stats --no-stream           # postgres 容器 CPU/内存
```

---

## 三、生产配置清单（8 实例部署）

### PostgreSQL 启动参数（见 docker-compose.yml）

| 参数 | 值 | 作用 |
|---|---|---|
| `max_connections` | 500 | 须大于所有实例 `SQL_MAX_OPEN_CONNS` 之和 |
| `shared_buffers` | 2GB | 数据库所在机器内存的 25% |
| `max_wal_size` | 4GB | 降低 checkpoint 频率 |
| `synchronous_commit` | off | 缩短高频小事务提交持锁时间；崩溃最多丢尾部数百毫秒已提交事务 |
| `idle_in_transaction_session_timeout` | 60000 | 自动断开事务中空闲 60 秒的会话，防僵尸事务持锁 |
| `tcp_keepalives_idle/interval/count` | 60/10/6 | 几分钟内清理死连接 |

### 每台应用实例环境变量

| 变量 | 值 | 说明 |
|---|---|---|
| `SQL_MAX_OPEN_CONNS` | 40 | 8 台 × 40 = 320 < 500 |
| `SQL_MAX_IDLE_CONNS` | 20 | |
| `SQL_MAX_LIFETIME` | 300 | 降低连接重建频率 |
| `BATCH_UPDATE_ENABLED` | true | **8 台全部**；启动日志须出现 `batch update enabled` |
| `SESSION_SECRET` | 同一随机串 | 8 台必须一致 |
| `NODE_TYPE` | slave | **仅 7 台从节点**设置；master 不设 |
| `SYNC_FREQUENCY` | 60（可调小） | 配置/渠道缓存同步周期 |

### 安全项

- 修改 PostgreSQL 默认密码 `123456`。
- 5432 端口防火墙白名单：仅放行 8 台应用实例的内网 IP，禁止公网访问
  （日志中 `invalid length of startup packet` 即公网扫描器探测的痕迹）。
- 应用与数据库之间走内网链路。

### 表级存储参数

应用启动时由 master 节点自动执行（见 `model/main.go` 的 `applyPostgresHotTableTuning`）：
`users`/`channels`/`tokens` 设置 `fillfactor=70` 与激进 autovacuum。
`fillfactor` 只影响新写入页，存量数据需一次 `VACUUM FULL` 或 pg_repack 才完全生效。

---

## 四、故障特征速查

| Postgres 日志 | 含义 | 处理 |
|---|---|---|
| `canceling statement due to user request` + 紧跟 `Connection reset by peer` | 应用放弃执行过慢的语句并断开连接 | 按第一节排查锁/膨胀 |
| `CONTEXT: while updating/locking tuple ... "users"` | 单一用户行成为锁热点 | 第一节第 4 步定位并限流 |
| `unexpected EOF on client connection with an open transaction` | 事务未结束时连接断开，可能遗留持锁僵尸事务 | 确认 `idle_in_transaction_session_timeout` 生效；排查网络 |
| `skipping vacuum ... lock not available` | autovacuum 被行锁流量挤出 | 手动 VACUUM；确认表级参数已生效 |
| `canceling authentication due to timeout` | 新连接 60 秒内无法完成认证：连接风暴或 CPU 饱和 | 核查连接池上限与实例数；查宿主机负载 |
| `invalid length of startup packet` | 非 Postgres 协议流量（扫描器/TCP 探活） | 检查 5432 是否暴露公网 |
| `PID xxx in cancel request did not match any process` | 应用对已死后端补发取消请求，网络断连的余波 | 无需处理；断连频繁则查网络 |

---

## 五、API Key 使用来源清理与计数代次

`token_usage_sources` 是按 Key 有界、但全平台可能达到较大规模的物化表。日志清理事务不得对该表执行无条件全表 UPDATE。

当前实现使用 `count_generation` 做逻辑清零：

- 日志清理只锁定并更新 `log_stat_rollup_states` 和单行 `token_usage_source_count_progresses` 状态；
- 清理与已开始的回填相交时递增代次，API 立即隐藏旧代次计数；
- 后台回填保留日志时，按现有每 Key 批次逐行重建当前代次计数；
- 不删除新增列、不手工回退代次、不对来源表执行人工全表清零。

发布前必须把以下设置全部关闭，并在所有旧实例退出后再启用：

```text
token_usage_source_setting.enabled=false
token_usage_source_setting.reconcile_enabled=false
token_usage_source_setting.backfill_enabled=false
```

日常巡检：

```sql
SELECT name, coverage_start, watermark, backfill_cursor,
       count_generation, updated_at
FROM log_stat_rollup_states
WHERE name = 'token_usage_source_v1';

SELECT direction, range_start, range_end, merge_started,
       count_generation, updated_at
FROM token_usage_source_count_progresses
WHERE id = 1;

SELECT count_generation, count(*) AS rows
FROM token_usage_sources
GROUP BY count_generation
ORDER BY count_generation DESC;

SELECT relname, n_live_tup, n_dead_tup, last_autovacuum
FROM pg_stat_user_tables
WHERE relname IN (
  'token_usage_sources',
  'token_usage_source_meta',
  'token_usage_source_count_progresses',
  'log_stat_rollup_states'
);
```

正常特征：

- `watermark` 持续前进；
- backfill 开启时 `backfill_cursor` 向 `coverage_start` 回退；
- `token_usage_source_count_progresses.direction` 只在单个处理区间内短暂非空；
- 代次切换后旧代次行可以存在，但接口不展示其旧计数，当前代次行随回填逐步增加；
- 日志清理期间没有 `token_usage_sources` 全表 UPDATE、持续锁等待或异常 WAL 峰值。

若 progress 长时间不清空或游标停止：

1. 先关闭使用来源三项开关，保留现场；
2. 检查系统任务状态、PostgreSQL 锁等待和应用错误日志；
3. 不直接删除 progress 行，不手工修改 watermark/backfill cursor/count generation；
4. 恢复原因后由同版本实例重试幂等任务。
