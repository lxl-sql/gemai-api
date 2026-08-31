# OAuth 万人授权安全修复补丁说明

日期：2026-08-27
基线：`merge/v1.0.0-rc.19` / `e0131d649`

本文记录本次实际代码补丁及推荐合并顺序。各补丁均沿直接 OAuth 链拆分，未修改兑换码、模型请求限流或其他业务模块。

## 补丁 1：授权代次与事务

- `oauth_grants.authorization_version` 为加法字段，旧数据迁移为 `0`。
- 新授权、撤销、重新授权均推进授权代次；JWT 携带 `grant_version`。
- 旧 JWT 未携带该字段时由 `OAUTH_ACCEPT_LEGACY_GRANT_TOKENS` 控制兼容；重新授权采用“旧行临时退休 → 插入新 Grant ID → 删除旧行”的同事务换代，避免 SQLite 主键复用，也让旧版本节点无法把旧 Grant ID 重新识别为有效授权。
- 授权码 CAS 消费、用户启用检查、Grant Upsert、Refresh Token Hash 和 Access Token 签名在一个主库事务中完成。
- 移除 Token 路径中的完整 `DB.Save(grant)`，避免覆盖并发撤销或轮换。

## 补丁 2：授权码与 Refresh Family 维护

- 删除每次 Token 成功后启动的授权码清理 Goroutine。
- 新增五分钟一次、数据库租约去重的系统任务，每轮分批删除过期授权码。
- 新增 `oauth_refresh_token_histories`，保存每一代被轮换的 Refresh Token Hash、授权代次和过期时间。
- 任意历史 Refresh Token 在宽限期后重放时，只撤销其所属的同一授权代次，不会误伤后续重新授权。

## 补丁 3：OAuth 有界队列与自适应并发

- 只精确豁免：
  - `POST /api/oauth-server/token`
  - `GET /api/oauth-server/userinfo`
- `GET /api/oauth-server/token-exchanges/:id` 保留全局API限流，防止随机Exchange ID放大Redis读取。
- Token、Refresh与UserInfo分别按 `操作 + App ID + User ID` 隔离单用户重复请求；三个操作互不挤占，不同用户、不同App及共享出口IP也相互隔离。
- OAuth路径不再使用固定App/IP QPS桶。每实例有界验证槽限制节点内等待任务，数据库校验与写事务共同受Redis全局Permit约束，避免多实例放大PostgreSQL并发。
- 支持 `Prefer: respond-async, wait=55`；55秒内完成仍返回标准Token响应，慢任务返回 `202 + status_url + 短期poll_token`。
- Redis有界队列按 `exchange_id` 分区，支持Standalone、Sentinel、Redis Cluster和多个独立Redis分片；全局协调Redis统一控制所有gemai-api实例的数据库并发。
- 多个独立Redis分片采用固定顺序映射；所有API节点必须使用完全一致的URL顺序，增删或重排分片前必须先停止接收并排空队列。Redis Cluster扩缩容不使用该独立分片映射。
- 仅高级独立拓扑使用 `OAUTH_QUEUE_REDIS_URLS`、`OAUTH_QUEUE_REDIS_CLUSTER_ADDRS`、`OAUTH_QUEUE_REDIS_SENTINEL_*` 或 `OAUTH_QUEUE_COORDINATOR_REDIS_URL`；普通部署全部省略。
- PostgreSQL现有授权码CAS继续作为最终幂等事实，不新增OAuth任务表；Redis彻底丢失时允许用户重新授权。
- 自适应并发只使用负反馈：数据库P95、连接池等待或错误升高时降低全局并发，稳定后缓慢增加。
- OAuth队列默认关闭；只有设置 `OAUTH_QUEUE_ENABLE=true` 才会启用，且显式启用时必须有可用Redis。不会切换到可随实例数放大的空内存窗口。
- 队列满会在授权码消费前返回结构化429。任务一旦可能已入队，后续Redis响应不确定时仍返回确定性的 `exchange_id + poll_token`，避免错误声明“状态未改变”。
- 429 包含 `Retry-After`、Bucket、Limit、Burst、Remaining 和 Request ID。

启用时默认复用现有 `REDIS_CONN_STRING`；`.env.oauth-rate-limit.example` 列出显式启用开关和独立Redis拓扑等可选项。

## 补丁 4：缓存与凭证验证

