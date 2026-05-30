package model

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// OperationLog 操作审计日志。
// 记录用户/管理员的关键行为（登录、充值、增删改用户/令牌/渠道等），
// 与计费用途的 Log 表分离，便于独立的权限控制、查询与保留策略。
// 该表使用 LOG_DB（与 Log 一致），在 LOG_SQL_DSN 配置后会落在独立日志库。
type OperationLog struct {
	Id           int    `json:"id"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;index:idx_oplog_created_at"`
	OperatorId   int    `json:"operator_id" gorm:"index:idx_oplog_operator_id;default:0"`
	OperatorName string `json:"operator_name" gorm:"index:idx_oplog_operator_name;default:''"`
	OperatorRole int    `json:"operator_role" gorm:"default:0"`
	Action       string `json:"action" gorm:"type:varchar(64);index:idx_oplog_action;default:''"`
	Category     string `json:"category" gorm:"type:varchar(32);index:idx_oplog_category;default:''"`
	TargetType   string `json:"target_type" gorm:"type:varchar(32);default:''"`
	TargetId     string `json:"target_id" gorm:"type:varchar(64);index:idx_oplog_target_id;default:''"`
	Success      bool   `json:"success"`
	Ip           string `json:"ip" gorm:"type:varchar(64);index:idx_oplog_ip;default:''"`
	UserAgent    string `json:"user_agent" gorm:"type:varchar(512);default:''"`
	Detail       string `json:"detail" gorm:"type:text"`
}

// 操作大类（= action 的前缀），前端据此分组/筛选。
const (
	OpCategoryAuth       = "auth"
	OpCategoryUser       = "user"
	OpCategoryToken      = "token"
	OpCategoryFinance    = "finance"
	OpCategoryChannel    = "channel"
	OpCategoryRedemption = "redemption"
	OpCategorySystem     = "system"
)

// 操作类型（action），统一使用 "<category>.<action>" 风格的稳定字符串，
// 不要随意修改已发布的值，以免历史数据语义错乱。
const (
	// 认证与账户安全
	OpActionLogin            = "auth.login"
	OpActionLoginFailed      = "auth.login_failed"
	OpActionLogout           = "auth.logout"
	OpActionRegister         = "auth.register"
	OpActionPasswordReset    = "auth.password_reset"
	OpActionPasswordChange   = "auth.password_change"
	OpActionAccessTokenReset = "auth.access_token_reset"
	OpAction2FAEnable        = "auth.2fa_enable"
	OpAction2FADisable       = "auth.2fa_disable"
	OpAction2FABackupRegen   = "auth.2fa_backup_regenerate"
	OpActionPasskeyRegister  = "auth.passkey_register"
	OpActionPasskeyDelete    = "auth.passkey_delete"
	OpActionPasskeyAdminRst  = "auth.passkey_admin_reset"
	OpActionOAuthBind        = "auth.oauth_bind"
	OpActionOAuthUnbind      = "auth.oauth_unbind"
	OpActionEmailBind        = "auth.email_bind"

	// 用户管理
	OpActionUserCreate     = "user.create"
	OpActionUserUpdate     = "user.update"
	OpActionUserDelete     = "user.delete"
	OpActionUserSelfUpdate = "user.self_update"
	OpActionUserSelfDelete = "user.self_delete"
	OpActionUserManage     = "user.manage"

	// 令牌（API Key）
	OpActionTokenCreate      = "token.create"
	OpActionTokenUpdate      = "token.update"
	OpActionTokenDelete      = "token.delete"
	OpActionTokenDeleteBatch = "token.delete_batch"
	OpActionTokenViewKey     = "token.view_key"

	// 财务
	OpActionTopup              = "finance.topup"
	OpActionRedeem             = "finance.redeem"
	OpActionAffTransfer        = "finance.aff_transfer"
	OpActionAdminCompleteTopup = "finance.admin_complete_topup"

	// 渠道
	OpActionChannelCreate      = "channel.create"
	OpActionChannelUpdate      = "channel.update"
	OpActionChannelDelete      = "channel.delete"
	OpActionChannelDeleteBatch = "channel.delete_batch"
	OpActionChannelViewKey     = "channel.view_key"

	// 兑换码
	OpActionRedemptionCreate = "redemption.create"
	OpActionRedemptionUpdate = "redemption.update"
	OpActionRedemptionDelete = "redemption.delete"

	// 系统配置
	OpActionOptionUpdate = "system.option_update"
	OpActionModelCreate  = "system.model_create"
	OpActionModelUpdate  = "system.model_update"
	OpActionModelDelete  = "system.model_delete"
)

