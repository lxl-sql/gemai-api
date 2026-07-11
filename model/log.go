package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"gorm.io/gorm"
)

func applyExplicitLogTextFilter(tx *gorm.DB, column string, value string) (*gorm.DB, error) {
	if value == "" {
		return tx, nil
	}
	if strings.Contains(value, "%") {
		condition, pattern, err := buildLogLikeCondition(column, value)
		if err != nil {
			return nil, err
		}
		return tx.Where(condition, pattern), nil
	}
	return tx.Where(column+" = ?", value), nil
}

func buildLogLikeCondition(column string, value string) (string, string, error) {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		pattern, err := sanitizeClickHouseLikePattern(value)
		if err != nil {
			return "", "", err
		}
		return column + " LIKE ?", pattern, nil
	}

	pattern, err := sanitizeLikePattern(value)
	if err != nil {
		return "", "", err
	}
	return column + " LIKE ? ESCAPE '!'", pattern, nil
}

func sanitizeClickHouseLikePattern(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, `\\`)
	input = strings.ReplaceAll(input, `_`, `\_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}
	return input, nil
}

type Log struct {
	Id               int    `json:"id" gorm:"index:idx_created_at_id,priority:2;index:idx_user_id_id,priority:2"`
	UserId           int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt        int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:1;index:idx_created_at_type"`
	Type             int    `json:"type" gorm:"index:idx_created_at_type"`
	Content          string `json:"content"`
	Username         string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName        string `json:"token_name" gorm:"index;default:''"`
	ModelName        string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota            int    `json:"quota" gorm:"default:0"`
	PromptTokens     int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens int    `json:"completion_tokens" gorm:"default:0"`
	UseTime          int    `json:"use_time" gorm:"default:0"`
	IsStream         bool   `json:"is_stream"`
	ChannelId        int    `json:"channel" gorm:"index"`
	ChannelName      string `json:"channel_name" gorm:"->"`
	TokenId          int    `json:"token_id" gorm:"default:0;index"`
	Group            string `json:"group" gorm:"index"`
	Ip               string `json:"ip" gorm:"index;default:''"`
	// UserAgent / UpstreamRequestId 不加 gorm index 标签：logs 表数据量极大（10 亿级），
	// AutoMigrate 启动时用非 CONCURRENTLY 的 CREATE INDEX 会长时间锁表导致生产事故。
	// 如需索引，请在低峰期手动执行 CREATE INDEX CONCURRENTLY（见 docs/quota-wallet-split-plan.md 部署说明）。
	UserAgent         string `json:"user_agent" gorm:"type:varchar(512);default:''"`
	RequestId         string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);default:''"`
	Other             string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
	LogTypeLogin   = 7
)

func ensureLogRequestId(log *Log) {
	if log != nil && log.RequestId == "" {
		log.RequestId = common.NewRequestId()
	}
}

func createLog(log *Log) error {
	ensureLogRequestId(log)
	return LOG_DB.Create(log).Error
}

func clickHouseLogOrder(prefix string) string {
	return prefix + "created_at desc, " + prefix + "request_id desc"
}

func assignDisplayLogIds(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].Id = startIdx + i + 1
	}
}

