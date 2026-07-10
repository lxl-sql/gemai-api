# logs 表结构与 API 对接文档

## 1. 表概述

| 属性 | 值 |
|------|-----|
| 表名 | `logs` |
| ORM | GORM（Go）自动迁移 |
| 默认排序 | `id DESC` |
| 独立部署 | 支持通过环境变量 `LOG_SQL_DSN` 指定独立数据库；未配置时与主库共享 |
| 支持数据库 | MySQL / PostgreSQL / SQLite |

---

## 2. 字段定义

| 字段名 | 数据库列名 | 类型 | 默认值 | JSON 字段名 | 说明 |
|--------|-----------|------|--------|-------------|------|
| Id | `id` | `int` (主键/自增) | — | `id` | 主键，自增 ID |
| UserId | `user_id` | `int` | — | `user_id` | 用户 ID |
| CreatedAt | `created_at` | `bigint` | — | `created_at` | 创建时间，Unix 时间戳（秒） |
| Type | `type` | `int` | — | `type` | 日志类型，见下方枚举 |
| Content | `content` | `text` | — | `content` | 日志内容/描述 |
| Username | `username` | `string` | `''` | `username` | 用户名 |
| TokenName | `token_name` | `string` | `''` | `token_name` | 令牌名称 |
| ModelName | `model_name` | `string` | `''` | `model_name` | 模型名称（如 `gpt-4o`、`claude-3.5-sonnet`） |
| Quota | `quota` | `int` | `0` | `quota` | 消耗额度（内部计量单位） |
| PromptTokens | `prompt_tokens` | `int` | `0` | `prompt_tokens` | 提示词 Token 数 |
| CompletionTokens | `completion_tokens` | `int` | `0` | `completion_tokens` | 补全 Token 数 |
| UseTime | `use_time` | `int` | `0` | `use_time` | 请求耗时（秒） |
| IsStream | `is_stream` | `bool` | `false` | `is_stream` | 是否流式请求 |
| ChannelId | `channel_id` | `int` | — | `channel` | 渠道 ID（注意 JSON 字段名为 `channel`） |
| ChannelName | — | `string` | — | `channel_name` | 渠道名称（**虚拟列**，仅查询时关联填充，不持久化） |
| TokenId | `token_id` | `int` | `0` | `token_id` | 令牌 ID |
| Group | `group` | `string` | — | `group` | 用户分组 |
| Ip | `ip` | `string` | `''` | `ip` | 客户端 IP（仅用户开启 IP 记录时写入） |
| RequestId | `request_id` | `varchar(64)` | `''` | `request_id` | 请求唯一标识 |
| Other | `other` | `text` | — | `other` | 扩展信息，JSON 字符串，详见下方 |

> **注意**：`channel_name` 是 GORM 只读字段（`gorm:"->"`），不存在于实际数据库表中，查询时通过关联 `channels` 表填充。

---

## 3. 索引定义

### 3.1 单列索引

| 列名 | 索引类型 |
|------|---------|
| `user_id` | 普通索引 |
| `username` | 普通索引 |
| `token_name` | 普通索引 |
| `model_name` | 普通索引 |
| `channel_id` | 普通索引 |
| `token_id` | 普通索引 |
| `group` | 普通索引 |
| `ip` | 普通索引 |

### 3.2 组合索引

| 索引名 | 列组合（按 priority 排序） | 用途 |
|--------|--------------------------|------|
| `idx_created_at_id` | `id`, `created_at` | 按时间范围分页查询 |
| `idx_user_id_id` | `user_id`, `id` | 按用户分页查询 |
| `idx_created_at_type` | `created_at`, `type` | 按时间 + 类型过滤 |
| `index_username_model_name` | `model_name`, `username` | 按模型 + 用户名联合查询 |
| `idx_logs_request_id` | `request_id` | 按请求 ID 精确查询 |

---

## 4. 日志类型枚举（`type` 字段）

