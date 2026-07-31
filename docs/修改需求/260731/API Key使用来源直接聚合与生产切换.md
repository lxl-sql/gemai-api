# API Key 使用来源直接批量聚合实施说明

日期：2026-07-31  
适用拓扑：1 个 master、多个 slave，共用 PostgreSQL 与 Redis  
参考规模：日在线用户约 2 万，业务峰值约 5000 RPM

## 1. 最终方案

API Key 使用来源只采用“请求终态直接批量聚合”，不再采用日志扫描、实时日志
rollup 或历史日志回填。

- 一次外部 API 请求只在处理结束后产生一个终态结果：成功或错误。
- 渠道内部重试不会重复计次；最终成功记一次成功，最终失败记一次错误。
- 每个 API 实例按 `token_id + IP + User-Agent` 在本机内存中合并增量。
- 每 5 秒刷新一次；单批最多处理 25 个 Key，Key ID 排序后进入 PostgreSQL 事务。
- 结果只写 `token_usage_sources`，协调、启停与删除墓碑使用
  `token_usage_source_meta`。
- 不更新 `tokens` 热表，不新增逐请求统计表，不用 Redis 保存计数事实。
- `request_count` 不单独落库，接口固定按
  `success_count + error_count` 计算。
- 每个 Key 默认只保留最近 500 个去重来源，可通过
  `token_usage_source_setting.max_sources_per_token` 调整。

普通 `logs` 仍可按原配置用于审计和故障排查，但它不是来源次数的输入。
关闭消费日志、清理日志或把日志库拆分到独立数据库，都不会改变使用来源计数。
`RecordIpLog` 只控制普通日志中的 IP/User-Agent 展示，不控制来源聚合。

## 2. 请求与写入链路

1. 鉴权成功后，中间件取得 `user_id`、`token_id`、客户端 IP 和 User-Agent。
2. 请求完成后读取最终 HTTP/Relay 结果；流式请求由 Relay 显式覆盖最终成功状态。
3. 终态事件进入当前实例的有界内存缓冲区。
4. 相同 Key、IP、User-Agent 在刷新前合并成功/错误增量及首末时间。
5. 刷新事务先按 Key ID 顺序锁定对应 `token_usage_source_meta` 行。
6. 仅当 Key 所属用户一致、未删除且跟踪启用时，才合并到
   `token_usage_sources`。
7. 超过来源上限时按 `last_seen_at` 保留最近来源，并设置 `truncated=true`。

不同 master/slave 可以同时刷新同一个 Key。PostgreSQL 中的元数据行锁负责串行化
同 Key 合并，不锁 `tokens`，也不依赖单机定时任务领导者。

## 3. 故障边界

- PostgreSQL 短暂失败：失败批次与尚未处理批次重新并回本机缓冲区，后续继续刷新。
- 缓冲区最多保留 100000 个不同的来源身份；相同身份继续合并，不新增槽位。
- 缓冲区已满时拒绝新的来源身份，并且每分钟最多记录一次错误，避免故障期间耗尽内存。
- 正常 SIGINT/SIGTERM 会停止接收请求，并使用独立的 10 秒窗口刷新剩余批次。
- SIGKILL、宿主机掉电或容器被强制杀死时，当前节点尚未刷新的最多约 5 秒来源增量
  可能丢失；这不影响鉴权、额度、计费和普通日志。

## 4. 配置

系统设置只暴露以下来源配置：

| 配置 | 默认值 | 用途 |
| --- | ---: | --- |
| `token_usage_source_setting.enabled` | `false` | 开启后立即接收请求终态并直接批量聚合 |
| `token_usage_source_setting.reconcile_enabled` | `false` | 在混合版本发布期间小批量修复 Key 元数据 |
| `token_usage_source_setting.max_sources_per_token` | `500` | 每个 Key 保留的最近去重来源上限 |

不存在切换时间、日志回填天数或来源历史回填开关。来源接口只返回
`counting_mode=direct_batch`，也不再返回历史日志水位、回填进度或“次数尚未完整”提示。

## 5. 生产发布顺序