func formatUserLogs(logs []*Log, startIdx int, showClientInfo bool) {
	for i := range logs {
		logs[i].ChannelName = ""
		if !showClientInfo {
			logs[i].Ip = ""
			logs[i].UserAgent = ""
		}
		var otherMap map[string]interface{}
		otherMap, _ = common.StrToMap(logs[i].Other)
		if otherMap != nil {
			// Remove admin-only debug fields.
			delete(otherMap, "admin_info")
			// Remove operation-audit details (operator/route info), admin-only.
			delete(otherMap, "audit_info")
			// Hide upstream-only model names from normal users. The requested
			// model remains visible through logs[i].ModelName.
			delete(otherMap, "upstream_model_name")
			// delete(otherMap, "reject_reason")
			if streamStatus, ok := otherMap["stream_status"].(map[string]interface{}); ok {
				// Keep user-facing stream state, but strip the verbose internal
				// error list. end_reason/end_error are enough to explain client
				// disconnects and stream failures in the dashboard.
				delete(streamStatus, "errors")
			}
		}
		logs[i].Other = common.MapToJsonStr(otherMap)
	}
	assignDisplayLogIds(logs, startIdx)
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	order := "id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order(order).Limit(common.MaxRecentItems).Find(&logs).Error
	showClientInfo := false
	if token, tokenErr := GetTokenById(tokenId); tokenErr == nil {
		showClientInfo = shouldShowClientLogInfo(token.UserId)
	}
	formatUserLogs(logs, 0, showClientInfo)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo 记录操作日志，并将管理员相关信息存入 Other.admin_info，
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if len(adminInfo) > 0 {
		other := map[string]interface{}{
			"admin_info": adminInfo,
		}
		log.Other = common.MapToJsonStr(other)
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

const logUserAgentMaxLength = 512

func shouldShowClientLogInfo(userId int) bool {
	settingMap, err := GetUserSetting(userId, false)
	return err == nil && settingMap.RecordIpLog
}

func normalizeLogUserAgent(userAgent string) string {
	userAgent = strings.Join(strings.Fields(userAgent), " ")
	if userAgent == "" {
		return ""
	}
	runes := []rune(userAgent)
	if len(runes) > logUserAgentMaxLength {
		return string(runes[:logUserAgentMaxLength])
	}
	return userAgent
}

func getLogClientInfo(c *gin.Context) (ip string, userAgent string) {
	if c == nil || c.Request == nil {
		return "", ""
	}
	return c.ClientIP(), normalizeLogUserAgent(c.Request.UserAgent())
}

// buildOpField 构建语言无关的操作描述（写入 Other.op）。
// 前端依据 action(稳定操作标识) + params(结构化参数) 在渲染期用 i18n 本地化展示，
// 因此不在数据库中存储自然语言句子。
func buildOpField(action string, params map[string]interface{}) map[string]interface{} {
	op := map[string]interface{}{
		"action": action,
	}
	if len(params) > 0 {
		op["params"] = params
	}
	return op
}

// RecordLoginLog 记录用户登录成功的审计日志（type=LogTypeLogin）。
// username 由调用方传入（登录流程已持有用户对象），避免额外的数据库查询。
// content 为英文兜底文本（用于导出/经典前端）；action+params 供前端本地化渲染。
// extra 可携带 login_method、user_agent 等附加信息（普通用户可见）。
func RecordLoginLog(userId int, username string, content string, ip string, action string, params map[string]interface{}, extra map[string]interface{}) {
	other := map[string]interface{}{}
	for k, v := range extra {
		other[k] = v
	}
	other["op"] = buildOpField(action, params)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeLogin,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record login log: " + err.Error())
	}
}

// RecordOperationAuditLog 记录管理/高危操作审计日志（type=LogTypeManage）。
// logUserId 为日志归属者，管理审计日志应归属实际操作者；目标资源/用户放入
// action params。username 内部按 logUserId 查询。content 为英文兜底文本（导出/经典前端用）。
// action+params 写入 Other.op，供前端本地化渲染（普通用户可见，不含敏感信息）。
// adminInfo 存放操作者身份（写入 Other.admin_info，普通用户查询时剥离）；
// auditInfo 存放路由/方法/结果等中间件兜底信息（写入 Other.audit_info，普通用户查询时剥离）。
func RecordOperationAuditLog(logUserId int, content string, ip string, action string, params map[string]interface{}, adminInfo map[string]interface{}, auditInfo map[string]interface{}) {
	username, _ := GetUsernameById(logUserId, false)
	other := map[string]interface{}{
		"op": buildOpField(action, params),
	}
	if len(adminInfo) > 0 {
		other["admin_info"] = adminInfo
	}
	if len(auditInfo) > 0 {
		other["audit_info"] = auditInfo
	}
	log := &Log{
		UserId:    logUserId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeManage,
		Content:   content,
		Ip:        ip,
		Other:     common.MapToJsonStr(other),
	}
	if err := createLog(log); err != nil {
		common.SysLog("failed to record operation audit log: " + err.Error())
	}
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, common.LocalLogPreview(content)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	otherStr := common.MapToJsonStr(other)
	clientIp, userAgent := getLogClientInfo(c)
	log := &Log{
		UserId:            userId,
		Username:          username,
		CreatedAt:         common.GetTimestamp(),
		Type:              LogTypeError,
		Content:           content,
		PromptTokens:      0,
		CompletionTokens:  0,
		TokenName:         tokenName,
		ModelName:         modelName,
		Quota:             0,
		ChannelId:         channelId,
		TokenId:           tokenId,
		UseTime:           useTimeSeconds,
		IsStream:          isStream,
		Group:             group,
		Ip:                clientIp,
		UserAgent:         userAgent,
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
}

type RecordConsumeLogParams struct {
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	Quota            int                    `json:"quota"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	Other            map[string]interface{} `json:"other"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	createdAt := common.GetTimestamp()
	otherStr := common.MapToJsonStr(params.Other)
	clientIp, userAgent := getLogClientInfo(c)
	log := &Log{
		UserId:            userId,
		Username:          username,
		CreatedAt:         createdAt,
		Type:              LogTypeConsume,
		Content:           params.Content,
		PromptTokens:      params.PromptTokens,
		CompletionTokens:  params.CompletionTokens,
		TokenName:         params.TokenName,
		ModelName:         params.ModelName,
		Quota:             params.Quota,
		ChannelId:         params.ChannelId,
		TokenId:           params.TokenId,
		UseTime:           params.UseTimeSeconds,
		IsStream:          params.IsStream,
		Group:             params.Group,
		Ip:                clientIp,
		UserAgent:         userAgent,
		RequestId:         requestId,
		UpstreamRequestId: upstreamRequestId,
		Other:             otherStr,
	}
	err := createLog(log)
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if common.DataExportEnabled {
		LogQuotaData(QuotaDataLogParams{
			UserID:    userId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			TokenUsed: params.PromptTokens + params.CompletionTokens,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  common.NodeName,
		})
	}
}

type RecordTaskBillingLogParams struct {
	UserId    int
	LogType   int
	Content   string
	ChannelId int
	ModelName string
	Quota     int
	TokenId   int
	Group     string
	Other     map[string]interface{}
	NodeName  string // 任务发起节点；为空时回退当前节点
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	createdAt := common.GetTimestamp()
	log := &Log{
		UserId:    params.UserId,
		Username:  username,
		CreatedAt: createdAt,
		Type:      params.LogType,
		Content:   params.Content,
		TokenName: tokenName,
		ModelName: params.ModelName,
		Quota:     params.Quota,
		ChannelId: params.ChannelId,
		TokenId:   params.TokenId,
		Group:     params.Group,
		Other:     common.MapToJsonStr(params.Other),
	}
	err := createLog(log)
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
	}
	if params.LogType == LogTypeConsume && common.DataExportEnabled {
		nodeName := params.NodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		LogQuotaData(QuotaDataLogParams{
			UserID:    params.UserId,
			Username:  username,
			ModelName: params.ModelName,
			Quota:     params.Quota,
			CreatedAt: createdAt,
			UseGroup:  params.Group,
			TokenID:   params.TokenId,
			ChannelID: params.ChannelId,
			NodeName:  nodeName,
		})
	}
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string, upstreamRequestId string, requestIp string, requestDomain string, content string, userAgent string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tx, err = applyExplicitLogTextFilter(tx, "logs.username", username); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp == 0 && requestId == "" && upstreamRequestId == "" {
		startTimestamp = defaultLogQueryStartTimestamp()
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	if requestIp != "" {
		tx = tx.Where("logs.other like ?", "%"+requestIp+"%")
	}
	if requestDomain != "" {
		tx = tx.Where("logs.other like ?", "%"+requestDomain+"%")
	}
	if userAgent != "" {
		if normalizedUserAgent := normalizeLogUserAgent(userAgent); normalizedUserAgent != "" {
			tx = tx.Where("logs.user_agent = ?", normalizedUserAgent)
		}
	}
	if content != "" {
		tx = tx.Where("logs.content like ?", "%"+content+"%")
	}
	total, err = countLogQueryWithLimit(tx, logSearchCountLimitValue())
	if err != nil {
		return nil, 0, err
	}
	order := "logs.created_at desc, logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		assignDisplayLogIds(logs, startIdx)
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

func defaultLogQueryStartTimestamp() int64 {
	days := common.GetEnvOrDefault("LOG_QUERY_DEFAULT_DAYS", 7)
	if days <= 0 {
		return 0
	}
	if days > 365 {
		days = 365
	}
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
}

func logSearchCountLimitValue() int {
	limit := common.GetEnvOrDefault("LOG_SEARCH_COUNT_LIMIT", logSearchCountLimit)
	if limit <= 0 {
		return logSearchCountLimit
	}
	if limit > 100000 {
		return 100000
	}
	return limit
}

func countLogQueryWithLimit(tx *gorm.DB, limit int) (int64, error) {
	if limit <= 0 {
		limit = logSearchCountLimitValue()
	}
	var ids []int
	err := tx.Session(&gorm.Session{}).Model(&Log{}).Select("logs.id").Limit(limit+1).Pluck("logs.id", &ids).Error
	if err != nil {
		return 0, err
	}
	total := int64(len(ids))
	if total > int64(limit) {
		total = int64(limit)
	}
	return total, nil
}

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	if tx, err = applyExplicitLogTextFilter(tx, "logs.model_name", modelName); err != nil {
		return nil, 0, err
	}
	if tokenName != "" {
		tx = tx.Where("logs.token_name = ?", tokenName)
	}
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}
	order := "logs.id desc"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("logs.")
	}
	err = tx.Order(order).Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}

	formatUserLogs(logs, startIdx, shouldShowClientLogInfo(userId))
	return logs, total, err
}