// categoryFromAction 从 action 推断大类（取第一个 "." 之前的部分）。
func categoryFromAction(action string) string {
	if idx := strings.Index(action, "."); idx > 0 {
		return action[:idx]
	}
	return action
}

// sensitiveDetailKeys 精确匹配需脱敏的字段名。
var sensitiveDetailKeys = map[string]struct{}{
	"password":        {},
	"old_password":    {},
	"new_password":    {},
	"key":             {},
	"secret":          {},
	"token":           {},
	"access_token":    {},
	"authorization":   {},
	"bearer":          {},
	"client_secret":   {},
	"api_key":         {},
	"access_key":      {},
	"private_key":     {},
	"private_key_pem": {},
}

func shouldMaskOperationDetailKey(key string) bool {
	lower := strings.ToLower(key)
	if _, ok := sensitiveDetailKeys[lower]; ok {
		return true
	}
	return strings.Contains(lower, "password") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "access_token") ||
		strings.Contains(lower, "refresh_token") ||
		strings.Contains(lower, "auth_token") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "access_key") ||
		strings.Contains(lower, "private_key")
}

// sanitizeOperationDetail 对 detail 做递归脱敏，避免明文密码/密钥落库。
// 注意：会就地修改传入的 map（调用方均传入临时 map）。
func sanitizeOperationDetailValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		for k, v := range typed {
			if shouldMaskOperationDetailKey(k) {
				typed[k] = "***"
				continue
			}
			typed[k] = sanitizeOperationDetailValue(v)
		}
	case []interface{}:
		for i, v := range typed {
			typed[i] = sanitizeOperationDetailValue(v)
		}
	case []map[string]interface{}:
		for i := range typed {
			typed[i] = sanitizeOperationDetailValue(typed[i]).(map[string]interface{})
		}
	}
	return value
}

func sanitizeOperationDetail(detail map[string]interface{}) map[string]interface{} {
	if len(detail) == 0 {
		return detail
	}
	return sanitizeOperationDetailValue(detail).(map[string]interface{})
}

// RecordOperationLog 记录一条操作审计日志，操作者身份从 gin.Context 自动提取
// （适用于已登录的请求：UserAuth/AdminAuth 之后）。
func RecordOperationLog(c *gin.Context, action string, targetType string, targetId string, success bool, detail map[string]interface{}) {
	operatorId, operatorName, operatorRole := 0, "", 0
	if c != nil {
		operatorId = c.GetInt("id")
		operatorName = c.GetString("username")
		operatorRole = c.GetInt("role")
	}
	recordOperationLog(c, operatorId, operatorName, operatorRole, action, targetType, targetId, success, detail)
}

// RecordOperationLogWithOperator 显式指定操作者身份记录审计日志，
// 适用于登录/注册/登出等 Context 中尚无身份信息的场景；IP/UA 仍从 Context 提取。
func RecordOperationLogWithOperator(c *gin.Context, operatorId int, operatorName string, operatorRole int, action string, targetType string, targetId string, success bool, detail map[string]interface{}) {
	recordOperationLog(c, operatorId, operatorName, operatorRole, action, targetType, targetId, success, detail)
}

// RecordTopupOperationLog 记录在线充值成功的审计日志。
// 适用于支付回调（Stripe/Creem/Waffo 等）等无 gin.Context 的服务端场景，
// 操作者以充值用户身份记录，IP 取支付回调来源 IP。
func RecordTopupOperationLog(userId int, tradeNo string, money float64, quota int, paymentMethod string, provider string, callerIp string) {
	recordOperationLogRaw(userId, "", 0, callerIp, "", OpActionTopup, "topup", tradeNo, true, map[string]interface{}{
		"money":          money,
		"quota":          quota,
		"payment_method": paymentMethod,
		"provider":       provider,
	})
}

func recordOperationLog(c *gin.Context, operatorId int, operatorName string, operatorRole int, action string, targetType string, targetId string, success bool, detail map[string]interface{}) {
	ip, userAgent := "", ""
	if c != nil && c.Request != nil {
		ip = c.ClientIP()
		userAgent = c.Request.UserAgent()
		if len(userAgent) > 512 {
			userAgent = userAgent[:512]
		}
	}
	recordOperationLogRaw(operatorId, operatorName, operatorRole, ip, userAgent, action, targetType, targetId, success, detail)
}

