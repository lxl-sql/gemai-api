# PostgreSQL Autovacuum 运维手册

本文档用于后续排查和处理 PostgreSQL 表膨胀、死元组过多、统计信息过期等问题。仅适用于 PostgreSQL，不适用于 SQLite / MySQL。

## 1. Autovacuum 是什么

PostgreSQL 的 `UPDATE` 和 `DELETE` 通常不会立刻物理删除旧数据，而是留下旧版本，也就是死元组。`autovacuum` 是 PostgreSQL 的后台自动维护机制，主要负责：

- 清理死元组，让表内空间可以被后续写入复用。
- 自动执行 `ANALYZE`，更新统计信息，帮助查询优化器选择正确执行计划。
- 防止事务 ID 回卷风险。

普通 `VACUUM` / `autovacuum` 通常不会把磁盘空间立即还给操作系统，只是让表内部空间可复用。需要真正缩小表文件时才考虑 `VACUUM FULL`，但它会锁表，线上需谨慎。

## 2. 常用巡检 SQL

### 2.1 查看死元组最多的表

```sql
SELECT relname, n_live_tup, n_dead_tup, last_autovacuum
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC
LIMIT 10;
```

### 2.2 按死元组比例排序

```sql
SELECT
  relname,
  n_live_tup,
  n_dead_tup,
  round(n_dead_tup::numeric / greatest(n_live_tup, 1) * 100, 2) AS dead_pct,
  last_vacuum,
  last_autovacuum,
  last_analyze,
  last_autoanalyze,
  vacuum_count,
  autovacuum_count
FROM pg_stat_user_tables
ORDER BY dead_pct DESC
LIMIT 20;
```

### 2.3 查看是否有长事务阻止清理

长事务会导致 vacuum 无法真正回收旧版本。如果死元组持续上涨，优先查这个。

```sql
SELECT
  pid,
  usename,
  state,
  now() - xact_start AS xact_age,
  age(backend_xmin) AS xmin_age,
  left(query, 200) AS query
FROM pg_stat_activity
WHERE backend_xmin IS NOT NULL
ORDER BY age(backend_xmin) DESC;
```

### 2.4 查看正在运行的 vacuum

```sql
SELECT
  pid,
  relid::regclass AS table_name,
  phase,
  heap_blks_total,
  heap_blks_scanned,
  heap_blks_vacuumed,
  index_vacuum_count
FROM pg_stat_progress_vacuum;
```

### 2.5 查看表级 autovacuum 参数

```sql
SELECT
  relname,
  reloptions
FROM pg_class
WHERE relkind = 'r'
  AND relnamespace = 'public'::regnamespace
  AND reloptions IS NOT NULL
ORDER BY relname;
```

## 3. 手动维护命令

### 3.1 推荐的安全清理

优先使用 `VACUUM (ANALYZE)`，它不会像 `VACUUM FULL` 那样重写整张表。

```sql
VACUUM (ANALYZE) users;
VACUUM (ANALYZE) channels;
VACUUM (ANALYZE) tokens;
VACUUM (ANALYZE) oauth_authorization_codes;
VACUUM (ANALYZE) redemptions;
VACUUM (ANALYZE) quota_data;
```

### 3.2 只更新统计信息

如果主要是执行计划不准、查询突然变慢，可以先执行 `ANALYZE`。

```sql
ANALYZE users;
ANALYZE channels;
ANALYZE tokens;
ANALYZE quota_data;
```

### 3.3 谨慎使用 VACUUM FULL

`VACUUM FULL` 会重写表并持有强锁，可能阻塞线上读写。仅在确认表/索引膨胀严重、并且可以接受维护窗口时使用。

```sql
VACUUM FULL users;
```

如果只是索引膨胀，优先考虑：

```sql
REINDEX TABLE CONCURRENTLY users;
```

## 4. 表级 Autovacuum 参数模板

PostgreSQL 默认触发阈值大致是：

```text
autovacuum_vacuum_threshold + autovacuum_vacuum_scale_factor * 表行数
```