type Stat struct {
	Quota int64 `json:"quota"`
	Rpm   int64 `json:"rpm"`
	Tpm   int64 `json:"tpm"`
}

// 统计错误使用哨兵值，controller 据此映射为 i18n 消息返回给前端。
var (
	ErrLogStatInitializing     = errors.New("统计数据正在初始化，请稍后重试")
	ErrLogStatRangeUnavailable = errors.New("所选时间范围暂无统计数据")
	ErrLogStatLagging          = errors.New("统计任务暂时滞后，请稍后重试")
	ErrLogStatInvalidRange     = errors.New("无效的统计时间范围")
	ErrLogStatQueryFailed      = errors.New("查询统计数据失败")
	ErrLogStatDisabled         = errors.New("统计功能已被管理员停用")
)

func SumUsedQuota(ctx context.Context, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	now := time.Now().Unix()
	if startTimestamp == 0 {
		// 与列表接口一致的默认时间窗（LOG_QUERY_DEFAULT_DAYS，默认 7 天）。
		// 该配置为 0 时此处仍为 0，由下方覆盖下界兜底。
		startTimestamp = defaultLogQueryStartTimestamp()
	}
	// 未来的 end 直接钳到 now：既保持语义（未来没有日志），也消除超大
	// 时间戳在后续分钟取整时的整数溢出风险。
	if endTimestamp == 0 || endTimestamp > now {
		endTimestamp = now
	}
	if startTimestamp < 0 || endTimestamp < startTimestamp {
		return stat, ErrLogStatInvalidRange
	}

	// 实时聚合被显式停用时给出明确原因，而不是永远显示“初始化中”。
	// 该开关是紧急制动，不回退到危险的原始日志全量聚合。
	if !common.GetEnvOrDefaultBool("LOG_STAT_ROLLUP_ENABLED", true) {
		return stat, ErrLogStatDisabled
	}

	state, err := GetLogStatRollupState(ctx, LogStatRollupStateName)
	if err != nil {
		common.SysError("failed to query log stat rollup state: " + err.Error())
		return stat, ErrLogStatQueryFailed
	}
	if state == nil || state.Watermark == 0 {
		return stat, ErrLogStatInitializing
	}
	if state.CleanupPending {
		return stat, ErrLogStatLagging
	}
	useMinuteTotals := username == "" && tokenName == "" && modelName == "" && channel == 0 && group == ""
	queryState := state
	if useMinuteTotals {
		queryState, err = GetLogStatRollupState(ctx, LogStatMinuteTotalStateName)
		if err != nil {
			common.SysError("failed to query log stat minute total state: " + err.Error())
			return stat, ErrLogStatQueryFailed
		}
		if queryState == nil || queryState.Watermark == 0 {
			return stat, ErrLogStatInitializing
		}
	}
	// 门禁按查询区间判断：连续覆盖区间为 [max(cursor, coverage), watermark)。
	// 回填由近及远推进，最近的数据最先可查；历史区间不受水位停滞影响。
	coveredLowerBound := queryState.CoverageStart
	if queryState.BackfillCursor > coveredLowerBound {
		coveredLowerBound = queryState.BackfillCursor
	}
	// 仅在 LOG_QUERY_DEFAULT_DAYS<=0（列表禁用默认下界）时可达：统计端点
	// 必须有界，start=0 收敛为“当前已覆盖的全部聚合数据”，绝不回退到
	// 原始日志全表扫描。
	if startTimestamp == 0 {
		startTimestamp = coveredLowerBound
	}
	if endTimestamp < startTimestamp {
		return stat, ErrLogStatRangeUnavailable
	}
	if startTimestamp < coveredLowerBound {
		// 回填被停用时下界不会再前进，“初始化中”会误导用户永远等待，
		// 如实返回“该范围暂无统计数据”。
		backfillEnabled := common.GetEnvOrDefaultBool("LOG_STAT_BACKFILL_ENABLED", true)
		if backfillEnabled && queryState.BackfillCursor > queryState.CoverageStart {
			return stat, ErrLogStatInitializing
		}
		return stat, ErrLogStatRangeUnavailable
	}

	exclusiveEnd := endTimestamp + 1
	rollupStart := startTimestamp
	if remainder := rollupStart % 60; remainder != 0 {
		rollupStart += 60 - remainder
	}
	rollupEnd := exclusiveEnd - exclusiveEnd%60
	if rollupEnd > queryState.Watermark {
		rollupEnd = queryState.Watermark
	}

	if rollupStart < rollupEnd {
		var aggregate LogStatRollupAggregate
		var queryErr error
		if useMinuteTotals {
			aggregate, queryErr = QueryLogStatMinuteTotals(ctx, rollupStart, rollupEnd)
		} else {
			aggregate, queryErr = QueryLogStatRollups(ctx, LogStatRollupFilter{
				StartTimestamp: rollupStart,
				EndTimestamp:   rollupEnd,
				Username:       username,
				TokenName:      tokenName,
				ModelName:      modelName,
				ChannelID:      channel,
				Group:          group,
			})
		}
		if queryErr != nil {
			common.SysError("failed to query pre-aggregated log stat: " + queryErr.Error())
			return stat, ErrLogStatQueryFailed
		}
		stat.Quota = aggregate.Quota
	}

	// 仅首尾不足一分钟的碎片和水位之后的短尾部才读原始日志。
	rawRanges := [][2]int64{}
	if rollupStart < rollupEnd {
		if startTimestamp < rollupStart {
			rawRanges = append(rawRanges, [2]int64{startTimestamp, rollupStart})
		}
		if rollupEnd < exclusiveEnd {
			rawRanges = append(rawRanges, [2]int64{rollupEnd, exclusiveEnd})
		}
	} else {
		rawRanges = append(rawRanges, [2]int64{startTimestamp, exclusiveEnd})
	}
	var rawSeconds int64
	for _, rawRange := range rawRanges {
		rawSeconds += rawRange[1] - rawRange[0]
	}
	maxRawMinutes := common.GetEnvOrDefault("LOG_STAT_RAW_TAIL_MAX_MINUTES", 10)
	if maxRawMinutes < 1 {
		maxRawMinutes = 1
	}
	if rawSeconds > int64(maxRawMinutes)*60 {
		// 只有需要最新数据（水位之后）的查询才会走到这里；
		// 纯历史区间的碎片不超过两分钟，永远不会触发。
		return stat, ErrLogStatLagging
	}
	for _, rawRange := range rawRanges {
		aggregate, queryErr := queryRawLogStatAggregate(ctx, rawRange[0], rawRange[1], modelName, username, tokenName, channel, group)
		if queryErr != nil {
			common.SysError("failed to query raw log stat boundary: " + queryErr.Error())
			return stat, ErrLogStatQueryFailed
		}
		stat.Quota += aggregate.Quota
	}

	recent, err := queryRecentLogRateStat(ctx, modelName, username, tokenName, channel, group)
	if err != nil {
		// RPM/TPM 是瞬时速率指标，且额度已经算出：此处失败（常见于接口总超时
		// 预算被额度查询耗尽）时降级为 0 并告警，而不是让整个统计请求失败。
		// 缓存 10 秒后即自愈。
		common.SysError("failed to query rpm/tpm stat (degraded to zero): " + err.Error())
		return stat, nil
	}
	stat.Rpm = recent.RequestCount
	stat.Tpm = recent.TotalTokens()

	return stat, nil
}

