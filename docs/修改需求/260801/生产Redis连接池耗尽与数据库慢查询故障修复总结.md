# 生产 Redis 连接池耗尽与数据库慢查询故障修复总结

> 事件时间：2026-07-31 23:13（Asia/Shanghai）  
> 文档日期：2026-08-01  
> 适用拓扑：1 个 master、多个 slave，共用 PostgreSQL 与 Redis  
> 当前状态：已完成日志与代码链路诊断；修复仍在工作区，尚未取得生产灰度、真实 Redis 峰值流量和多实例滚动发布验收结论。

## 一、事件现象

2026-07-31 23:13 左右，应用日志同时出现以下现象：

1. Token 安全策略缓存连续报错：

   ```text
   failed to cache token security policy: failed to execute transaction:
   redis: connection pool timeout
   ```

2. `/api/status`、`/api/notice` 等轻量接口耗时约 12.2 秒，已经影响普通页面加载，不只是中继请求变慢。
3. 多张无直接关联的表同时出现 274～1566ms 的简单查询，包括 `users`、`token_security_profiles`、`billing_reservations`、`tokens` 和 `user_subscriptions`。这更符合连接池排队、数据库整体负载、锁等待、I/O 或网络延迟，而不是单条复杂 SQL。
4. `quota_transactions` 和 `token_security_policies` 出现红色 `record not found`。
5. 带额度边界守卫的 PostgreSQL `UPDATE ... RETURNING` 出现 `rows:0`。

## 二、日志判定

### 2.1 Redis 连接池超时发生在应用侧

`redis: connection pool timeout` 的含义是：go-redis 在 `PoolTimeout` 内无法从当前应用进程的连接池取得连接。它不等同于 Redis 进程宕机，也不直接证明 Redis 拒绝了连接。

事故版本把每个应用实例的 Redis 连接池固定为：

```text
REDIS_POOL_SIZE 未配置时 = 10
```

go-redis v8 默认 `ReadTimeout` 为 3 秒，`PoolTimeout` 为 `ReadTimeout + 1 秒`，即约 4 秒。网关一次请求会执行限流、用户/Token 缓存、安全策略和额度窗口等多次 Redis 操作；连接池耗尽时，多个串行命令可累计多个 4 秒等待。`/api/status` 约 12 秒的耗时与三次池等待高度一致。

旧通用限流实现还会为一次判断串行执行 `LLEN`、`LINDEX`、`LPUSH`、`LTRIM`、`EXPIRE` 中的多条命令，并使用不受请求取消控制的 `context.Background()`。这既增加连接借用次数，也会在 Redis 变慢时把一次旁路限流放大成多次等待和 HTTP 500。

### 2.2 Redis 持久化日志没有显示故障

Redis 日志时间为 UTC。Redis 的 `2026-07-31 15:13` 对应应用所在时区的 `2026-07-31 23:13`，可与事故窗口直接对齐。

事故窗口内观察到：

- 持续达到 `10000 changes in 60 seconds`，约每分钟触发一次 RDB 后台保存；
- 每次 RDB 保存约 60～100ms，均以 `Background saving terminated with success` 结束；
- Fork CoW 仅 1～2MB；
- 15:05 的 AOF 重写成功完成；
- 15:13:03 的 RDB 保存已经结束，应用在约 15:13:14 才报告连接池超时，两者相差约 11 秒。

因此，现有 Redis 服务端日志不支持“BGSAVE/AOF 重写卡死 Redis”这一结论。日志能够证明当时写入频繁和持久化持续触发，但不能替代 `INFO clients/stats/memory/persistence`、`SLOWLOG`、`LATENCY` 以及从应用节点发起的网络延迟检查。

### 2.3 `record not found` 是预期分支

- `quota_transactions` 的查询是额度入账前的幂等键探测。首次请求不存在同键流水属于正常情况，代码会继续执行入账。
- Token 没有独立安全策略记录时，代码会回落到默认策略。数据库不存在该行并不是 Token 损坏。

