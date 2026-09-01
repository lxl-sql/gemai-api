package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type requeueManualBillingSettlementRequest struct {
	RequestId           string `json:"request_id"`
	ExpectedActualQuota int    `json:"expected_actual_quota"`
	ConfirmDebt         bool   `json:"confirm_debt"`
}

func RequeueManualBillingSettlement(c *gin.Context) {
	var req requeueManualBillingSettlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request")
		return
	}
	req.RequestId = strings.TrimSpace(req.RequestId)
	if req.RequestId == "" || req.ExpectedActualQuota <= 0 || !req.ConfirmDebt {
		model.RecordOperationLog(c, model.OpActionBillingSettlementRequeue, "billing_reservation", req.RequestId, false, map[string]interface{}{
			"expected_actual_quota": req.ExpectedActualQuota,
			"confirm_debt":          req.ConfirmDebt,
			"reason":                "explicit request_id, expected_actual_quota and confirm_debt=true are required",
		})
		common.ApiErrorMsg(c, "request_id, expected_actual_quota and confirm_debt=true are required")
		return
	}

	result, err := service.RequeueManualBillingSettlement(req.RequestId, req.ExpectedActualQuota)
	if err != nil {
		model.RecordOperationLog(c, model.OpActionBillingSettlementRequeue, "billing_reservation", req.RequestId, false, map[string]interface{}{
			"expected_actual_quota": req.ExpectedActualQuota,
			"confirm_debt":          true,
			"reason":                err.Error(),
		})
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionBillingSettlementRequeue, "billing_reservation", req.RequestId, true, map[string]interface{}{
		"expected_actual_quota": req.ExpectedActualQuota,
		"previous_attempts":     result.PreviousAttempts,
		"failure_requeued":      result.FailureRequeued,
		"delta":                 result.Reservation.DesiredQuota - result.Reservation.ReservedQuota,
	})
	common.ApiSuccess(c, result)
}

func CreateLogCleanupSystemTask(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}

	task, err := service.StartLogCleanupTask(targetTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func GetCurrentSystemTask(c *gin.Context) {
	taskType := c.Query("type")
	if taskType == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "type is required",
		})
		return
	}

	task, err := model.GetActiveSystemTask(taskType)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    nil,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}

func ListSystemTasks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))

	tasks, err := model.ListSystemTasks(limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	responses := make([]model.SystemTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, task.ToResponse())
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responses,
	})
}

func GetSystemTask(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "task id is required",
		})
		return
	}

	task, err := model.GetSystemTaskByTaskID(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "task not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    task.ToResponse(),
	})
}