func recordOperationLogRaw(operatorId int, operatorName string, operatorRole int, ip string, userAgent string, action string, targetType string, targetId string, success bool, detail map[string]interface{}) {
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	detailStr := ""
	if len(detail) > 0 {
		detailBytes, err := common.Marshal(sanitizeOperationDetail(detail))
		if err != nil {
			common.SysLog("failed to marshal operation log detail: " + err.Error())
		} else {
			detailStr = string(detailBytes)
		}
	}
	logEntry := &OperationLog{
		CreatedAt:    common.GetTimestamp(),
		OperatorId:   operatorId,
		OperatorName: operatorName,
		OperatorRole: operatorRole,
		Action:       action,
		Category:     categoryFromAction(action),
		TargetType:   targetType,
		TargetId:     targetId,
		Success:      success,
		Ip:           ip,
		UserAgent:    userAgent,
		Detail:       detailStr,
	}
	if err := LOG_DB.Select(
		"CreatedAt",
		"OperatorId",
		"OperatorName",
		"OperatorRole",
		"Action",
		"Category",
		"TargetType",
		"TargetId",
		"Success",
		"Ip",
		"UserAgent",
		"Detail",
	).Create(logEntry).Error; err != nil {
		common.SysLog("failed to record operation log: " + err.Error())
	}
}

// successFilter 取值：0 = 全部，1 = 仅成功，2 = 仅失败。
func applyOperationLogSuccessFilter(tx *gorm.DB, successFilter int) *gorm.DB {
	switch successFilter {
	case 1:
		return tx.Where("success = ?", true)
	case 2:
		return tx.Where("success = ?", false)
	default:
		return tx
	}
}

func GetAllOperationLogs(category string, action string, operatorName string, targetType string, targetId string, successFilter int, startTimestamp int64, endTimestamp int64, ip string, startIdx int, num int) (logs []*OperationLog, total int64, err error) {
	tx := LOG_DB.Model(&OperationLog{})
	if category != "" {
		tx = tx.Where("category = ?", category)
	}
	if action != "" {
		tx = tx.Where("action = ?", action)
	}
	if operatorName != "" {
		tx = tx.Where("operator_name = ?", operatorName)
	}
	if targetType != "" {
		tx = tx.Where("target_type = ?", targetType)
	}
	if targetId != "" {
		tx = tx.Where("target_id = ?", targetId)
	}
	tx = applyOperationLogSuccessFilter(tx, successFilter)
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if ip != "" {
		tx = tx.Where("ip = ?", ip)
	}
	if err = tx.Count(&total).Error; err != nil {
		common.SysError("failed to count operation logs: " + err.Error())
		return nil, 0, errors.New("查询操作日志失败")
	}
	if err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&logs).Error; err != nil {
		common.SysError("failed to search operation logs: " + err.Error())
		return nil, 0, errors.New("查询操作日志失败")
	}
	return logs, total, nil
}

func GetUserOperationLogs(operatorId int, category string, action string, successFilter int, startTimestamp int64, endTimestamp int64, startIdx int, num int) (logs []*OperationLog, total int64, err error) {
	targetId := strconv.Itoa(operatorId)
	tx := LOG_DB.Model(&OperationLog{}).Where("operator_id = ? OR (target_type = ? AND target_id = ?)", operatorId, "user", targetId)
	if category != "" {
		tx = tx.Where("category = ?", category)
	}
	if action != "" {
		tx = tx.Where("action = ?", action)
	}
	tx = applyOperationLogSuccessFilter(tx, successFilter)
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if err = tx.Count(&total).Error; err != nil {
		common.SysError("failed to count user operation logs: " + err.Error())
		return nil, 0, errors.New("查询操作日志失败")
	}
	if err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&logs).Error; err != nil {
		common.SysError("failed to search user operation logs: " + err.Error())
		return nil, 0, errors.New("查询操作日志失败")
	}
	return logs, total, nil
}

func DeleteOldOperationLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	var total int64 = 0
	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}
		result := LOG_DB.Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&OperationLog{})
		if nil != result.Error {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(limit) {
			break
		}
	}
	return total, nil
}
