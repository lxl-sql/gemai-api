package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			sanitizeLoggedPath(param.Path),
		)
	}))
}

func sanitizeLoggedPath(path string) string {
	queryIndex := strings.IndexByte(path, '?')
	if queryIndex < 0 {
		return path
	}
	requestPath := path[:queryIndex]
	if strings.HasPrefix(requestPath, "/api/oauth-server/") ||
		strings.HasPrefix(requestPath, "/api/oauth/") ||
		strings.HasPrefix(requestPath, "/oauth/") {
		return requestPath + "?<redacted>"
	}

	queryParts := strings.Split(path[queryIndex+1:], "&")
	for index, queryPart := range queryParts {
		rawKey, _, hasValue := strings.Cut(queryPart, "=")
		key := rawKey
		if decodedKey, err := url.QueryUnescape(rawKey); err == nil {
			key = decodedKey
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "key", "api_key", "access_token", "token":
			if hasValue {
				queryParts[index] = rawKey + "=<redacted>"
			}
		}
	}
	return requestPath + "?" + strings.Join(queryParts, "&")
}