| 常量名 | 值 | 说明 |
|--------|-----|------|
| `LogTypeUnknown` | `0` | 未知/全部（查询时 type=0 表示不过滤类型） |
| `LogTypeTopup` | `1` | 充值 |
| `LogTypeConsume` | `2` | 消费（API 调用扣费） |
| `LogTypeManage` | `3` | 管理操作 |
| `LogTypeSystem` | `4` | 系统日志 |
| `LogTypeError` | `5` | 错误日志 |
| `LogTypeRefund` | `6` | 退费 |

---

## 5. `other` 字段 JSON 结构

`other` 字段为 JSON 字符串，根据日志类型和场景包含不同键值。以下为所有已知字段：

### 5.1 基础计费信息（消费/错误日志通用）

| 键名 | 类型 | 说明 |
|------|------|------|
| `model_ratio` | `float64` | 模型倍率 |
| `group_ratio` | `float64` | 分组倍率 |
| `completion_ratio` | `float64` | 补全倍率 |
| `model_price` | `float64` | 模型单价 |
| `user_group_ratio` | `float64` | 用户组特殊倍率 |
| `frt` | `float64` | 首 Token 响应时间（毫秒） |
| `request_path` | `string` | 请求路径（如 `/v1/chat/completions`） |

### 5.2 缓存相关

| 键名 | 类型 | 说明 |
|------|------|------|
| `cache_tokens` | `int` | 缓存命中 Token 数 |
| `cache_ratio` | `float64` | 缓存倍率 |
| `cache_creation_tokens` | `int` | 缓存创建 Token 数 |
| `cache_creation_ratio` | `float64` | 缓存创建倍率 |
| `cache_creation_tokens_5m` | `int` | 5 分钟缓存创建 Token 数 |
| `cache_creation_ratio_5m` | `float64` | 5 分钟缓存创建倍率 |
| `cache_creation_tokens_1h` | `int` | 1 小时缓存创建 Token 数 |
| `cache_creation_ratio_1h` | `float64` | 1 小时缓存创建倍率 |
| `cache_write_tokens` | `int` | 缓存写入 Token 总数 |

### 5.3 模型映射与参数覆盖

| 键名 | 类型 | 说明 |
|------|------|------|
| `is_model_mapped` | `bool` | 是否模型映射 |
| `upstream_model_name` | `string` | 上游实际模型名 |
| `reasoning_effort` | `string` | 推理强度参数 |
| `is_system_prompt_overwritten` | `bool` | 是否系统提示词被覆盖 |
| `po` | `object` | 参数覆盖审计信息 |
| `claude` | `bool` | 是否使用 Claude 格式（前端用于展示） |
| `usage_semantic` | `string` | 用量语义标记（如 `"anthropic"`） |
| `input_tokens_total` | `int` | 总输入 Token 数 |

### 5.4 订阅相关

| 键名 | 类型 | 说明 |
|------|------|------|
| `billing_source` | `string` | 计费来源：`"wallet"` 或 `"subscription"` |
| `billing_preference` | `string` | 用户计费偏好 |
| `subscription_id` | `int` | 订阅 ID |
| `subscription_plan_id` | `int` | 订阅套餐 ID |
| `subscription_plan_title` | `string` | 订阅套餐名称 |
| `subscription_pre_consumed` | `int` | 订阅预扣额度 |
| `subscription_post_delta` | `int` | 结算差额（负数为退费） |
| `subscription_total` | `int` | 订阅总额度 |
| `subscription_used` | `int` | 订阅已用额度 |
| `subscription_remain` | `int` | 订阅剩余额度 |
| `subscription_consumed` | `int` | 本次消耗订阅额度 |
| `wallet_quota_deducted` | `int` | 钱包扣费（订阅计费时为 0） |

### 5.5 附加功能计费

