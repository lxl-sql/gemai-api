package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

func GetTokenUsageSources(c *gin.Context) {
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenID <= 0 {
		common.ApiError(c, errors.New("invalid token id"))
		return
	}
	userID := c.GetInt("id")
	if _, err := model.GetTokenByIds(tokenID, userID); err != nil {
		common.ApiError(c, err)
		return
	}

	page := 1
	if parsed, err := strconv.Atoi(c.DefaultQuery("p", "1")); err == nil && parsed > 0 {
		page = parsed
	}
	pageSize := 50
	if parsed, err := strconv.Atoi(c.DefaultQuery("page_size", "50")); err == nil && parsed > 0 {
		pageSize = parsed
	}
	if pageSize > 100 {
		pageSize = 100
	}

	result := &model.TokenUsageSourcePage{
		Items: make([]model.TokenUsageSource, 0),
	}
	available := system_setting.TokenUsageSourceUIEnabled()
	if available {
		result, err = model.GetDirectTokenUsageSourcePage(
			c.Request.Context(),
			userID,
			tokenID,
			(page-1)*pageSize,
			pageSize,
		)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, gin.H{
		"items":                   result.Items,
		"total":                   result.Total,
		"page":                    page,
		"page_size":               pageSize,
		"tracking_enabled":        result.TrackingEnabled,
		"tracking_start":          result.TrackingStart,
		"truncated":               result.Truncated,
		"available":               available,
		"counting_mode":           "direct_batch",
		"update_interval_seconds": model.TokenUsageSourceFlushIntervalSeconds(),
	})
}
