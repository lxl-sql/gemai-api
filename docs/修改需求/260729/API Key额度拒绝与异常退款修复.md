# API Key 额度拒绝与异常退款修复

日期：2026-07-29

## 问题

- API Key 的单次、小时或每日额度限制拒绝请求时只返回笼统的 429 提示，用户无法判断具体是哪一项配置不足。
- 额度限制拒绝没有写入使用日志，无法按请求追溯令牌、模型、限制类型和预计额度。
- 请求完成预扣费后如果中途发生 panic，常规错误退款分支不会执行，预扣额度只能等待后台修复。
- API Key 编辑操作日志没有记录令牌剩余额度和安全额度策略的变更前后值，无法还原误配置。
- 用户侧安全额度输入直接使用内部 quota 单位，与同一表单上方按 CNY/USD/Token 展示的额度口径不一致。
- 安全策略重置提交成功后再次读取数据库；二次读取失败会导致接口误报失败并漏记操作日志。
- 极小的正数显示额度可能在换算时四舍五入为 0，而安全策略中的 0 表示继承管理员上限或不限额。
- 当前本地服务未配置 Redis，且安全保护异常时暂停请求已关闭，导致依赖 Redis 的小时/每日额度按既定 fail-open 策略放行。
- 小时/每日额度最大值经过显示货币换算后可能因浮点精度回传为超过允许范围 1 个 quota。
- 单次、小时或每日额度在预留阶段拒绝请求时直接返回 429，没有执行当前 API Key 配置的“自动暂停令牌”风险响应。
- 实际结算额度超限时没有区分 `observe`、`notify` 和 `suspend`，三种模式都会禁用 Key。
- 模型风险通知的五分钟去重同时跳过暂停执行，风险模式升级、暂停失败或手动重新启用后可能暂时绕过自动暂停。
- `notify` 只对模型风险发送通知，额度超限没有通知；RPS、并发和安全服务不可用的早期拒绝也没有结构化使用日志。
- 数据库已经禁用 Key 但 Redis 凭据缓存同步失败时，上层无法区分“禁用已提交”和“禁用未执行”。

## 修改范围