| 键名 | 类型 | 说明 |
|------|------|------|
| `image` | `bool` | 是否包含图像 |
| `image_ratio` | `float64` | 图像倍率 |
| `image_output` | `int` | 图像输出 Token 数 |
| `image_generation_call` | `bool` | 是否图像生成调用 |
| `image_generation_call_price` | `float64` | 图像生成调用价格 |
| `web_search` | `bool` | 是否包含网络搜索 |
| `web_search_call_count` | `int` | 网络搜索调用次数 |
| `web_search_price` | `float64` | 网络搜索价格 |
| `file_search` | `bool` | 是否包含文件搜索 |
| `file_search_call_count` | `int` | 文件搜索调用次数 |
| `file_search_price` | `float64` | 文件搜索价格 |
| `audio_input_seperate_price` | `bool` | 音频输入是否独立计价 |
| `audio_input_token_count` | `int` | 音频输入 Token 数 |
| `audio_input_price` | `float64` | 音频输入价格 |

### 5.6 请求转换与格式

| 键名 | 类型 | 说明 |
|------|------|------|
| `request_conversion` | `array` | 请求格式转换链路 |

### 5.7 管理员专属（普通用户查询时会被过滤）

| 键名 | 类型 | 说明 |
|------|------|------|
| `admin_info` | `object` | 管理员调试信息（含 `use_channel`、`is_multi_key`、`multi_key_index` 等） |
| `reject_reason` | `string` | 拒绝原因 |

### 5.8 任务计费专属

| 键名 | 类型 | 说明 |
|------|------|------|
| `task_id` | `string` | 任务 ID |
| `reason` | `string` | 计费原因 |
| `pre_consumed_quota` | `int` | 预消耗额度 |
| `actual_quota` | `int` | 实际消耗额度 |

---

## 6. API 接口

### 6.1 获取日志列表（管理员）

```
GET /api/log/
```

**权限**：管理员

**查询参数**：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `p` | int | 否 | 页码（从 1 开始），默认 1 |
| `page_size` | int | 否 | 每页数量，默认系统值，最大 100 |
| `type` | int | 否 | 日志类型（0=全部） |
| `username` | string | 否 | 精确匹配用户名 |
| `token_name` | string | 否 | 精确匹配令牌名 |
| `model_name` | string | 否 | 模型名（支持 LIKE 模糊匹配） |
| `start_timestamp` | int64 | 否 | 起始时间戳（Unix 秒） |
| `end_timestamp` | int64 | 否 | 结束时间戳（Unix 秒） |
| `channel` | int | 否 | 渠道 ID |
| `group` | string | 否 | 用户分组 |
| `request_id` | string | 否 | 请求 ID 精确匹配 |

**响应示例**：

```json
{
  "success": true,
  "message": "",
  "data": {
    "page": 1,
    "page_size": 20,
    "total": 1500,
    "items": [
      {
        "id": 12345,
        "user_id": 1,
        "created_at": 1712300000,
        "type": 2,
        "content": "模型 gpt-4o 消耗 xxx 额度",
        "username": "admin",
        "token_name": "my-token",
        "model_name": "gpt-4o",
        "quota": 5000,
        "prompt_tokens": 100,
        "completion_tokens": 200,
        "use_time": 3,
        "is_stream": true,
        "channel": 5,
        "channel_name": "OpenAI 渠道",
        "token_id": 10,
        "group": "default",
        "ip": "192.168.1.1",
        "request_id": "req_abc123",
        "other": "{\"model_ratio\":1.0,\"group_ratio\":1.0,...}"
      }
    ]
  }
}
```

### 6.2 获取日志列表（用户自己）

```
GET /api/log/self
```

**权限**：登录用户