默认 `scale_factor` 通常是 `0.2`。大表可能要积累很多死元组才会触发，小表但高频更新/删除的表也可能清理不够及时。因此可以对热点表设置更敏感的表级参数。

### 4.1 users 表

适合活跃用户资料、余额、状态等更新较频繁的场景。

```sql
ALTER TABLE users SET (
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_vacuum_threshold = 1000,
  autovacuum_analyze_scale_factor = 0.02,
  autovacuum_analyze_threshold = 1000
);
```

### 4.2 channels 表

如果表行数少但更新频繁，建议降低阈值。

```sql
ALTER TABLE channels SET (
  autovacuum_vacuum_scale_factor = 0.05,
  autovacuum_vacuum_threshold = 50,
  autovacuum_analyze_scale_factor = 0.05,
  autovacuum_analyze_threshold = 50
);
```

### 4.3 tokens 表

适合频繁更新状态、额度、过期时间或使用统计的 token 表。

```sql
ALTER TABLE tokens SET (
  autovacuum_vacuum_scale_factor = 0.03,
  autovacuum_vacuum_threshold = 500,
  autovacuum_analyze_scale_factor = 0.03,
  autovacuum_analyze_threshold = 500
);
```

### 4.4 oauth_authorization_codes 表

授权码通常生命周期很短，容易产生大量删除记录。

```sql
ALTER TABLE oauth_authorization_codes SET (
  autovacuum_vacuum_scale_factor = 0.01,
  autovacuum_vacuum_threshold = 20,
  autovacuum_analyze_scale_factor = 0.05,
  autovacuum_analyze_threshold = 20
);
```

### 4.5 quota_data 表

`quota_data` 行数较大时，不建议阈值过低，否则 vacuum 可能过于频繁。可以先使用温和参数。

```sql
ALTER TABLE quota_data SET (
  autovacuum_vacuum_scale_factor = 0.05,
  autovacuum_vacuum_threshold = 50000,
  autovacuum_analyze_scale_factor = 0.03,
  autovacuum_analyze_threshold = 50000
);
```

## 5. 恢复默认表级参数

如果后续发现某张表 autovacuum 太频繁，可以重置为数据库默认值。

```sql
ALTER TABLE users RESET (
  autovacuum_vacuum_scale_factor,
  autovacuum_vacuum_threshold,
  autovacuum_analyze_scale_factor,
  autovacuum_analyze_threshold
);
```

## 6. 推荐处理流程

1. 先执行巡检 SQL，确认 `n_dead_tup` 和 `dead_pct`。
2. 如果 `dead_pct` 很高，先检查是否有长事务。
3. 没有长事务阻塞时，在低峰期执行 `VACUUM (ANALYZE)`。
4. 连续观察 1 到 3 天，如果同一张表反复死元组过高，再设置表级 autovacuum 参数。
5. 不要直接在线上使用 `VACUUM FULL`，除非已有维护窗口。

## 7. 当前数据的建议优先级

根据最近一次巡检结果，建议优先关注：

- `channels`：死元组远高于活元组，优先清理并调低阈值。
- `users`：死元组数量和比例都较高，建议 `VACUUM (ANALYZE)` 后观察。
- `tokens`：死元组比例较明显，适合调表级参数。
- `oauth_authorization_codes`：短生命周期表，适合使用低阈值 autovacuum。
- `quota_data`：死元组绝对值较大，但相对比例较低，可先温和调参。

## 8. 放到代码迁移中的注意事项

本项目支持 SQLite、MySQL、PostgreSQL。`ALTER TABLE ... SET (autovacuum_...)` 是 PostgreSQL 专属语法，如果要写进 Go 迁移逻辑，必须仅在 PostgreSQL 下执行。

示例：

```go
if common.UsingPostgreSQL {
    model.DB.Exec(`ALTER TABLE users SET (
        autovacuum_vacuum_scale_factor = 0.02,
        autovacuum_vacuum_threshold = 1000,
        autovacuum_analyze_scale_factor = 0.02,
        autovacuum_analyze_threshold = 1000
    )`)
}
```

不建议在普通业务请求里执行这些 SQL。它们应由 DBA 手动执行，或放在数据库初始化/迁移阶段执行。
