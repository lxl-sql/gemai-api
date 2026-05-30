package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// parseSuccessFilter 将查询参数 success 解析为 model 层的 successFilter：
// ""=全部(0)，"1"=仅成功(1)，"0"=仅失败(2)。
func parseSuccessFilter(c *gin.Context) int {
	switch c.Query("success") {
	case "1":
		return 1
	case "0":
		return 2
	default:
		return 0
	}
}

// GetAllOperationLogs 管理员查看全部操作审计日志。
func GetAllOperationLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	category := c.Query("category")
	action := c.Query("action")
	operatorName := c.Query("operator_name")
	targetType := c.Query("target_type")
	targetId := c.Query("target_id")
	ip := c.Query("ip")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	successFilter := parseSuccessFilter(c)

	logs, total, err := model.GetAllOperationLogs(category, action, operatorName, targetType, targetId, successFilter, startTimestamp, endTimestamp, ip, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

func redactSelfOperationLogs(logs []*model.OperationLog, userId int) {
	for _, logEntry := range logs {
		logEntry.Ip = ""
		logEntry.Detail = ""
		if logEntry.OperatorId != userId {
			logEntry.OperatorId = 0
			logEntry.OperatorName = ""
			logEntry.OperatorRole = 0
		}
	}
}

// GetSelfOperationLogs 普通用户查看自己的操作审计日志。
func GetSelfOperationLogs(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	category := c.Query("category")
	action := c.Query("action")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	successFilter := parseSuccessFilter(c)

	logs, total, err := model.GetUserOperationLogs(userId, category, action, successFilter, startTimestamp, endTimestamp, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redactSelfOperationLogs(logs, userId)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// DeleteHistoryOperationLogs 清理指定时间戳之前的操作审计日志。
func DeleteHistoryOperationLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}
	count, err := model.DeleteOldOperationLog(c.Request.Context(), targetTimestamp, 100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}