**查询参数**：与管理员接口类似，但**不支持** `username` 和 `channel` 参数（自动按当前用户过滤）。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `p` | int | 否 | 页码 |
| `page_size` | int | 否 | 每页数量，最大 100 |
| `type` | int | 否 | 日志类型 |
| `token_name` | string | 否 | 令牌名 |
| `model_name` | string | 否 | 模型名（LIKE 模糊匹配） |
| `start_timestamp` | int64 | 否 | 起始时间戳 |
| `end_timestamp` | int64 | 否 | 结束时间戳 |
| `group` | string | 否 | 分组 |
| `request_id` | string | 否 | 请求 ID |

**响应格式**：与管理员接口相同，但：
- `id` 字段会被重新编号（从 offset+1 开始递增，非数据库真实 ID）
- `channel_name` 被清空
- `other` 中 `admin_info` 和 `reject_reason` 字段被移除

**总数上限**：用户日志查询总数最大返回 10000。

### 6.3 获取统计数据（管理员）

```
GET /api/log/stat
```

**权限**：管理员

**查询参数**：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `type` | int | 否 | 仅支持 `0`（默认）或 `2`（消费日志）；统计口径固定为消费日志 |
| `username` | string | 否 | 用户名 |
| `token_name` | string | 否 | 令牌名 |
| `model_name` | string | 否 | 模型名 |
| `start_timestamp` | int64 | 否 | 起始时间戳 |
| `end_timestamp` | int64 | 否 | 结束时间戳 |
| `channel` | int | 否 | 渠道 ID |
| `group` | string | 否 | 分组 |

**响应示例**：

```json
{
  "success": true,
  "message": "",
  "data": {
    "quota": 500000,
    "rpm": 42,
    "tpm": 15000
  }
}
```

| 返回字段 | 类型 | 说明 |
|---------|------|------|
| `quota` | int64 | 过滤条件下的总消耗额度（仅统计 `type=2` 的消费日志） |
| `rpm` | int64 | 最近 60 秒内的请求数（结果有约 10 秒缓存） |
| `tpm` | int64 | 最近 60 秒内的 Token 数（`prompt_tokens + completion_tokens`，同上有缓存） |

**语义说明**：

- `end_timestamp` 为 0 或晚于当前时间时，按当前时间处理（旧版本 0 表示不设上限）。
- 统计数据按分钟预聚合，目标覆盖最近 7 天（保留边界额外留 1 小时缓冲，每整点清理一次过期分钟桶；即使回填被停用清理也照常执行）。整分钟读聚合表，首尾不足一分钟的碎片和水位之后的短尾部从原始日志补算。
- `start_timestamp=0` 等同于 `LOG_QUERY_DEFAULT_DAYS`（默认 7 天）；该配置为 0 时收敛为“当前已覆盖的全部聚合数据”，绝不回退到原始日志全表扫描。
- 历史回填**由近及远**执行：部署后最近 5 分钟范围先可用，更长时间窗随回填推进逐步可查；接口不会把部分区间静默伪装成完整总额，查询起点尚未覆盖时返回“统计数据正在初始化”。
- 聚合任务停滞时，纯历史区间查询不受影响；只有需要最新数据的查询会返回“统计任务暂时滞后”。
- 日志清理（log cleanup）与实时/回填聚合使用跨任务共享租约；清理期间统计返回“暂时滞后”，完成后自动同步聚合数据，避免并发任务重新写回已删除统计。
- 清理中断自愈：清理进程崩溃或对账失败时，实时聚合任务会按状态表记录的 `cleanup_target` 每分钟自动补齐对账并解除“滞后”。仅当遗留状态无目标记录（如从旧版本升级时清理恰好进行中）才只清除标志不对账，此时建议手动重跑一次日志清理。
- `/stat` 与 `/self/stat` 使用 `SEARCH_RATE_LIMIT_*` 用户级限流，超限返回 HTTP 429（前端显示“请求过多”提示，不自动重试）。

**相关配置（环境变量）**：

