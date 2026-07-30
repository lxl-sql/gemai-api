# 账号与 API 令牌泄露防护生产改造

日期：2026-07-28

## 背景与目标

本次改造针对两类已确认风险：

1. 已有 API Key 被第三方程序直接调用；
2. 账号密码或浏览器会话失控后，攻击者登录、查看或创建 API Key。

目标是在不使用全平台统一低 RPM、不过度影响约 5000 RPM 企业流量的前提下，完成账号会话撤销、密钥不可逆存储、一次性交付、令牌级容量控制、消费预算、模型遍历检测、异常通知和可信代理边界。

## 实施范围

### 账号与会话

- 登录会话不再信任 Cookie 中缓存的角色、状态和分组；每次认证读取当前用户安全状态。
- 用户增加 `security_version`。修改或重置密码、管理员禁用账号、执行“退出所有设备”都会递增版本，使旧会话立即失效。
- 修改或重置密码会在同一数据库事务内：
  - 更新密码哈希；
  - 轮换账号 Access Token；
  - 撤销全部 OAuth 委托授权和刷新令牌。
- 新增 `POST /api/user/self/logout-all`，同时撤销浏览器会话、账号 Access Token 和 OAuth 委托授权。
- 账号 Access Token 改为 `POST /api/user/token`，并要求安全二次验证。

### 安全二次验证

- 敏感令牌操作要求五分钟有效的二次验证。
- 支持当前密码、2FA 和 Passkey；没有启用 2FA 的普通密码用户仍可完成验证。
- 验证记录绑定 `security_version`，密码变更或全会话撤销后旧验证结果不能复用。
- OAuth 委托接口不再允许创建或读取完整 API Key。

### API Key 存储与生命周期

- 新增 `key_hash` 和 `key_hint`：
  - `key_hash` 使用平台密钥执行 HMAC-SHA256；
  - `key_hint` 仅保存脱敏展示值；
  - 原 `key` 列改存不可逆指纹标记，避免明文落库。
- 启动迁移会分批、事务化转换所有历史令牌。原 Key 仍可认证，但服务端不再能够还原完整值。
- 完整 Key 仅在创建或轮换成功响应中显示一次。
- 删除单个和批量完整 Key 查看接口。
- 新增 `POST /api/token/:id/rotate`。轮换保留令牌 ID、使用记录与策略，旧凭证同步失效。
- 缓存撤销改为同步并带三次短重试；若数据库已完成轮换但缓存删除仍异常，响应仍交付新 Key，并明确提示旧 Key 可能在缓存 TTL 内短暂有效，后台日志和操作日志会记录异常。

### 令牌级策略

新增 `token_security_policies` 表和以下接口：

- `GET /api/token/:id/security-policy`
- `PUT /api/token/:id/security-policy`
- `DELETE /api/token/:id/security-policy`

支持：

- 持续 RPS 与突发容量（Redis Lua 原子令牌桶）；
- 最大并发；
- 单请求最大额度；
- 小时和每日额度预算；
- 五分钟内不同模型数量；
- `observe`、`notify`、`suspend` 风险响应；
- Redis 不可用时按令牌选择 fail-open 或 fail-closed。

新建令牌的默认策略为：

| 项目 | 默认值 |
|---|---:|
| 持续 RPS | 5 |
| 突发容量 | 25 |
| 最大并发 | 20 |
| 五分钟不同模型数 | 20 |
| 风险响应 | 自动暂停单个令牌 |
| Redis 故障 | fail-open |

前端同时将新令牌默认值调整为有限额度和 30 天有效期。用户可以在高级设置中调整容量或关闭固定限制。

企业令牌示例（约 5000 RPM）：

```text
sustained_rps = 100
burst_capacity = 500
max_concurrency = 根据 P95 上游延迟和在途请求量配置
max_distinct_models_5m = 按业务模型集合配置，聚合平台可设为 0
risk_mode = notify
fail_closed = 仅在 Redis 高可用时启用
```

高 RPM 本身不会被判定为盗用。风险检测重点是新环境下的模型遍历、消费突增和异常组合；本次实现先落地最强特征“短时间不同模型数”，并为后续动态基线保留独立策略边界。

### 预算与计费一致性

- 令牌小时/每日预算在请求发往上游前原子预留，避免并发穿透。
- 成功结算按实际额度修正预留值，退款按零消费释放。
- 持久化计费结算失败时保留保守预留，避免故障期间继续突破安全预算。
- 预算计数按 `token_id` 隔离，不会用用户级阈值误伤同一企业下的其他令牌。

### 风险审计与通知