- 为 API Key 请求频率、并发、额度、模型风险和安全服务不可用返回稳定错误码。
- 单次、小时、每日额度拒绝分别返回明确提示，展示本次预计额度、已配置上限，并说明请求在计费前被拒绝。
- 额度或模型风险拒绝写入零费用错误日志；日志记录请求、令牌、模型、错误码，并在管理员详情中记录限制类型、预计额度和配置上限。
- 预扣费后的处理链发生 panic 时同步执行幂等退款；退款持久化失败时仍保留原有预留记录供后台修复。
- API Key 更新和安全策略更新/重置的操作日志记录额度及策略的变更前后值，不记录密钥明文。
- 用户侧单次、小时、每日安全额度与普通额度使用同一显示单位；提交时统一转换为内部 quota，编辑回显时反向转换。管理员安全策略页面仍保留明确标注的内部 quota 单位。
- 安全策略重置使用已提交的确定结果记录审计，不再在提交后增加一次可能失败的读取。
- 用户输入的安全额度只要大于 0，提交后至少保留为 1 个内部 quota；只有明确输入 0 才使用继承/不限额语义。
- 当前本地环境连接已存在的 `127.0.0.1:6379` Redis，使小时/每日窗口计数在服务重启后生效；不改变 fail-open/fail-closed 策略语义。
- 用户侧安全额度换算结果按各字段既有最大值收口，避免最大值回传越界。
- 额度预留拒绝统一经过公共风险响应：配置为 `suspend` 时同步禁用当前 API Key 并刷新凭据缓存；`observe` 和 `notify` 不会误禁用。429 提示和错误日志会明确记录是否已暂停。
- 实际结算超额单独保存并应用风险模式：`observe` 只审计、`notify` 审计并通知、`suspend` 才禁用当前 Key；Redis 正向结算失败且 `fail_closed=true` 时继续保留原有安全暂停语义，退款或负差额失败不暂停。
- 模型风险暂停在通知去重之前执行；五分钟去重只限制重复审计和通知，不再跳过禁用。
- 额度风险和模型风险复用同一用户通知函数，并继续使用现有通知频率限制。
- RPS、并发和 fail-closed 的早期拒绝在认证中间件返回前写入零费用安全错误日志。
- 结算阶段的额度异常写入 `token.risk_detected` 结构化操作审计，记录预计/实际额度、限制类型、风险模式、暂停结果和缓存同步状态。
- Key 禁用成功但缓存同步失败时返回可识别的提交后错误；上层仍记录 `token_suspended=true`，同时记录 `cache_synchronized=false`，避免把已提交状态误报为未暂停。
- 通知限流在 `RedisEnabled=true` 但 Redis 客户端暂不可用时降级到现有内存限流，避免额度风险通知 goroutine 因空客户端失败。
- Redis 通知限流使用单个 Lua 脚本原子完成上限判断和递增；Redis 客户端断线、超时或关闭时记录降级并使用内存限流。内存限流的读取、判断和写入使用互斥保护，避免并发通知覆盖计数。
- 小时/每日安全额度预留增加短生命周期请求状态；同一请求的 Redis 预留和结算可重复执行而不会重复累计。结算统一从 Redis 中的实际预留状态计算差额，避免“服务端已执行但客户端收到超时”造成双重计数。
- 并发租约续期只更新仍然存在的租约；租约已经过期或被清理时不会重新创建，避免 Redis 恢复后复活旧槽位。
- 安全通知在创建异步任务和查询用户前取得通知许可；用户加载失败、通知通道未配置或发送前发生异常时释放未使用许可。普通通知同样先验证目标通道，不再让未实际发送的通知占用次数。
- 通知限流 Key 不再按自然小时切换；Redis 与内存均按配置的持续时间形成同一固定窗口。内存影子计数始终参与检查，Redis 操作结果不确定时不会从零重新放行。
- 模型风险通知去重失败只记录降级日志，不再把已经通过的核心风险检查变成 fail-closed 503。
- 令牌安全策略提交后发生缓存降级时，在返回策略和操作审计中明确给出 `cache_synchronized=false`；数据库提交仍保持成功语义。
- 通知许可记录其所属的内存与 Redis 固定窗口；延迟释放旧许可时只回退同一窗口的计数，不会误减已经开始的新窗口。
- 用户重置令牌安全策略后直接返回已提交策略；缓存同步降级时，响应和操作审计都会保留 `cache_synchronized=false`。

## 验收证据

- `go test ./service -run 'TestPreConsumeBillingRejectsTokenQuotaLimitBeforeDeductionAndRecordsErrorLog|TestReserveTokenSecurityBudgetDoesNotSuspendInObserveMode|TestTokenSecurityBudgetMessagesIdentifyConfiguredScope|TestBillingSessionPreConsumesBeforeChannelMetaInitializationDispatchesAndRefundsDurably|TestFinalizeTokenSecurityBudget|TestRecordTokenSecurityRejectionRecordsTrafficLimit' -count=1`
  - 通过。覆盖计费前拒绝、自动暂停当前 Key、观察模式不误暂停、结算风险响应、用户及令牌额度不变、零费用错误日志、流量限制日志、三种额度提示和退款持久化。
- `go test ./controller -run 'TestUpdateTokenRecordsQuotaAndSecurityPolicyChanges|TestDeleteTokenSecurityPolicyRecordsCommittedResetValues|TestUpdateTokenMasksKeyInResponse|TestUpdateTokenStatusRejectsSecurityPolicyPayload' -count=1`
  - 通过。覆盖额度/安全策略变更审计、重置审计和密钥脱敏。