- `LOG_STAT_QUERY_TIMEOUT_SECONDS`：接口查询超时，默认 10 秒。
- `LOG_STAT_RAW_TAIL_MAX_MINUTES`：允许从原始日志补算的最大总时长，默认 10 分钟。
- `LOG_STAT_ROLLUP_ENABLED`：是否启用实时分钟聚合任务，默认 `true`。设为 `false` 时统计接口返回“统计功能已被管理员停用”（紧急制动，不回退到原始日志全量聚合）。
- `LOG_STAT_BACKFILL_ENABLED`：是否启用历史回填，默认 `true`；生产 IO 异常时可独立关闭。关闭后未覆盖的历史范围返回“所选时间范围暂无统计数据”。
- `LOG_STAT_BACKFILL_CHUNK_MINUTES`：回填分块大小，默认 30 分钟（5–120）。
- `LOG_STAT_BACKFILL_MAX_CHUNKS_PER_RUN`：单次回填任务最多处理的分块数，默认 2（1–12）。
- `LOG_STAT_CHUNK_TIMEOUT_SECONDS`：单个聚合分块的查询超时，默认 120 秒。
- `LOG_STAT_MAX_GROUPS_PER_CHUNK`：单块最多物化的维度组合数，默认 100000；SQL 使用 `LIMIT + 1` 检测截断，超限自动把分块减半重试（下限 1 分钟），截断结果绝不落库。
- `LOG_STAT_BACKFILL_PAUSE_MS`：回填分块之间暂停时间下限，默认 250 毫秒；实际暂停不少于分块自身耗时（占空比 ≤ 50%）。本轮最后一块之后不暂停，尽快释放维护锁让实时聚合继续推进。
- `LOG_STAT_REPAIR_MAX_CHUNKS_PER_RUN`：水位修复单次最多处理分块数，默认 2（每块最多 30 分钟）。
- `LOG_STAT_REPAIR_PAUSE_MS`：水位修复块间暂停，默认 250 毫秒。
- `LOG_SQL_PG_IDLE_IN_TX_TIMEOUT_MS` / `LOG_SQL_PG_LOCK_TIMEOUT_MS` / `LOG_SQL_PG_STATEMENT_TIMEOUT_MS`：独立日志库为 PostgreSQL 时的数据库级角色保护，仅作用于日志数据库；未设置时回落到对应的 `SQL_PG_*` 值。

### 6.4 获取统计数据（用户自己）

```
GET /api/log/self/stat
```

**权限**：登录用户

**查询参数与响应**：与管理员统计接口相同（自动按当前用户名过滤）。

### 6.5 按令牌查询日志

```
GET /api/log/token
```

**权限**：令牌只读认证（Token Auth）

**响应**：返回该令牌最近的日志记录（数量由 `MaxRecentItems` 控制）。

```json
{
  "success": true,
  "message": "",
  "data": [
    { "id": 1, "...": "..." }
  ]
}
```

### 6.6 删除历史日志（管理员）

```
DELETE /api/log/
```

**权限**：管理员

**查询参数**：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `target_timestamp` | int64 | 是 | 删除此时间戳之前的所有日志（Unix 秒） |

**响应**：

```json
{
  "success": true,
  "message": "",
  "data": 1500
}
```

`data` 为实际删除的记录数。

---

## 7. 直接查询 SQL 参考

### 7.1 建表 DDL（MySQL 参考，实际由 GORM AutoMigrate 生成）

