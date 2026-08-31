package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type globalAPIRateLimitExemptRoute struct {
	method   string
	fullPath string
}

var globalAPIRateLimitExemptRoutes = map[globalAPIRateLimitExemptRoute]struct{}{
	{method: http.MethodPost, fullPath: "/api/oauth-server/token"}:   {},
	{method: http.MethodGet, fullPath: "/api/oauth-server/userinfo"}: {},
}

func isGlobalAPIRateLimitExempt(c *gin.Context) bool {
	_, found := globalAPIRateLimitExemptRoutes[globalAPIRateLimitExemptRoute{
		method:   c.Request.Method,
		fullPath: c.FullPath(),
	}]
	return found
}