1. 发布前保持 `token_usage_source_setting.enabled=false`。
2. 备份 PostgreSQL，并记录 API 错误率、P95/P99、锁等待、WAL 与副本延迟基线。
3. 先升级唯一 master，等待 GORM 迁移完成并确认健康检查、数据库连接正常。
4. 逐台滚动升级 slave；每台正常后再继续下一台。
5. 确认所有旧实例已经退出，所有实例连接相同 PostgreSQL 写主库。
6. 可先启用 `reconcile_enabled`，等待至少一个完整对账周期。
7. 启用 `enabled`。保存后各实例按现有配置同步周期生效，无需设置时间戳。
8. 从至少两个不同实例各发送成功和失败请求，5 至 10 秒后验收来源弹窗。

混合版本期间不要开启 `enabled`，因为旧实例不会提交请求终态，计数会形成缺口。

## 6. 上线验收

必须检查：

- 同一外部请求触发多次内部渠道重试时，总请求数只增加 1。
- 成功请求只增加 `success_count`，最终失败只增加 `error_count`。
- `request_count = success_count + error_count`。
- 分别经过至少两个实例的请求都能在 5 至 10 秒内出现。
- 临时关闭 `LogConsumeEnabled` 后，来源计数仍正常增加；验收后恢复原配置。
- 删除 Key 后，延迟批次不能重建来源。
- `tokens` 表没有因来源计次产生持续 UPDATE。
- PostgreSQL 没有持续行锁等待、长事务或明显副本回放延迟。

参考查询：

```sql
SELECT token_id, ip, user_agent, success_count, error_count,
       success_count + error_count AS request_count,
       first_seen_at, last_seen_at
FROM token_usage_sources
WHERE token_id = :token_id
ORDER BY last_seen_at DESC
LIMIT 20;

SELECT count(*) AS lock_waiters
FROM pg_stat_activity
WHERE wait_event_type = 'Lock';
```

## 7. 回滚

1. 先设置 `token_usage_source_setting.enabled=false`，等待至少 10 秒。
2. 正常终止实例，让各节点执行最后一次缓冲刷新。
3. 回滚应用镜像。
4. 不清空 `token_usage_sources` 和 `token_usage_source_meta`，不执行 DDL 回退。
5. 修复版本重新发布后，按第 5 节重新启用；停用期间不会从日志补计。

## 8. 监控

应用日志重点关注：

- `flush token usage source batch failed`
- `token usage source direct buffer is full`
- `flush token usage source updates before exit failed`

数据库重点关注：

- `token_usage_source_meta` 行锁等待与事务时长
- `token_usage_sources` 写入速率、dead tuples 和索引膨胀
- PostgreSQL WAL 速率及主从复制延迟

Redis 不参与来源计数；Redis 故障不会改变这条写入链路。

## 10. 发布前复审补充

2026-07-31 再次复核终态采集、批量缓冲、PostgreSQL 并发合并、删除墓碑、
查询接口和前端弹窗。补充修复：使用来源弹窗打开期间每 5 秒自动刷新，
避免弹窗保持打开时仍显示旧计数。

复审门禁结果：

- 相关 Go 包测试通过。
- PostgreSQL 16 隔离实例上的并发合并与删除竞态集成测试通过。
- 全仓 Go 测试除 Windows 临时可执行文件锁冲突外均通过；受影响的
  `relay/helper` 与 `router` 已单独重跑通过。
- `go build -p 1 ./...` 与相关包 `go vet` 通过。
- 前端 25 个测试文件、102 个测试通过，类型检查、生产构建与相关文件
  定向 lint 通过。

## 9. 本地验证记录

2026-07-31 已完成：

- `go test -p 1 ./... -count=1`：全仓通过。
- `go build -p 1 ./...`：通过。
- `go vet ./setting/system_setting ./controller ./model ./middleware ./service ./router`：
  通过。
- `bun run typecheck`：通过。
- `bun run test`：25 个文件、102 个用例通过。
- `bun run build`：通过。
- 本次修改文件的定向 `oxlint`：通过。

全仓 `bun run lint` 仍被仓库内其他未修改文件的既有规则错误阻断，本次修改文件无新增
lint 错误。真实 PostgreSQL 主从、反向代理客户端 IP 和多实例流量仍必须按第 6 节
在生产同构预发布环境验收。