这两类日志是 GORM 默认错误日志表现，不是本次故障根因。运维排障时应结合后续返回值判断，不能看到红色 `record not found` 就认定事务失败。

### 2.4 `rows:0` 是额度守卫未命中

额度更新 SQL 同时要求：

- 用户存在且未软删除；
- 更新后的 `quota`、`gift_quota` 均在 `0～2147483647`；
- 扣减不会形成负余额，增加不会超过数据库 `int32` 上限。

因此 `rows:0` 表示守卫条件不成立或并发状态已经变化，不代表 PostgreSQL 执行 SQL 失败。代码会继续读取用户快照并返回用户不存在、余额不足或边界错误，事务不会静默部分提交。

事故样本中，签到增加赠送额度 `9,899,661` 时，如果用户存在且未删除，需要重点确认原 `gift_quota` 是否已经大于 `2,137,583,986`。仅凭截取日志不能确认具体原因。

## 三、根因与放大器

### 3.1 已确认

1. 每实例默认 10 个 Redis 连接明显偏小，应用确实发生了本地连接池等待超时。
2. 通用限流一次判断包含多次串行 Redis 往返，Redis 变慢或连接池打满时会累计等待，并把基础设施错误升级成 HTTP 500。
3. Token 安全策略缓存读取失败后会回源 PostgreSQL，再尝试写回 Redis；Redis 持续拥塞时，一次缓存路径可能同时承担读等待、数据库回源和写等待。
4. 多张表的简单 SQL 同时变慢，说明 PostgreSQL 或应用数据库连接池当时存在系统性延迟。
5. `/api/status`、`/api/notice` 等接口在写响应期间持有全局配置读锁；慢客户端会延长持锁时间，等待写锁的配置同步又会阻塞后续读锁，形成进程内排队放大。

### 3.2 尚未确认

1. Redis 主机 CPU、内存、`maxclients`、网络或慢命令是否在事故时异常；当前只有服务端持久化日志，没有 `INFO` 和延迟快照。
2. PostgreSQL 慢查询主要来自连接池排队、锁等待、磁盘 I/O、网络还是连接频繁重建。
3. 各应用实例当时实际的 `REDIS_POOL_SIZE`、`SQL_MAX_OPEN_CONNS`、`SQL_MAX_IDLE_CONNS` 和进程并发量。
4. 各条 `rows:0` 最终属于余额不足、上限保护、用户状态还是跨实例并发变化。

## 四、本次修复内容

### 4.1 Redis 连接池容量

- 不再无条件把连接池固定为 10。
- 未显式配置时按 `10 × GOMAXPROCS` 计算，并设置最小值 50。
- 连接串中已经配置更大 `pool_size` 时不调小。
- 默认预热约四分之一的空闲连接，减少高峰期临时建连。
- 启动日志输出最终 pool size 和 min idle，便于核对各实例配置。
- `.env.example` 补充 `REDIS_POOL_SIZE`、PoolTimeout 和容量说明。

生产环境仍应显式设置连接池，而不是完全依赖 CPU 推导。必须满足：

```text
应用实例数 × REDIS_POOL_SIZE + 运维/监控/其他客户端余量 < Redis maxclients
```

连接池扩大前必须先确认 Redis CPU、内存、文件描述符和 `maxclients` 余量。不能通过单纯增加 `PoolTimeout` 处理池耗尽，否则只会延长用户等待时间。

### 4.2 通用限流链路

- 将一次限流判断合并为单个 Lua 脚本，减少为一次 Redis 往返。
- Redis 限流设置 500ms 请求级超时，不再使用无限延续的后台上下文。
- Redis 不可用或超时时降级到进程内限流，不再把旁路限流错误升级成全站 HTTP 500。
- 降级日志限制为最多每 5 秒一条，避免 Redis 故障时日志风暴继续放大负载。
- 新增 Redis 不可用时的内存降级回归，以及真实 Redis 原子窗口集成用例。

