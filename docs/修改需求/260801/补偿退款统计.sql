-- =====================================================================
-- 2026-08-02 带宽枯竭事故 —— 用户补偿退款【只读统计】脚本
-- 对应事故文档：生产事故问题与修复总结.md § 11.7 第 4 条
--
-- 补偿窗口：2026-07-31 17:00:00 ~ 2026-08-02 17:00:00（北京时间 UTC+8）
--   采用半开区间 [start, end)，恰好 48 小时。
--   logs.created_at 为 Unix 时间戳，与服务器/数据库时区无关：
--     start = 1785488400   -- 2026-07-31T09:00:00Z = 07-31 17:00 +08
--     end   = 1785661200   -- 2026-08-02T09:00:00Z = 08-02 17:00 +08
--
-- 应退口径：
--   应退 = 窗口内消费(type=2 之和) - 窗口内系统已自动退款(type=6 之和)
--   * type=6（LogTypeRefund）的 quota 记录为正数（见 service/task_billing.go），
--     这部分已经退回用户余额，补偿时必须扣除，否则会双重退款。
--   * 换算：美元 = quota / 500000（common.QuotaPerUnit 默认值；
--     若线上通过设置修改过 QuotaPerUnit，请同步修改本脚本内的 500000）。
--
-- 执行注意（重要）：
--   1. 本脚本全部为只读 SELECT，不修改任何数据。
--   2. logs 为十亿级大表，且 DB 主机公网入口带宽受限（本次事故根因），
--      必须在 DB 本机（127.0.0.1）或内网路径、低峰期执行。
--   3. 过滤条件 (created_at, type) 命中复合索引 idx_created_at_type。
--   4. 目标数据库为生产 PostgreSQL；聚合语句同时兼容 MySQL/SQLite
--      （未使用 FILTER 等 PG 专有语法）。
--   5. 若日志库与主库分离（LOG_DB），Q1~Q4 在日志库执行；
--      Q5 的 users 联表仅在主库与日志库同库时可用，分库时改用
--      Q4 结果中的 user_id 清单回主库核对。
-- =====================================================================


-- ---------------------------------------------------------------------
-- Q1. 总额统计：本次补偿总应退额度（最终对外口径以本查询 net 为准）
-- ---------------------------------------------------------------------
SELECT
    SUM(CASE WHEN type = 2 THEN quota ELSE 0 END)                    AS gross_consume_quota,   -- 窗口内总消费
    SUM(CASE WHEN type = 6 THEN quota ELSE 0 END)                    AS auto_refunded_quota,   -- 窗口内系统已自动退款
    SUM(CASE WHEN type = 2 THEN quota ELSE -quota END)               AS net_refund_quota,      -- 应退净额
    ROUND(SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) / 500000.0, 4) AS net_refund_usd,
    SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END)                        AS consume_log_count,
    SUM(CASE WHEN type = 6 THEN 1 ELSE 0 END)                        AS refund_log_count,
    COUNT(DISTINCT user_id)                                          AS affected_users
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type IN (2, 6);


-- ---------------------------------------------------------------------
-- Q2. 按小时分布：核对消费曲线与事故时间线是否吻合
--     （7/31 17:00 起波动、8/2 白天崩溃循环期间消费应明显下降）
-- ---------------------------------------------------------------------
SELECT
    (created_at - 1785488400) / 3600                                 AS hour_offset,  -- 窗口起点后第 N 小时
    SUM(CASE WHEN type = 2 THEN quota ELSE 0 END)                    AS consume_quota,
    SUM(CASE WHEN type = 6 THEN quota ELSE 0 END)                    AS refund_quota,
    COUNT(*)                                                         AS log_count
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type IN (2, 6)
GROUP BY (created_at - 1785488400) / 3600
ORDER BY hour_offset;


-- ---------------------------------------------------------------------
-- Q3. 边界核对（可选，代价较高）：窗口内的退款是否对应窗口外的消费
--     任务类（video/mj 等）先预扣后结算，预扣发生在窗口前、退款落在窗口内
--     时，Q1 的 net 会被多扣。此查询量化该误差；金额小可忽略，
--     金额大则需把这部分退款额加回 net。
--     注意：request_id 无索引，靠 created_at 范围（窗口前 7 天起）走索引，
--     仍属重查询，务必低峰执行。
-- ---------------------------------------------------------------------
SELECT
    COUNT(*)                    AS orphan_refund_count,   -- 窗口内退款、但窗口内找不到同 request_id 消费
    COALESCE(SUM(r.quota), 0)   AS orphan_refund_quota,
    ROUND(COALESCE(SUM(r.quota), 0) / 500000.0, 4) AS orphan_refund_usd