func queryRawLogStatAggregate(ctx context.Context, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (aggregate LogStatRollupAggregate, err error) {
	if endTimestamp <= startTimestamp {
		return aggregate, nil
	}
	tx := LOG_DB.WithContext(ctx).Table("logs").Select(`COALESCE(SUM(quota), 0) quota,
		COUNT(*) request_count,
		COALESCE(SUM(prompt_tokens), 0) prompt_tokens,
		COALESCE(SUM(completion_tokens), 0) completion_tokens`).
		Where("created_at >= ? AND created_at < ? AND type = ?", startTimestamp, endTimestamp, LogTypeConsume)
	if tx, err = applyExplicitLogTextFilter(tx, "username", username); err != nil {
		return aggregate, err
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if tx, err = applyExplicitLogTextFilter(tx, "model_name", modelName); err != nil {
		return aggregate, err
	}
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
	}
	return aggregate, tx.Scan(&aggregate).Error
}

// recentLogRateCache 缓存最近 60 秒 RPM/TPM 的原始日志聚合结果，避免多用户
// 多标签页在大表上高频重复扫描热尾。TTL 短（10 秒），量级封顶后整体清空。
var (
	recentLogRateCacheMu  sync.Mutex
	recentLogRateCache    = make(map[string]recentLogRateCacheEntry)
	recentLogRateCacheTTL = int64(10)
	recentLogRateFlight   singleflight.Group
)

const recentLogRateCacheMaxEntries = 2048

type recentLogRateCacheEntry struct {
	aggregate LogStatRollupAggregate
	expiresAt int64
}

func queryRecentLogRateStat(ctx context.Context, modelName string, username string, tokenName string, channel int, group string) (LogStatRollupAggregate, error) {
	cacheKey := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", modelName, username, tokenName, channel, group)
	now := time.Now().Unix()

	recentLogRateCacheMu.Lock()
	if entry, ok := recentLogRateCache[cacheKey]; ok && entry.expiresAt > now {
		recentLogRateCacheMu.Unlock()
		return entry.aggregate, nil
	}
	recentLogRateCacheMu.Unlock()

	resultCh := recentLogRateFlight.DoChan(cacheKey, func() (interface{}, error) {
		// A concurrent leader may have populated the cache while this caller
		// waited to enter the singleflight group.
		queryNow := time.Now().Unix()
		recentLogRateCacheMu.Lock()
		if entry, ok := recentLogRateCache[cacheKey]; ok && entry.expiresAt > queryNow {
			recentLogRateCacheMu.Unlock()
			return entry.aggregate, nil
		}
		recentLogRateCacheMu.Unlock()

		// Do not bind the shared query to the first caller's cancellation;
		// every waiter observes its own ctx below. The detached query remains
		// bounded so it cannot outlive all callers indefinitely.
		queryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		aggregate, queryErr := queryRawLogStatAggregate(queryCtx, queryNow-60, queryNow+1, modelName, username, tokenName, channel, group)
		if queryErr != nil {
			return aggregate, queryErr
		}
		recentLogRateCacheMu.Lock()
		if len(recentLogRateCache) >= recentLogRateCacheMaxEntries {
			recentLogRateCache = make(map[string]recentLogRateCacheEntry)
		}
		recentLogRateCache[cacheKey] = recentLogRateCacheEntry{
			aggregate: aggregate,
			expiresAt: queryNow + recentLogRateCacheTTL,
		}
		recentLogRateCacheMu.Unlock()
		return aggregate, nil
	})
	select {
	case <-ctx.Done():
		return LogStatRollupAggregate{}, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return LogStatRollupAggregate{}, result.Err
		}
		return result.Val.(LogStatRollupAggregate), nil
	}
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("COALESCE(sum(prompt_tokens), 0) + COALESCE(sum(completion_tokens), 0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

// logDBUsingPostgreSQL 判断日志库是否为 PostgreSQL
// （未配置 LOG_SQL_DSN 时日志库即主库）。
func logDBUsingPostgreSQL() bool {
	return common.UsingLogDatabase(common.DatabaseTypePostgreSQL)
}

func CountOldLog(ctx context.Context, targetTimestamp int64) (int64, error) {
	var total int64
	if err := LOG_DB.WithContext(ctx).Model(&Log{}).Where("created_at < ?", targetTimestamp).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func DeleteOldLogBatch(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if nil != ctx.Err() {
		return 0, ctx.Err()
	}

	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// ClickHouse DELETE is a heavy mutation that rewrites data parts, so
		// per-batch mutations would be pathologically slow. Remove all matching
		// rows in a single synchronous mutation regardless of limit; the reported
		// count lets the caller's progress loop complete in one pass.
		total, err := CountOldLog(ctx, targetTimestamp)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, nil
		}
		if err := LOG_DB.WithContext(ctx).Exec(
			"ALTER TABLE logs DELETE WHERE created_at < ? SETTINGS mutations_sync = 1",
			targetTimestamp,
		).Error; err != nil {
			return 0, err
		}
		return total, nil
	}

	if logDBUsingPostgreSQL() {
		// PostgreSQL 不支持 DELETE ... LIMIT，用子查询分批删除
		result := LOG_DB.WithContext(ctx).Exec(
			"DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE created_at < ? LIMIT ?)",
			targetTimestamp, limit,
		)
		if nil != result.Error {
			return 0, result.Error
		}
		return result.RowsAffected, nil
	}

	result := LOG_DB.WithContext(ctx).Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
	if nil != result.Error {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func DeleteOldLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}

	var total int64 = 0

	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}

		rowsAffected, err := DeleteOldLogBatch(ctx, targetTimestamp, limit)
		if nil != err {
			return total, err
		}

		total += rowsAffected

		if rowsAffected < int64(limit) {
			break
		}
	}

	if err := ReconcileLogStatRollupsAfterLogCleanup(ctx, targetTimestamp); err != nil {
		return total, err
	}

	return total, nil
}
