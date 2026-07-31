package middleware

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const tokenUsageSourceOutcomeContextKey = "token_usage_source_outcome"

// TokenUsageSource records one terminal outcome for an authenticated relay
// request. Controllers can override the HTTP-status fallback for streaming or
// protocol-specific responses with SetTokenUsageSourceOutcome.
func TokenUsageSource() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		userID := c.GetInt("id")
		tokenID := c.GetInt("token_id")
		if userID <= 0 || tokenID <= 0 || c.Request == nil {
			return
		}
		success := c.Writer.Status() >= http.StatusOK &&
			c.Writer.Status() < http.StatusBadRequest
		if outcome, exists := c.Get(tokenUsageSourceOutcomeContextKey); exists {
			if explicitSuccess, ok := outcome.(bool); ok {
				success = explicitSuccess
			}
		}
		occurredAt := tokenUsageSourceOccurredAt(c, success, time.Now().Unix())
		model.EnqueueTokenUsageSourceOutcome(model.TokenUsageSourceOutcome{
			UserID:     userID,
			TokenID:    tokenID,
			IP:         c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			OccurredAt: occurredAt,
			Success:    success,
		})
	}
}

func tokenUsageSourceOccurredAt(c *gin.Context, success bool, fallback int64) int64 {
	key := constant.ContextKeyTokenUsageSourceErrorAt
	if success {
		key = constant.ContextKeyTokenUsageSourceSuccessAt
	}
	if occurredAt, ok := common.GetContextKeyType[int64](c, key); ok && occurredAt > 0 {
		return occurredAt
	}
	return fallback
}

func SetTokenUsageSourceOutcome(c *gin.Context, success bool) {
	if c != nil {
		c.Set(tokenUsageSourceOutcomeContextKey, success)
	}
}