FROM logs r
WHERE r.type = 6
  AND r.created_at >= 1785488400
  AND r.created_at <  1785661200
  AND r.request_id <> ''
  AND NOT EXISTS (
      SELECT 1 FROM logs c
      WHERE c.type = 2
        AND c.created_at >= 1785488400
        AND c.created_at <  1785661200
        AND c.request_id = r.request_id
  );

-- Q3b. request_id 为空的窗口内退款（老日志无法关联，只能人工判断）
SELECT COUNT(*) AS empty_reqid_refund_count,
       COALESCE(SUM(quota), 0) AS empty_reqid_refund_quota
FROM logs
WHERE type = 6
  AND created_at >= 1785488400
  AND created_at <  1785661200
  AND request_id = '';


-- ---------------------------------------------------------------------
-- Q4. 按用户明细：批量退款脚本的执行清单（user_id + 应退额度）
--     HAVING > 0：净额为负（窗口内退款多于消费，见 Q3 场景）的用户不补偿。
--     username 取自日志快照，仅供人工核对，执行以 user_id 为准。
-- ---------------------------------------------------------------------
SELECT
    user_id,
    MAX(username)                                                    AS username_snapshot,
    SUM(CASE WHEN type = 2 THEN quota ELSE 0 END)                    AS consume_quota,
    SUM(CASE WHEN type = 6 THEN quota ELSE 0 END)                    AS refunded_quota,
    SUM(CASE WHEN type = 2 THEN quota ELSE -quota END)               AS net_refund_quota,
    ROUND(SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) / 500000.0, 6) AS net_refund_usd,
    COUNT(*)                                                         AS log_count
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type IN (2, 6)
GROUP BY user_id
HAVING SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) > 0
ORDER BY net_refund_quota DESC;

-- 交叉校验：Q4 的 SUM(net_refund_quota) 必须等于 Q1 的 net_refund_quota
-- 加上「净额为负的用户合计」（下查询），否则统计口径有误。
SELECT COALESCE(SUM(t.net_quota), 0) AS negative_net_total
FROM (
    SELECT SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) AS net_quota
    FROM logs
    WHERE created_at >= 1785488400
      AND created_at <  1785661200
      AND type IN (2, 6)
    GROUP BY user_id
    HAVING SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) < 0
) t;


-- ---------------------------------------------------------------------
-- Q6. 【异常核查】单笔大额消费 TOP 50
--     Q1 实测均值 ≈ $4.24/笔，远超正常水平，先看是谁在拉总数。
-- ---------------------------------------------------------------------
SELECT id, user_id, username, model_name, quota,
       ROUND(quota / 500000.0, 2) AS usd,
       prompt_tokens, completion_tokens, use_time, created_at, request_id
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type = 2
ORDER BY quota DESC
LIMIT 50;


-- ---------------------------------------------------------------------
-- Q7. 【异常核查】单笔金额数量级分布：总额集中在哪个量级
-- ---------------------------------------------------------------------
SELECT
    CASE
        WHEN quota <= 0            THEN 'a. <=0'
        WHEN quota < 10000         THEN 'b. <1万 (<$0.02)'
        WHEN quota < 100000        THEN 'c. 1万-10万 (<$0.2)'
        WHEN quota < 1000000       THEN 'd. 10万-100万 (<$2)'
        WHEN quota < 10000000      THEN 'e. 100万-1000万 (<$20)'
        WHEN quota < 100000000     THEN 'f. 1000万-1亿 (<$200)'
        WHEN quota < 1000000000    THEN 'g. 1亿-10亿 (<$2000)'
        ELSE                            'h. >=10亿 (>=$2000)'
    END                                       AS bucket,
    COUNT(*)                                  AS log_count,
    SUM(quota)                                AS total_quota,
    ROUND(SUM(quota) / 500000.0, 2)           AS total_usd
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type = 2
GROUP BY 1
ORDER BY 1;


