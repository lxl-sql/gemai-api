-- =====================================================================
-- 2026-08-02 事故补偿 —— 批量退款【执行】脚本（PostgreSQL 15，生产）
-- 对应事故文档 §11.7 第 4 条后半句：带审计留痕执行
--
-- 口径（已与只读统计核对）：
--   窗口 [1785488400, 1785661200)，即 7/31 17:00 ~ 8/2 17:00 (UTC+8)
--   每用户应退 = 窗口内消费(type=2) - 窗口内已自动退款(type=6)，仅退净额>0 者
--   退入【赠送额度 gift_quota】，流水记入 quota_transactions：
--     type='refund'，gift_quota_delta=应退额，quota_delta=0，
--     reference_type='incident_compensation'，
--     reference_id='incident-20260802-bandwidth'，
--     idempotency_key='compensation:20260802:<user_id>'（唯一索引兜底，天然防重跑）
--
-- 执行方式：在 DB 宿主机 docker exec -it postgres psql -U root -d new-api
-- 逐段执行，每个【STOP】处人工核对输出后再继续。
-- =====================================================================


-- =====================================================================
-- 第 0 步：预检（全部只读，必须全部通过才能继续）
-- =====================================================================

-- 0.1 确认 users / quota_transactions 与 logs 同库，且幂等键唯一索引存在
\dt users
\dt quota_transactions
SELECT indexname, indexdef FROM pg_indexes
WHERE tablename = 'quota_transactions' AND indexdef LIKE '%idempotency_key%';
-- 【STOP】必须看到 idempotency_key 上的 UNIQUE 索引，否则不能继续（防重跑失效）。

-- 0.2 确认余额与流水列的实际类型
SELECT table_name, column_name, data_type
FROM information_schema.columns
WHERE (table_name = 'users' AND column_name IN ('quota', 'gift_quota'))
   OR (table_name = 'quota_transactions' AND column_name IN
       ('quota_delta','gift_quota_delta','balance_before','balance_after',
        'gift_balance_before','gift_balance_after','total_delta'));
-- 【STOP】若 users.quota / users.gift_quota 是 integer（int4）而不是 bigint：
--   先跑第 0.4 步的溢出名单；有超限用户则必须先
--   ALTER TABLE users ALTER COLUMN gift_quota TYPE bigint;（低峰执行）
--   或将这些用户单独人工处理，绝不能让 UPDATE 溢出报错中断批次。

-- 0.3 操作者 id（写入流水 operator_id，用你的管理员账号 id）
SELECT id, username, role FROM users WHERE role >= 10 ORDER BY id LIMIT 10;

-- 0.4 溢出预检：入账后 gift_quota 超过 int4 上限的用户（列是 bigint 则忽略）
SELECT u.id, u.username, u.gift_quota, t.refund_quota,
       u.gift_quota + t.refund_quota AS gift_after
FROM users u
JOIN (
    SELECT user_id, SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) AS refund_quota
    FROM logs
    WHERE created_at >= 1785488400 AND created_at < 1785661200 AND type IN (2, 6)
    GROUP BY user_id
    HAVING SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) > 0
) t ON t.user_id = u.id
WHERE u.gift_quota + t.refund_quota > 2147483647;


-- =====================================================================
-- 第 1 步：冻结退款清单（审计留痕的第一份记录，可重复执行）
-- =====================================================================

CREATE TABLE IF NOT EXISTS compensation_20260802 (
    user_id      int PRIMARY KEY,
    refund_quota bigint NOT NULL,
    status       text   NOT NULL DEFAULT 'pending',
    -- pending | credited | skipped_deleted | needs_review
    tx_id        int,
    updated_at   bigint
);

INSERT INTO compensation_20260802 (user_id, refund_quota)
SELECT user_id, SUM(CASE WHEN type = 2 THEN quota ELSE -quota END)
FROM logs
WHERE created_at >= 1785488400
  AND created_at <  1785661200
  AND type IN (2, 6)
GROUP BY user_id
HAVING SUM(CASE WHEN type = 2 THEN quota ELSE -quota END) > 0
ON CONFLICT (user_id) DO NOTHING;   -- 清单一经冻结不再变化，重跑安全