- `go test ./model -run 'TestResetUserWritableTokenSecurityPolicyPreservesAdministratorFields|TestTokenUpdateWithSecurityPolicyCommitsTogether' -count=1`
  - 通过。覆盖安全策略重置与令牌/策略事务更新。
- `go test ./model -run 'TestSuspendTokenForRiskReportsCommittedCacheFailure' -count=1`
  - 通过。覆盖数据库禁用已提交但 Redis 缓存同步失败时的状态区分。
- `go test ./service -run 'TestNotificationPermitFallsBack|TestNotificationPermitMemoryFallbackIsAtomic' -count=1`
  - 通过。覆盖 Redis 客户端为空或操作失败时的内存降级，以及内存计数并发原子性。
- `go test ./service -run 'TestNotificationPermitReleaseRestoresUnusedCapacity|TestNotifyUserWithoutConfiguredChannelDoesNotConsumeLimit|TestNotifyUserReleasesLimitWhenDeliveryFails' -count=1`
  - 通过。覆盖未使用通知许可释放、通知通道未配置时不占用次数，以及发送失败后退回本次许可。
- `$env:TOKEN_SECURITY_REDIS_TEST_URL='redis://127.0.0.1:6379/0'; go test ./service -run 'TestTokenSecurityRedisScriptsRemainIdempotent|TestNotificationPermitRedisIntegration' -count=1`
  - 通过。使用本机真实 Redis 验证重复预留、重复结算不会重复累计，不存在的并发租约不会被续期复活，以及旧通知许可不会误减新窗口计数。
- `go test ./model -run 'TestStandaloneTokenSecurityPolicyChangesDoNotReportCacheFailureAfterCommit' -count=1`
  - 通过。覆盖策略已提交但缓存同步失败时的状态透明化。
- `go test ./service -run 'TestNotificationPermit' -count=1`
  - 通过。覆盖正常释放未使用许可，以及旧许可不会回退新内存窗口。
- `go test ./model -run 'TestResetUserWritableTokenSecurityPolicyPreservesAdministratorFields' -count=1`
  - 通过。覆盖重置结果直接返回，同时保留管理员管理字段。
- `go test ./controller -run 'TestDeleteTokenSecurityPolicyRecordsCommittedResetValues' -count=1`
  - 通过。覆盖重置提交后的响应与操作审计会暴露缓存同步降级状态。
- `go test ./model ./service ./middleware ./controller -count=1`、`go vet ./model ./service ./middleware ./controller`
  - 通过。相关后端包的定向回归与静态检查无新增失败。
- `bun test src/features/keys/lib/api-key-form.test.ts`
  - 通过。覆盖 CNY 显示额度与内部 quota 的提交、回显双向转换、极小正数不会变成继承/不限额，以及最大值换算不会越界。
- `bun run typecheck`、涉及文件的 `oxlint`、`bun run i18n:sync`
  - 通过；未新增翻译键，各语言包缺失项为 0。
- `go test ./middleware -run '^$' -count=1`
  - 通过编译检查。确认认证中间件使用新的稳定错误码。
- 本机 Redis `127.0.0.1:6379` 执行 RESP `PING`
  - 返回 `PONG`；本地 `.env` 已配置对应连接串。

## 尚未验证

- 未重启当前正在运行的本地服务，也未在浏览器中触发真实 429 弹窗。
- 尚未重启当前 `go run` 进程，因此本地 Redis 配置尚未由运行中服务加载；重启后仍需以 CNY 4 小时额度、CNY 2/次模型完成一次端到端拒绝验收。
- 未模拟真实上游 panic；退款本身及预留记录持久化已有定向测试，panic defer 通过源码和编译检查。
- 尚未执行共享 Redis 的多实例故障切换和超过 30 分钟的长请求续租验收；本机真实 Redis 已覆盖脚本幂等和租约不复活。
- 当前 Windows Go 环境为 `CGO_ENABLED=0`，未执行 `go test -race`。