```sql
CREATE TABLE `logs` (
  `id`                bigint       NOT NULL AUTO_INCREMENT,
  `user_id`           int          DEFAULT NULL,
  `created_at`        bigint       DEFAULT NULL,
  `type`              int          DEFAULT NULL,
  `content`           longtext,
  `username`          varchar(255) DEFAULT '',
  `token_name`        varchar(255) DEFAULT '',
  `model_name`        varchar(255) DEFAULT '',
  `quota`             int          DEFAULT 0,
  `prompt_tokens`     int          DEFAULT 0,
  `completion_tokens` int          DEFAULT 0,
  `use_time`          int          DEFAULT 0,
  `is_stream`         tinyint(1)   DEFAULT NULL,
  `channel_id`        int          DEFAULT NULL,
  `token_id`          int          DEFAULT 0,
  `group`             varchar(255) DEFAULT NULL,
  `ip`                varchar(255) DEFAULT '',
  `request_id`        varchar(64)  DEFAULT '',
  `other`             longtext,
  PRIMARY KEY (`id`),
  INDEX `idx_created_at_id`            (`id`, `created_at`),
  INDEX `idx_user_id_id`               (`user_id`, `id`),
  INDEX `idx_created_at_type`          (`created_at`, `type`),
  INDEX `index_username_model_name`    (`model_name`, `username`),
  INDEX `idx_logs_request_id`          (`request_id`),
  INDEX `idx_logs_user_id`             (`user_id`),
  INDEX `idx_logs_username`            (`username`),
  INDEX `idx_logs_token_name`          (`token_name`),
  INDEX `idx_logs_model_name`          (`model_name`),
  INDEX `idx_logs_channel_id`          (`channel_id`),
  INDEX `idx_logs_token_id`            (`token_id`),
  INDEX `idx_logs_group`               (`group`),
  INDEX `idx_logs_ip`                  (`ip`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

> ⚠️ 以上 DDL 为根据 GORM 标签推导的参考，实际列类型和索引名可能因 GORM 版本和数据库方言略有差异。

### 7.2 常用查询示例

**按时间范围查询消费日志：**

```sql
SELECT * FROM logs
WHERE type = 2
  AND created_at >= 1712200000
  AND created_at <= 1712300000
ORDER BY id DESC
LIMIT 20 OFFSET 0;
```

**按用户 + 模型查询：**

```sql
SELECT * FROM logs
WHERE user_id = 1
  AND model_name LIKE '%gpt-4%'
ORDER BY id DESC
LIMIT 20 OFFSET 0;
```

**统计总消耗额度：**

```sql
SELECT SUM(quota) AS total_quota
FROM logs
WHERE type = 2
  AND created_at >= 1712200000
  AND created_at <= 1712300000;
```

**统计最近 60 秒 RPM / TPM：**

```sql
SELECT COUNT(*) AS rpm,
       SUM(prompt_tokens) + SUM(completion_tokens) AS tpm
FROM logs
WHERE type = 2
  AND created_at >= UNIX_TIMESTAMP(NOW()) - 60;
```

**按模型分组统计：**

```sql
SELECT model_name,
       COUNT(*) AS request_count,
       SUM(quota) AS total_quota,
       SUM(prompt_tokens) AS total_prompt_tokens,
       SUM(completion_tokens) AS total_completion_tokens
FROM logs
WHERE type = 2
GROUP BY model_name
ORDER BY total_quota DESC;
```

---

## 8. 注意事项

1. **`group` 列是 SQL 保留字**：在 MySQL/SQLite 中需用反引号 `` `group` `` 引用，在 PostgreSQL 中需用双引号 `"group"` 引用。
2. **时间字段为 Unix 时间戳（秒）**：`created_at` 存储的是 `int64` 类型的 Unix 秒级时间戳，非毫秒。
3. **`channel` vs `channel_id`**：数据库列名为 `channel_id`，但 JSON 序列化时字段名为 `channel`。
4. **`channel_name` 不在表中**：该字段仅在查询时通过关联 `channels` 表填充，直接查 `logs` 表不会有此列。
5. **`other` 字段需 JSON 解析**：存储为 JSON 字符串，使用时需反序列化。
6. **用户日志脱敏**：用户接口返回的日志会移除 `admin_info`、`reject_reason` 等管理员字段，且 `id` 被重新编号。
7. **日志记录受开关控制**：消费日志（`type=2`）受 `LogConsumeEnabled` 配置控制，关闭后不写入。
8. **IP 记录受用户设置控制**：仅当用户设置了 `RecordIpLog` 时才记录 `ip` 字段。