### 4.3 PostgreSQL 热路径减负

- 数据库时钟由“每个计费事务查询一次”调整为周期采样偏移量，降低 `SELECT clock_timestamp()` 在事务内占用连接的次数。
- 活跃订阅存在性增加短 TTL 缓存，并在新增订阅时主动失效，减少每个中继请求对 `user_subscriptions` 的重复 `COUNT(*)`。
- Token 鉴权将 `key_hash = ? OR key = ?` 拆成两个可使用索引的顺序探测，避免未知 Key 触发不稳定的 `OR + ORDER BY` 扫描计划。
- Token 元数据回填移出同步迁移热路径，增加批次节流和进度日志；生产 PostgreSQL 已有数据但缺少在线 DDL 时阻止实例直接启动迁移。
- PostgreSQL 会话级锁超时在迁移前生效，并刷新已有空闲连接，避免 AutoMigrate 长时间排队持锁。
- `.env.example` 调整数据库连接池示例，说明空闲连接过少和生命周期过短会导致高峰期频繁建连。

### 4.4 进程内配置锁

- `/api/status`、`/api/notice`、`/api/about`、首页内容等接口只在复制配置值时持有读锁，响应序列化和网络发送前立即释放。
- 定时配置同步跳过未变化的值，减少大 JSON 重复解析和全局写锁次数。

### 4.5 额度与安全错误边界

- 额度流水在进入数据库更新前校验单桶 delta、总 delta 和更新后余额的 `int32` 边界。
- 超过数据库范围时返回错误并保持余额与流水均未修改，新增边界回归用例。
- Token 安全策略加载失败统一分类为“安全服务暂不可用”，对外返回 503，而不是误报成某个 Key 的配置拒绝。
- 基础设施故障不再同步追加一条“API Key 安全拒绝”错误日志，避免数据库已经异常时继续逐请求写日志放大故障。

## 五、上线前阻塞项

以下问题在解决前，当前修复不能直接按多实例滚动发布完成验收：

### 5.1 限流 Redis Key 存在新旧格式兼容风险

旧实例在 Redis LIST 中写 RFC3339 时间字符串，新实现写 Unix 秒整数；新代码能识别并覆盖旧格式，但旧实例无法解析新格式。滚动发布期间，新旧实例共享同一个限流 Key，旧实例在列表达到阈值后可能因时间解析失败返回 HTTP 500。

发布前必须采用独立的 `v2` Key 前缀，或实现新旧实例都能读取的兼容编码。不能仅依靠“一台 slave 灰度”绕过，因为 Redis 数据由全部实例共享。

### 5.2 活跃订阅缓存仍可能重复等待 Redis

活跃订阅缓存当前复用无请求超时的通用 Redis GET/SET。连接池已经耗尽时，缓存未命中路径可能先等待一次 GET、回源数据库后再等待一次 SET，从而再次累计多个 PoolTimeout。

进入生产前应给这条热路径增加短超时/熔断，或在 Redis 连接池错误时跳过写回；必须补充 Redis pool timeout 下接口耗时上限的回归。

### 5.3 验收证据仍缺失

- 尚未提供事故窗口的 Redis `INFO`、`SLOWLOG`、`LATENCY` 快照；
- 尚未在真实共享 Redis 上执行多实例限流与 Token 安全 Lua 集成验收；
- 尚未验证生产 PostgreSQL 的连接池等待、锁等待和查询 P95/P99；
- 尚未完成 slave 灰度、剩余 slave、master 的完整发布和回滚演练。

## 六、生产检查与验收

### 6.1 Redis 基线

发布前及灰度期间记录：

```redis
INFO clients
INFO stats
INFO memory
INFO persistence
INFO cpu
SLOWLOG LEN
SLOWLOG GET 20
LATENCY LATEST
LATENCY DOCTOR
CONFIG GET maxclients
CONFIG GET save
CONFIG GET appendonly
CONFIG GET auto-aof-rewrite-percentage
CONFIG GET auto-aof-rewrite-min-size
```

