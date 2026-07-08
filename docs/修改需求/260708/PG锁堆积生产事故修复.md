# PostgreSQL 锁堆积 / idle-in-transaction 生产事故：根因与修复

> 日期：2026-07-08
> 分支：`merge/v1.0.0-rc.19`
> 触发现象：`pg_stat_activity` 出现大量
> `SELECT * FROM users WHERE id = $1 ... FOR UPDATE` 处于 `idle in transaction`
> 长达数小时，后续请求在行锁上无限排队（`Lock: tuple` / `Lock: transactionid`），
> 最终连接池打满、数据库无响应、全站不可用。

---

## 1. 根因（两个叠加事故）

### 事故 A：事务持 `users` 行锁后长时间不提交（idle in transaction）

日志特征：
```
state = idle in transaction, wait_event = ClientRead
query = SELECT * FROM "users" WHERE id=$1 ... FOR UPDATE
running_time 最长 03:19:22（3 小时以上）
```

`idle in transaction` + `ClientRead` 的含义是：**应用已经 `BEGIN` 并执行了
`SELECT ... FOR UPDATE` 拿到用户行锁，但迟迟不发 `COMMIT`**（事务中间卡在慢操作
或连接泄漏）。一个用户行被锁住数小时，同一用户的所有后续扣费/充值/订阅请求
在行锁上排队（表现为 `active` + `Lock: tuple` / `Lock: transactionid`），
连接被逐个占满。

锁来源（PG 上会发出 `users ... FOR UPDATE` 的路径）：
- `model/quota_transaction.go` 悲观锁回退 `lockUserForQuotaTx`；
- `model/subscription.go` 订阅购买/消费/重置对 user 行加锁；
- `model/user.go` `inviteUser` 邀请奖励；
- `model/topup.go` 充值结算（锁订单行）。

**关键缺陷：只有钱包额度事务通过 `applyQuotaTxLockTimeout` 设了 `lock_timeout`，
其余锁 `users` 的路径没有任何超时保护；且数据库没有
`idle_in_transaction_session_timeout` 兜底**（否则泄漏事务 60s 就会被强杀）。
于是一旦某个事务因慢操作/泄漏挂住，锁就一直不释放。

### 事故 B：重启时 AutoMigrate 的 DDL 锁死整表

日志最近 ~28 分钟那批 `active` + `Lock: relation`，源头是：
```
ALTER TABLE "users" ALTER COLUMN "access_token" TYPE char(32)   -- ACCESS EXCLUSIVE 锁
ALTER TABLE users SET (autovacuum_...)
VACUUM (ANALYZE) users
```
这是**应用启动时 GORM AutoMigrate** 发出的（`char(32)` 类型 GORM 每次启动都会
误判不一致而重发 ALTER）。`ALTER TABLE ... ALTER COLUMN` 需要 ACCESS EXCLUSIVE 锁，
它排在事故 A 的泄漏事务后面拿不到锁；而**一个"正在排队的 ACCESS EXCLUSIVE 请求"
会阻塞其后所有对该表的查询（连普通 SELECT 都卡）** → 全表瘫痪。

### 放大器：连接池默认值过大

`model/main.go` 中 `SQL_MAX_OPEN_CONNS` 默认 **1000**，而 PG 典型
`max_connections` 只有 100~500。未按 `docker-compose.yml`（已设 40）显式配置时，
单实例就能把 PG 连接打爆；叠加行锁争用直接压垮数据库。

---

## 2. 本次修复（代码，均已 `go build` + `go test ./model ./service` 通过）

### 修复 1（根治）：PostgreSQL 会话级超时兜底
`model/main.go` 新增 `applyPostgresSessionGuards()`，在迁移后（master 节点、PG）
用 `ALTER ROLE CURRENT_USER SET` 写入三个角色级默认值，对之后建立的所有连接
（含其他节点）生效：

| 参数 | 默认 | 环境变量 | 作用 |
|---|---|---|---|
| `idle_in_transaction_session_timeout` | 60s | `SQL_PG_IDLE_IN_TX_TIMEOUT_MS` | **事务空闲超时即由 DB 强制回滚**，即使应用泄漏也不会持锁超过 60s（事故 A 的直接兜底） |
| `lock_timeout` | 5s | `SQL_PG_LOCK_TIMEOUT_MS` | 等锁超时即报错返回，避免行锁/DDL 无限排队；让 AutoMigrate 的 ALTER **快速失败**而非阻塞全表（事故 B 的兜底） |
| `statement_timeout` | 0（关闭） | `SQL_PG_STATEMENT_TIMEOUT_MS` | 单语句上限，默认关闭以免误杀长的管理/迁移查询；需要时可开启兜底病态全表 COUNT |

- 幂等、失败仅记日志不阻塞启动；置 0 关闭对应项。
- 角色级设置持久化，重启后仍生效，且一个 master 应用后所有节点连接都继承。
- 现有连接在 `SQL_MAX_LIFETIME`（默认 60s）内自动回收后应用新设置。

> 这条是本次事故的**根治手段**：即便未来又出现某个事务泄漏/慢操作，
> 数据库也会在 60s 内自动回滚它并释放锁，不会再累积到数小时、拖垮全站。

