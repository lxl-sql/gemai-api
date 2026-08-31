package model

import (
	"context"
	"strconv"
)

func RecordOAuthTokenIssueOperationLog(
	ctx context.Context,
	user *User,
	app *OAuthApp,
	grant *OAuthGrant,
	scope string,
	redirectURI string,
	expiresIn int,
	requestID string,
	clientIP string,
	userAgent string,
) {
	if user == nil || app == nil || grant == nil {
		return
	}
	recordOperationLogRawContext(
		ctx,
		user.Id,
		user.Username,
		user.Role,
		clientIP,
		userAgent,
		OpActionOAuthTokenIssue,
		"oauth_app",
		strconv.Itoa(app.Id),
		true,
		map[string]interface{}{
			"app_id":         app.Id,
			"client_id":      app.ClientId,
			"app_name":       app.Name,
			"client_type":    app.EffectiveClientType(),
			"scope":          scope,
			"grant_id":       grant.Id,
			"expires_in":     expiresIn,
			"token_type":     "Bearer",
			"redirect_uri":   redirectURI,
			"request_id":     requestID,
			"execution_mode": "queued",
		},
	)
}