重点字段：

- `connected_clients`、`blocked_clients`、`rejected_connections`；
- `instantaneous_ops_per_sec`、`total_error_replies`；
- `used_memory`、`used_memory_peak`、`maxmemory`、`evicted_keys`；
- `rdb_last_bgsave_status`、`rdb_last_bgsave_time_sec`、`latest_fork_usec`；
- `aof_last_bgrewrite_status`。

从每台应用节点所在网络执行持续延迟检测，不能只在 Redis 容器内测回环地址。机房内网应以发布前基线为准；持续超过基线两倍、频繁超过 10ms 或出现 50ms 以上尖峰时停止扩大灰度。

### 6.2 PostgreSQL 基线

检查：

- `pg_stat_activity` 中的活跃连接、连接等待、`wait_event_type` 和长事务；
- `pg_locks` 中未授予锁；
- 应用数据库连接池的打开数、使用中、空闲数、等待次数和等待时长；
- 主键查询、Token 鉴权、订阅存在性查询和计费事务 P95/P99；
- 所有实例 `SQL_MAX_OPEN_CONNS` 总和低于 PostgreSQL `max_connections` 并保留运维余量。

### 6.3 灰度顺序

1. 先完成限流 Key 滚动兼容和订阅缓存短超时修复。
2. 记录发布前 Redis、PostgreSQL、接口 P95/P99、401/403/429/5xx 基线。
3. 将唯一 master 从普通业务流量中摘除，先更新 master；确认在线 DDL 已完成、启动迁移/结构护栏通过、Token 元数据后台回填正常，并观察至少 10 分钟。
4. 如果 master 承载业务流量，先导入约 5% 或一个最小可控流量单元，验证 `/healthz`、登录、普通 API、Token 调用和计费闭环。
5. 更新一台 slave 并观察至少 30 分钟，确认无新增 `connection pool timeout`、限流 500、数据库连接等待和异常拒绝。
6. 逐台或按最多 10%～20% 的小批次更新其余 slave；上一批通过健康检查和观察窗口后才能继续。
7. 确认旧版本实例全部退出，并检查后台任务、水位、Token 元数据回填和缓存同步正常。

停止扩大灰度的条件包括：

- Redis pool timeout、连接失败或命令 P95 持续上升；
- `/api/status`、`/api/notice` 再次出现秒级延迟；
- 5xx、错误 429、Token 安全暂不可用明显高于基线；
- PostgreSQL 连接等待、锁等待或主键查询延迟持续上升；
- Redis `rejected_connections`、`blocked_clients` 或延迟尖峰增加。

## 七、回滚原则

- 回滚应用镜像，不删除数据库新增列或索引。
- 不执行 Redis `FLUSHALL`，不全量清空共享缓存。
- 新版本限流应使用独立版本 Key，并依靠 TTL 自然过期；回滚时旧实例继续使用旧 Key。
- `REDIS_POOL_SIZE` 可以保留为经过容量核算的显式值，回滚前确认旧版本同样读取该环境变量。
- 回滚后继续观察 Redis、PostgreSQL 和接口延迟，确认连接池中的旧请求已经排空。

## 八、结论

本次日志能够确认的是应用侧 Redis 连接池耗尽和全局数据库延迟；不能把故障直接归因于 Redis RDB/AOF 持久化。修复方向应同时覆盖连接池容量、Redis 往返次数、故障降级、数据库热路径和进程内锁，而不是只扩大超时时间。

当前工作区已经包含对应的最小修复，但限流 Key 的滚动兼容和订阅缓存的 Redis 超时仍是上线阻塞项。完成这两项并取得真实共享 Redis、PostgreSQL、多实例灰度证据后，才能把状态更新为“生产修复完成”。