- 只缓存授权页所需的 OAuth App 公共元数据，不缓存 Client Secret Hash。
- Token、Refresh、UserInfo和 Delegated API 的 App状态继续直接读取主库，确保禁用、删除和Secret重置立即生效。
- Client Secret成功验证使用短期 HMAC Key缓存；每次先从主库读取当前Hash，再使用对应版本缓存，因此旧Secret不能借陈旧缓存继续认证。
- 同实例并发验证通过 singleflight 合并，避免批量授权开始时重复执行 bcrypt。
- Grant、用户状态和 Refresh Token有效性继续由PostgreSQL权威判断，不做普通缓存。

## 补丁 5：协议、客户端和日志

- Delegated JWT统一验证 HS256、issuer、audience、expiration、jti、token type和授权代次。
- 新建 App 显式区分 `confidential` 与 `public`；存量 App 迁移为 `legacy`。Confidential 使用PKCE时仍必须验证Client Secret，Public 强制严格PKCE；legacy 保留原有“PKCE 或 Secret”兼容行为。
- Token/UserInfo响应统一 `Cache-Control: no-store`。
- Classic回调删除对429、超时、断线和5xx一视同仁的授权码自动重放。
- 删除自定义OAuth调试日志中的授权码前缀、Token响应体和UserInfo响应体。
- OAuth查询参数在Gin访问日志中显示为 `?<redacted>`。

## 合并与发布顺序

1. 按文件边界合并模型/事务、限流、缓存与协议客户端补丁，并先在预发布运行完整验证。
2. 在升级任何 Slave 前，用新版本源码执行 `go run ./cmd/schema-migrate`；Docker 镜像可用 `--entrypoint /schema-migrate`，Release 包也包含对应平台的 `schema-migrate` 可执行文件。该命令只迁移 `oauth_apps`、`oauth_authorization_codes`、`oauth_grants` 与 `oauth_refresh_token_histories`，不会运行全库 AutoMigrate 或其他数据迁移；迁移使用单数据库连接，并以 PostgreSQL `SQL_PG_LOCK_TIMEOUT_MS`（默认 5000ms）或 MySQL `SQL_MYSQL_LOCK_WAIT_TIMEOUT_SECONDS`（默认 5s）同时限制 metadata lock 与 InnoDB 行锁等待。确认 `oauth_apps.client_type`、`oauth_grants.authorization_version`、`oauth_refresh_token_histories` 与 `idx_oauth_authorization_codes_expires_at` 已存在后再继续。
   PostgreSQL 迁移 DSN 必须直连主库，不能经过 transaction-pooling PgBouncer，否则会话级 `lock_timeout` 不能保证覆盖后续 DDL。
3. `OAUTH_ACCEPT_LEGACY_GRANT_TOKENS` 未配置时默认兼容旧Token；所有节点必须使用一致的`CRYPTO_SECRET`（或稳定的`SESSION_SECRET`）。OAuth队列初次灰度保持关闭；完成Redis与数据库容量验证后，才在所有同版本节点设置 `OAUTH_QUEUE_ENABLE=true`，普通拓扑复用现有Redis，独立拓扑再配置OAuth队列Redis参数。
4. 发布窗口暂停 OAuth 授权/撤销/刷新入口或将这些入口整体导向同一版本；不能让新旧节点同时处理授权状态写入。随后排空节点，Slave 逐台升级，Master 最后。
5. 全部节点同版本后恢复 OAuth 流量。至少等待旧 Access Token TTL（一小时）后设置 `OAUTH_ACCEPT_LEGACY_GRANT_TOKENS=false`，再逐台滚动该配置。
6. 新建/切换 `confidential`、`public` 类型只能在所有节点升级完成后开放；旧节点不理解该字段。

## 生产门槛

- 用实测数据库延迟核验代码内置的有界容量和自适应并发默认值；若确需改变，应通过独立代码评审修改默认值，避免线上环境参数漂移。
- 核对真实 `TRUSTED_PROXIES`、Redis拓扑、PgBouncer池和每实例SQL连接上限。
- 不把工具服务器自行填写的 `X-Forwarded-For` 当作用户身份；OAuth业务保护不依赖最终用户IP，共享出口不会共享业务配额。
- 工具端只需在原Token请求增加 `Prefer`，并在收到202时按响应中的 `status_url` 与 `Retry-After`轮询，无需实现另一套OAuth接口。
- 超时、断线、无响应或普通5xx不得自动重放一次性授权码。
- Access Token一小时后的集中Refresh仍需由工具端加入随机抖动和单Grant并发合并。
- 收到202后只按同一 `exchange_id` 查询；若队列结果彻底丢失或返回 `exchange_not_found`，再让用户重新授权。