### 修复 2：连接池护栏告警
`model/main.go`：PG 且 `SQL_MAX_OPEN_CONNS > 200` 时启动打印 WARNING，
提示 `实例数 × SQL_MAX_OPEN_CONNS` 必须小于 PG `max_connections`，建议单实例 20~50。

### 修复 3：修正订阅重置中失效的行锁（正确性）
`model/subscription.go` `adminResetUserSubscriptionsByPlanTx` /
`adminResetPlanSubscriptionsTx` 原用 `tx.Set("gorm:query_option", "FOR UPDATE")`——
这在 GORM v2 下是**空操作（根本没加锁）**，并发重置会竞态。已改为项目统一的
`lockForUpdate(tx)`（真实 `SELECT ... FOR UPDATE`，SQLite 自动跳过）。

### 修复 4：去掉只读列表的多余事务
`GetAllUsers` / `GetUserTopUps` / `GetAllTopUps` 原本把 `COUNT + SELECT` 包在
`DB.Begin()/Commit()` 里，没有任何隔离性收益，却会跨两条查询长时间占用一条连接，
加剧连接堆积与 `idle in transaction` 暴露面。改为直接用连接池查询。

---

## 3. 生产环境必须同步的配置（强烈建议）

代码兜底之外，数据库/部署侧也要对齐，双保险：

### 3.1 PostgreSQL 服务端（即使不改代码也应设置）
```conf
# postgresql.conf 或启动参数
idle_in_transaction_session_timeout = 60000   # 60s，与代码兜底一致
lock_timeout = 5000                            # 5s
# statement_timeout 视业务，OLTP 可设 120000，注意别误杀迁移
```
`docker-compose.yml` 的 postgres 已有部分调优参数，建议把上面两项也加入
`command: -c ...`，这样即使应用侧 ALTER ROLE 未执行也有服务端兜底。

### 3.2 应用连接数（关键）
```bash
SQL_MAX_OPEN_CONNS=40     # 单实例；确保 实例数×该值 < PG max_connections
SQL_MAX_IDLE_CONNS=20
SQL_MAX_LIFETIME=300
```
PG 侧 `max_connections` 要 > 所有实例 `SQL_MAX_OPEN_CONNS` 之和。

### 3.3 重启/迁移策略（避免事故 B 复发）
- **迁移只在单一 master 节点执行**（代码已按 `IsMasterNode` 门控）；
- 生产**重启尽量避开高峰**：AutoMigrate 的 `ALTER TABLE users` 需要表级独占锁，
  高负载下即使有 `lock_timeout` 也会反复失败重试；
- 有条件时把 `access_token` 这类会反复 ALTER 的列，在维护窗口一次性对齐类型，
  避免每次启动都触发 DDL。

---

## 4. 事故发生时的应急处置（运维手册）

若再次出现锁堆积，按序处理（不改代码即可止血）：

```sql
-- 1) 找出持锁的 idle in transaction 元凶（按空闲时长排序）
SELECT pid, state, wait_event, now()-xact_start AS xact_age,
       now()-state_change AS idle_age, left(query,120)
FROM pg_stat_activity
WHERE state = 'idle in transaction'
ORDER BY xact_start;

-- 2) 强杀空闲事务超过 2 分钟的连接（释放行锁）
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE state = 'idle in transaction'
  AND now() - state_change > interval '2 minutes';

-- 3) 立刻给角色装上兜底（等价于代码修复 1，可先手动执行）
ALTER ROLE CURRENT_USER SET idle_in_transaction_session_timeout = '60s';
ALTER ROLE CURRENT_USER SET lock_timeout = '5s';

-- 4) 若是启动 DDL 卡住：查阻塞链
SELECT blocked.pid AS blocked_pid, blocking.pid AS blocking_pid,
       left(blocked.query,80) AS blocked_q, left(blocking.query,80) AS blocking_q
FROM pg_stat_activity blocked
JOIN pg_locks bl ON bl.pid = blocked.pid AND NOT bl.granted
JOIN pg_locks gl ON gl.locktype = bl.locktype AND gl.granted
JOIN pg_stat_activity blocking ON blocking.pid = gl.pid
WHERE blocked.pid <> blocking.pid;
```

---

## 5. 验证与后续

- 已验证：`go build ./...`、`go test ./model ./service` 全部通过。
- 上线后观察：`pg_stat_activity` 中 `idle in transaction` 不应再出现分钟级以上、
  更不应有小时级；`ALTER ROLE` 生效可用
  `SELECT rolname, rolconfig FROM pg_roles WHERE rolname = current_user;` 确认。
- 仍待跟进（本次未改，见《后端性能与额度安全全局分析.md》）：
  - 事务内是否存在慢操作/HTTP 调用导致 idle（需在压测中定位具体路径，
    会话超时已能兜底，但根除需消除慢操作）；
  - 日志表无界 COUNT（B1/B2）等其他 SQL 性能项；
  - `access_token` 列 AutoMigrate 反复 ALTER 的彻底消除。

---

## 附：改动文件清单
- `model/main.go`：新增 `applyPostgresSessionGuards()`；连接数护栏告警。
- `model/subscription.go`：两处失效 `FOR UPDATE` → `lockForUpdate(tx)`。
- `model/user.go`：`GetAllUsers` 去事务。
- `model/topup.go`：`GetUserTopUps` / `GetAllTopUps` 去事务。
