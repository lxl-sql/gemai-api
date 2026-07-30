# API Key 默认容量与生产上线修复

日期：2026-07-29

## 问题

- 新建 API Key 在未提交安全策略时会隐式写入 5 RPS、25 突发、20 并发、五分钟 20 个模型和自动停用策略。该固定值对普通用户过宽，对企业 Key 又过低。
- 未保存管理员策略时，用户页面将新 Key 的最终生效值展示为“管理员策略/平台默认”，容易让管理员误判平台策略已经保存。
- 上线文档先配置平台策略、后配置企业覆盖；平台策略保存后会立即完整替换命中 Key 的容量字段，可能造成存量高流量 Key 全站限流。
- 使用日志 RPM 查询包含当前秒和此前 60 秒，整数时间戳边界可能覆盖 61 个秒值。
- 安全策略为每请求增加 Redis 读取和原子操作；在没有生产压测证据前，不合并不同语义的 Lua 脚本。

## 修改范围

- 省略 `security_policy` 创建 Key 时不再隐式创建安全策略行，保持旧接口兼容。
- 后端与 `web/default` 的新 Key 默认策略统一为零容量限制、`observe` 风险响应和 fail-open；容量由持久化的平台、分组或用户管理员策略负责。
- 普通用户默认值不再硬编码为统一容量。建议管理员在企业覆盖完成后，按生产数据配置普通平台策略。
- 用户页面在内置兼容策略生效时显示容量不受限；命中持久化管理员策略时展示 `admin_profile` 的真实容量，不再使用 `effective_policy` 冒充管理员配置。
- RPM 查询公共窗口函数固定返回 `[now-60, now)`，只统计 60 个完整秒。
- 更新生产上线顺序：迁移 → 企业分组/用户策略 → 验证 → 平台策略 → 监控。

## 明确不修改

- 不改变存量 Key 已保存的单 Key 策略。
- 不改变管理员策略 `user > group > platform > built-in` 的优先级和完整 Profile 语义。
- 不改变额度预留、结算、429 日志和退款链路。
- 不在本次合并 RPS、并发、模型风险和额度 Lua 脚本；先使用生产压测确定瓶颈，避免扩大安全边界。
- 不盲目提高 Redis 连接池；上线时按每实例命令延迟、连接池等待和峰值请求量调整。

## 生产配置建议

普通用户平均约 5 RPM 时，可从以下平台策略开始灰度：

- 持续速率：1 RPS；
- 突发容量：5～10；
- 最大并发：5～10；
- 五分钟不同模型数：10；
- 最低风险响应：`notify`；
- Redis 异常时暂停：Redis 高可用验收前保持关闭。

以上是起点而非固定生产值。企业策略必须依据单 Key 的一秒峰值 RPS、p95/p99 并发、请求耗时和五分钟模型数单独配置。

## 验收要求

- 省略安全策略创建 Key 后不存在隐式策略行，且最终容量不受限。
- `web/default` 新建 Key 默认值与后端一致。
- 内置兼容策略与持久化管理员策略在页面上可明确区分。
- RPM 窗口长度严格为 60 秒。
- Go、Vitest、TypeScript、lint、格式、i18n 同步和前端生产构建全部通过。
- 上线前仍需在真实 Redis 和生产近似流量下验证每实例 Redis 延迟、连接池等待、数据库回源和 429 分布。

## 验收证据

- `go test ./model -run 'TestDefaultTokenSecurityPolicyDoesNotImplicitlyLimitNewTokens|TestInsertWithNilSecurityPolicyDoesNotCreateImplicitLimits|TestRecentLogRateWindowUsesSixtyCompletedSeconds|TestTokenSecurityPolicyUsesAdministrator|TestTokenSecurityPolicyBuiltIn' -count=1`
  - 通过。覆盖默认容量兼容、空策略不落行、60 秒 RPM 窗口和管理员策略合并。
- `go test ./model ./service ./controller -count=1`
  - 通过。覆盖模型、计费安全链和令牌接口的完整包级回归。
- `bun run test -- src/lib/token-security-policy.test.ts src/features/keys/components/api-key-security-policy-summary.test.tsx src/features/keys/lib/api-key-form.test.ts`
  - 通过，3 个测试文件共 8 项断言场景。
- `bun run typecheck`、涉及文件的 `oxlint` 与 `oxfmt --check`
  - 通过。
- `bun run i18n:sync`
  - 通过，`en`、`zh`、`zh-TW`、`fr`、`ja`、`ru`、`vi` 的 `missingCount` 均为 0。
- `bun run build`
  - 通过，Rsbuild 生产构建成功。

## 尚未验证

- 未重启当前本地服务验证新建 Key 页面和后端实际响应。
- 未在生产近似拓扑执行 Redis 延迟、连接池等待、多实例缓存一致性和峰值流量压测。
- 普通平台策略的建议值尚未写入任何数据库；必须先配置并验证企业分组/用户策略，再由管理员保存平台策略。