- 超过五分钟不同模型阈值时写入 `token.risk_detected` 操作审计。
- 五分钟内同一事件只触发一次，避免通知风暴。
- `notify` 和 `suspend` 使用用户现有 Email、Webhook、Bark 或 Gotify 通知配置发送安全告警。
- `suspend` 仅暂停命中的 API Key，不冻结账号和其他令牌。

### 客户端 IP 可信边界

- 新增 `TRUSTED_PROXIES` 环境变量，值为逗号分隔的代理 IP 或 CIDR。
- 未配置时完全禁用转发头信任，`ClientIP()` 使用直连对端地址。
- 配置错误时拒绝启动，避免在错误可信链上运行 IP 白名单和审计。

生产入口还必须：

1. 防火墙禁止绕过反向代理直连 API；
2. 入口代理覆盖客户端提交的 `X-Forwarded-For`，不要直接透传；
3. `TRUSTED_PROXIES` 只填写真实 Nginx、负载均衡或 CDN 出口网段。

## 前端行为变化

- Key 列表仅显示脱敏提示，不再按悬停、点击或批量操作获取明文。
- 创建和轮换后显示一次性密钥对话框，关闭后无法再次查看。
- 原“复制 Key、复制连接信息、CC Switch、带 Key 打开外部聊天”入口移除；需要外部客户端时应创建或轮换专用 Key 并立即保存。
- 仪表盘命令只复制脱敏模板，明确提示用户替换 Key。
- 账号安全页新增“退出所有设备”。
- 新增密码二次验证界面，全部新增文案已同步到 `en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi`。
- `web/classic` 复用既有安全验证组件补充密码验证，令牌新增、编辑、启停、删除、批量删除、轮换和账号 Access Token 轮换均进入同一二次验证链路；列表不再尝试回显或批量读取完整 Key。

## 数据迁移与部署

### 部署前

1. 备份主数据库，重点保留 `tokens`、`users`、`oauth_grants`。
2. 确认所有实例使用相同且稳定的 `SESSION_SECRET`/加密密钥；HMAC 指纹依赖该密钥。
   - 非 `GIN_MODE=debug` 环境未配置 `SESSION_SECRET` 时，服务会拒绝启动，避免重启后会话与 API Key 指纹全部失效。
3. 确认 Redis 可用性和缓存 TTL。
4. 配置并验证 `TRUSTED_PROXIES`，同时限制源站网络访问。

### 发布顺序

1. 在维护窗口滚动发布后端；首次启动执行表结构迁移和历史 Key 指纹转换。
2. 等待一个旧版本令牌缓存 TTL，或确认所有实例启动时已清理 `token:*` 缓存。
3. 发布 `web/default` 前端。
4. 验证一次创建、认证、轮换、旧 Key 拒绝、退出所有设备和企业高并发策略。

不应在新后端发布后回滚到依赖数据库明文 Key 的旧版本。历史明文已被不可逆清除，旧版本无法通过 `key` 列完成认证。若必须回滚，应回滚数据库备份并按安全事件重新轮换全部 Key。

## 验收记录

已完成：

- `go test ./...`
- `bun run typecheck`
- `web/default`：`bun run build`
- `web/classic`：`bun run build`
- i18n 缺失键扫描：全部 `t(...)` Key 已存在
- `bun run i18n:sync`：所有语言 `missingCount = 0`
- SQLite 旧 `tokens.key char(48)` 表结构迁移回归
- 历史明文 Key 转换后不可从数据库读取，原 Key 仍可认证
- 密码重置同时递增安全版本、轮换账号 Access Token、撤销 OAuth Grant
- 令牌策略所有权、规范化以及 Redis 故障 fail-open/fail-closed 回归

上线后仍需在真实基础设施验证：

- PostgreSQL/MySQL 生产副本上的迁移耗时与锁等待；
- Redis Cluster Lua、故障转移和 5000 RPM 压测；
- Nginx/CDN 真实 `ClientIP()` 与伪造转发头测试；
- Email/Webhook/Bark/Gotify 实际安全通知；
- 多实例轮换后旧 Key 的最大失效时间；
- 浏览器中密码、2FA、Passkey 一次性交付完整流程。

## 边界说明

- 本次没有引入机器学习或全量动态行为基线，避免在缺乏历史标注时误伤企业流量。
- IP 和 User-Agent 仍只作为审计辅助；IP 白名单只有在可信代理和源站隔离正确时才构成安全边界。
- 已存在令牌不会自动套用低 RPS；只有新令牌使用安全默认值，历史令牌可按业务逐个配置。