-- ---------------------------------------------------------------------
-- Q8. 【异常核查】同 request_id 重复计费
--     窗口内发生过结算超时 + 补单风暴，repair 重复结算会产生
--     同一 request_id 的多条消费日志——这是总额被放大的头号嫌疑。
-- ---------------------------------------------------------------------
SELECT request_id,
       COUNT(*)                             AS dup_count,
       SUM(quota)                           AS total_quota,
       ROUND(SUM(quota) / 500000.0, 2)      AS total_usd,
       MIN(user_id)                         AS user_id,
       MIN(model_name)                      AS model_name
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type = 2
  AND request_id <> ''
GROUP BY request_id
HAVING COUNT(*) > 1
ORDER BY SUM(quota) DESC
LIMIT 50;

-- Q8b. 重复计费的总体规模（多计部分 = 总额 - 每组保留一笔的金额，
--      这里先给出重复组的条数与总额，精确多计额待 Q8 人工确认口径）
SELECT COUNT(*)                        AS dup_groups,
       SUM(cnt)                        AS dup_logs,
       SUM(total_quota)                AS dup_total_quota,
       ROUND(SUM(total_quota) / 500000.0, 2) AS dup_total_usd
FROM (
    SELECT request_id, COUNT(*) AS cnt, SUM(quota) AS total_quota
    FROM logs
    WHERE created_at >= 1785488400
      AND created_at <  1785661200
      AND type = 2
      AND request_id <> ''
    GROUP BY request_id
    HAVING COUNT(*) > 1
) t;


-- ---------------------------------------------------------------------
-- Q9. 【异常核查】计费饱和标记：other.admin_info.quota_saturation
--     计费不变量被触发（clamp）的日志本身就是异常账，需单独处理。
-- ---------------------------------------------------------------------
SELECT id, user_id, model_name, quota,
       ROUND(quota / 500000.0, 2) AS usd, created_at, other
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type = 2
  AND other LIKE '%quota_saturation%'
ORDER BY quota DESC
LIMIT 50;


-- ---------------------------------------------------------------------
-- Q10. 【异常核查】基线对比：事故窗口前 48 小时（7/29 17:00 - 7/31 17:00）
--      正常流水量级是多少，窗口内总额是基线的几倍。
-- ---------------------------------------------------------------------
SELECT
    SUM(CASE WHEN type = 2 THEN quota ELSE 0 END)                    AS gross_consume_quota,
    ROUND(SUM(CASE WHEN type = 2 THEN quota ELSE 0 END) / 500000.0, 4) AS gross_usd,
    SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END)                        AS consume_log_count,
    COUNT(DISTINCT user_id)                                          AS active_users
FROM logs
WHERE created_at >= 1785315600
  AND created_at <  1785488400
  AND type IN (2, 6);


-- ---------------------------------------------------------------------
-- Q11. 【执行风险】净退额超过 int32 的用户
--      user.quota 列是 32 位整数，单用户净退额 > 2147483647
--      （≈$4294）时无法一次性入账，必须人工处理。
-- ---------------------------------------------------------------------
SELECT user_id,
       MAX(username)                                     AS username_snapshot,
       SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) AS net_refund_quota,
       ROUND(SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) / 500000.0, 2) AS net_refund_usd
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type IN (2, 6)
GROUP BY user_id
HAVING SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) > 2147483647
ORDER BY net_refund_quota DESC;


-- ---------------------------------------------------------------------
-- Q5. 用户状态核对（主库执行；主库与日志库同库时可直接联表）：
--     已注销（deleted_at 非空）/ 已封禁（status<>1）的用户需人工决定
--     是否补偿。软删除用户看不到余额，直接入账无意义。
-- ---------------------------------------------------------------------
SELECT
    l.user_id,
    u.username,
    u.status,                                   -- 1=enabled
    (u.deleted_at IS NOT NULL)                  AS is_deleted,
    SUM(CASE WHEN l.type = 2 THEN l.quota ELSE -l.quota END) AS net_refund_quota
FROM logs l
LEFT JOIN users u ON u.id = l.user_id
WHERE l.created_at >= 1785488400
  AND l.created_at <  1785661200
  AND l.type IN (2, 6)
GROUP BY l.user_id, u.username, u.status, u.deleted_at
HAVING SUM(CASE WHEN l.type = 2 THEN l.quota ELSE -l.quota END) > 0
   AND (u.id IS NULL OR u.status <> 1 OR u.deleted_at IS NOT NULL)
ORDER BY net_refund_quota DESC;