-- 核对冻结结果：总额应与只读统计基本一致
-- （允许比 Q1 net 略大：净额为负的用户被排除，其负额不再抵扣总盘）
SELECT COUNT(*)                          AS user_count,
       SUM(refund_quota)                 AS total_quota,
       ROUND(SUM(refund_quota) / 500000.0, 2) AS total_usd
FROM compensation_20260802;
-- 【STOP】user_count 应 ≈ 17956（少量净额≤0 用户被排除属正常），
--         total_usd 应 ≈ 5,415,089 + |负净额合计|。数字对不上就停下排查。


-- =====================================================================
-- 第 2 步：创建批量执行过程（分批提交 + 幂等 + 全程留痕）
-- =====================================================================

CREATE OR REPLACE PROCEDURE run_compensation_20260802(
    p_operator_id int,
    p_batch_size  int DEFAULT 500,
    p_sleep_sec   float DEFAULT 0.2
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_uids     int[];
    v_batch    int;
    v_total    int := 0;
    v_meta     text := '{"reason":"2026-08-02 bandwidth incident compensation",'
                    || '"window_start":1785488400,"window_end":1785661200,'
                    || '"policy":"full refund of window consumption as gift quota"}';
BEGIN
    LOOP
        SELECT array_agg(user_id) INTO v_uids FROM (
            SELECT user_id FROM compensation_20260802
            WHERE status = 'pending'
            ORDER BY user_id
            LIMIT p_batch_size
        ) t;
        EXIT WHEN v_uids IS NULL;

        -- (a) 已注销用户：跳过并标记，人工另行决定
        UPDATE compensation_20260802 c
        SET status = 'skipped_deleted', updated_at = extract(epoch FROM now())::bigint
        WHERE c.user_id = ANY (v_uids)
          AND c.status = 'pending'
          AND NOT EXISTS (SELECT 1 FROM users u
                          WHERE u.id = c.user_id AND u.deleted_at IS NULL);

        -- (b) 之前运行已入账但状态未更新的行（幂等键已存在）：补记状态
        UPDATE compensation_20260802 c
        SET status = 'credited', updated_at = extract(epoch FROM now())::bigint
        WHERE c.user_id = ANY (v_uids)
          AND c.status = 'pending'
          AND EXISTS (SELECT 1 FROM quota_transactions q
                      WHERE q.idempotency_key = 'compensation:20260802:' || c.user_id);

        -- (c) 核心：余额入账 + 流水写入 + 状态更新，单条语句原子完成。
        --     幂等键唯一索引兜底：并发/重跑冲突会让整条语句失败回滚，绝无双重入账。
        WITH todo AS (
            SELECT c.user_id, c.refund_quota,
                   'compensation:20260802:' || c.user_id AS ikey
            FROM compensation_20260802 c
            WHERE c.user_id = ANY (v_uids)
              AND c.status = 'pending'
        ),
        upd AS (
            UPDATE users u
            SET gift_quota = u.gift_quota + t.refund_quota
            FROM todo t
            WHERE u.id = t.user_id
              AND u.deleted_at IS NULL
            RETURNING u.id AS user_id, u.quota, u.gift_quota,
                      t.refund_quota, t.ikey
        ),
        ins AS (
            INSERT INTO quota_transactions
                (user_id, type, quota_delta, gift_quota_delta,
                 balance_before, gift_balance_before,
                 balance_after,  gift_balance_after,
                 total_delta, source, reference_type, reference_id,
                 request_id, idempotency_key, operator_id, metadata, created_at)
            SELECT user_id, 'refund', 0, refund_quota,
                   quota, gift_quota - refund_quota,
                   quota, gift_quota,
                   refund_quota, 'admin', 'incident_compensation',
                   'incident-20260802-bandwidth',
                   '', ikey, p_operator_id, v_meta,
                   extract(epoch FROM now())::bigint
            FROM upd
            RETURNING user_id, id
        )
        UPDATE compensation_20260802 c
        SET status = 'credited', tx_id = ins.id,
            updated_at = extract(epoch FROM now())::bigint
        FROM ins
        WHERE c.user_id = ins.user_id;

        GET DIAGNOSTICS v_batch = ROW_COUNT;
        v_total := v_total + v_batch;

        -- (d) 兜底防死循环：本轮仍为 pending 的行标记待人工复核
        UPDATE compensation_20260802
        SET status = 'needs_review', updated_at = extract(epoch FROM now())::bigint
        WHERE user_id = ANY (v_uids) AND status = 'pending';

        RAISE NOTICE 'batch done: % credited this batch, % total', v_batch, v_total;
        COMMIT;                       -- 每批独立提交，中断可随时重跑续作
        PERFORM pg_sleep(p_sleep_sec);
    END LOOP;
    RAISE NOTICE 'all done, total credited: %', v_total;
END;
$$;


-- =====================================================================
-- 第 3 步：小批量试跑 → 核对 → 全量执行
-- =====================================================================

-- 3.1 试跑一小批（先把 batch_size 当作总闸门：只处理前 20 人）
--     注意：把 1 换成第 0.3 步查到的管理员 id
-- CALL run_compensation_20260802(1, 20, 0.2);
-- ↑ 试跑后立刻 Ctrl+C 停掉？不需要——过程会继续跑完全部 pending。
--   正确做法：试跑前先临时限制清单，例如：
--     UPDATE compensation_20260802 SET status='hold' WHERE user_id NOT IN
--       (SELECT user_id FROM compensation_20260802 ORDER BY user_id LIMIT 20);
--   试跑核对无误后再放开：
--     UPDATE compensation_20260802 SET status='pending' WHERE status='hold';

-- 3.2 试跑核对（抽 3 个已入账用户逐字段核对）
-- SELECT q.user_id, q.type, q.gift_quota_delta,
--        q.gift_balance_before, q.gift_balance_after,
--        u.gift_quota AS current_gift_quota,
--        q.reference_type, q.reference_id, q.idempotency_key, q.created_at
-- FROM quota_transactions q JOIN users u ON u.id = q.user_id
-- WHERE q.reference_id = 'incident-20260802-bandwidth'
-- ORDER BY q.id LIMIT 10;
-- 【STOP】gift_balance_after 必须等于 users.gift_quota（若该用户无并发消费）；
--         再登录前台钱包流水页面确认展示正常，然后执行全量：
-- CALL run_compensation_20260802(1, 500, 0.2);


-- =====================================================================
-- 第 4 步：终验（全部只读）
-- =====================================================================

-- 4.1 状态汇总：credited 之外的行必须逐条人工解释
SELECT status, COUNT(*), SUM(refund_quota) AS quota,
       ROUND(SUM(refund_quota) / 500000.0, 2) AS usd
FROM compensation_20260802 GROUP BY status;

-- 4.2 流水与清单对账：两边总额必须完全相等
SELECT (SELECT COALESCE(SUM(gift_quota_delta), 0) FROM quota_transactions
        WHERE reference_id = 'incident-20260802-bandwidth')       AS ledger_total,
       (SELECT COALESCE(SUM(refund_quota), 0) FROM compensation_20260802
        WHERE status = 'credited')                                 AS list_total;

-- 4.3 绝无重复入账：每个用户至多一条补偿流水
SELECT user_id, COUNT(*) FROM quota_transactions
WHERE reference_id = 'incident-20260802-bandwidth'
GROUP BY user_id HAVING COUNT(*) > 1;
-- 必须 0 行。

-- =====================================================================
-- 第 5 步：清理 Redis 用户缓存（在宿主机 shell 执行，不在 psql 里）
-- 余额缓存键为 user:<id>，不清理则前台余额显示滞后。
-- =====================================================================
-- docker exec postgres psql -U root -d new-api -Atc \
--   "SELECT 'user:'||user_id FROM compensation_20260802 WHERE status='credited'" \
--   | xargs -n 200 redis-cli -h <redis地址> -a <密码> DEL
-- （Redis 若在本机 docker：xargs -n 200 docker exec -i <redis容器> redis-cli DEL）

-- =====================================================================
-- 附：回滚预案（仅在重大失误时使用，先与相关人确认再执行）
-- 依据流水精确反向扣减；用户若已花掉部分赠送额度，扣回会让 gift_quota
-- 变负——因此回滚必须逐用户 clamp，宁可少扣不可扣负：
--   UPDATE users u SET gift_quota = GREATEST(0, u.gift_quota - q.gift_quota_delta)
--   FROM quota_transactions q
--   WHERE q.user_id = u.id AND q.reference_id = 'incident-20260802-bandwidth';
-- 并写入反向 admin_adjust 流水留痕（勿直接删除补偿流水，流水只增不删）。
-- =====================================================================
