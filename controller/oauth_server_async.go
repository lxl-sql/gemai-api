package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/oauthqueue"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func handleQueuedOAuthAuthorizationCode(
	c *gin.Context,
	app *model.OAuthApp,
	authorizationCode *model.OAuthAuthorizationCode,
) bool {
	config, enabled := service.OAuthExchangeQueueConfig()
	if !enabled {
		return false
	}
	asyncRequested, wait := oauthqueue.ParsePreferWait(c.GetHeader("Prefer"), config.SyncWaitMax)
	if !asyncRequested {
		return false
	}
	ticket, err := service.EnqueueOAuthAuthorizationCode(
		c.Request.Context(),
		app,
		authorizationCode,
		c.GetString(common.RequestIdKey),
		c.ClientIP(),
		c.Request.UserAgent(),
	)
	if err != nil {
		if ticket != nil {
			if ticket.EnqueueConfirmed && !ticket.Created {
				_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationToken, app.Id, authorizationCode.UserId)
			}
			writeOAuthExchangeAccepted(c, ticket, ticket.Status)
			return true
		}
		_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationToken, app.Id, authorizationCode.UserId)
		status := http.StatusServiceUnavailable
		errorCode := "temporarily_unavailable"
		if errors.Is(err, oauthqueue.ErrQueueFull) {
			status = http.StatusTooManyRequests
			errorCode = "queue_full"
			c.Header("Retry-After", "1")
		}
		c.JSON(status, gin.H{
			"error":         errorCode,
			"retryable":     true,
			"state_changed": false,
			"request_id":    c.GetString(common.RequestIdKey),
		})
		return true
	}
	if !ticket.Created {
		_ = service.RefundOAuthExchangeUser(context.Background(), service.OAuthUserOperationToken, app.Id, authorizationCode.UserId)
	}
	result, err := service.WaitOAuthExchangeResult(
		c.Request.Context(),
		ticket.ExchangeID,
		ticket.PollToken,
		wait,
	)
	if err != nil {
		writeOAuthExchangeAccepted(c, ticket, ticket.Status)
		return true
	}
	if result.Status == oauthqueue.StatusPending || result.Status == oauthqueue.StatusProcessing {
		writeOAuthExchangeAccepted(c, ticket, result.Status)
		return true
	}
	writeOAuthExchangeTokenResult(c, result)
	return true
}

func writeOAuthExchangeAccepted(c *gin.Context, ticket *service.OAuthExchangeTicket, status oauthqueue.Status) {
	retryAfter := 1
	if config, ok := service.OAuthExchangeQueueConfig(); ok {
		retryAfter = int(config.PollInterval/time.Second) + 1
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.JSON(http.StatusAccepted, gin.H{
		"status":      status,
		"exchange_id": ticket.ExchangeID,
		"status_url":  "/api/oauth-server/token-exchanges/" + ticket.ExchangeID,
		"poll_token":  ticket.PollToken,
		"retry_after": retryAfter,
		"expires_in":  ticket.ExpiresIn,
	})
}

func writeOAuthValidationAdmissionError(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	errorCode := "temporarily_unavailable"
	if errors.Is(err, oauthqueue.ErrQueueFull) {
		status = http.StatusTooManyRequests
		errorCode = "queue_full"
		c.Header("Retry-After", "1")
	}
	c.JSON(status, gin.H{
		"error":             errorCode,
		"error_description": "OAuth validation capacity is temporarily unavailable",
		"retryable":         true,
		"state_changed":     false,
		"request_id":        c.GetString(common.RequestIdKey),
	})
}

func OAuthTokenExchangeStatus(c *gin.Context) {
	exchangeID := strings.TrimSpace(c.Param("id"))
	pollToken := service.OAuthPollTokenFromAuthorizationHeader(c.GetHeader("Authorization"))
	if exchangeID == "" || len(exchangeID) > 128 || pollToken == "" || len(pollToken) > 256 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_token",
			"error_description": "exchange credentials are invalid",
		})
		return
	}
	result, err := service.GetOAuthExchangeResult(c.Request.Context(), exchangeID, pollToken)
	if err != nil {
		switch {
		case errors.Is(err, oauthqueue.ErrJobNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "exchange_not_found"})
		case errors.Is(err, oauthqueue.ErrPollTokenInvalid):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if result.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(result.RetryAfter))
	}
	writeOAuthExchangeResult(c, result)
}

func writeOAuthExchangeResult(c *gin.Context, result *service.OAuthExchangeResult) {
	switch result.Status {
	case oauthqueue.StatusSucceeded:
		c.JSON(http.StatusOK, gin.H{
			"status":                   result.Status,
			"access_token":             result.AccessToken,
			"token_type":               result.TokenType,
			"expires_in":               result.ExpiresIn,
			"scope":                    result.Scope,
			"refresh_token":            result.RefreshToken,
			"refresh_token_expires_in": result.RefreshTokenExpiresIn,
		})
	case oauthqueue.StatusFailed, oauthqueue.StatusUnknown, oauthqueue.StatusExpired:
		c.JSON(http.StatusOK, gin.H{
			"status":            result.Status,
			"error":             result.Error,
			"error_description": result.ErrorDescription,
			"reauthorize":       result.Reauthorize,
		})
	default:
		c.JSON(http.StatusOK, gin.H{
			"status":      result.Status,
			"retry_after": result.RetryAfter,
		})
	}
}

func writeOAuthExchangeTokenResult(c *gin.Context, result *service.OAuthExchangeResult) {
	if result.Status == oauthqueue.StatusSucceeded {
		c.JSON(http.StatusOK, gin.H{
			"access_token":             result.AccessToken,
			"token_type":               result.TokenType,
			"expires_in":               result.ExpiresIn,
			"scope":                    result.Scope,
			"refresh_token":            result.RefreshToken,
			"refresh_token_expires_in": result.RefreshTokenExpiresIn,
		})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{
		"error":             result.Error,
		"error_description": result.ErrorDescription,
		"reauthorize":       result.Reauthorize,
	})
}
