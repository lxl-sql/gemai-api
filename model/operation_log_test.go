package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSanitizeOperationDetailRecursivelyMasksSensitiveValues(t *testing.T) {
	detail := map[string]interface{}{
		"name": "safe-name",
		"nested": map[string]interface{}{
			"api_key": "sk-secret",
			"items": []interface{}{
				map[string]interface{}{
					"client_secret": "client-secret",
					"value":         "safe-value",
				},
			},
		},
	}

	sanitized := sanitizeOperationDetail(detail)
	payload, err := common.Marshal(sanitized)
	require.NoError(t, err)
	payloadStr := string(payload)

	require.NotContains(t, payloadStr, "sk-secret")
	require.NotContains(t, payloadStr, "client-secret")
	require.Contains(t, payloadStr, `"api_key":"***"`)
	require.Contains(t, payloadStr, `"client_secret":"***"`)
	require.Contains(t, payloadStr, "safe-name")
	require.Contains(t, payloadStr, "safe-value")
}

func TestSanitizeOperationDetailKeepsNonSecretTokenMetadata(t *testing.T) {
	detail := map[string]interface{}{
		"token_ids":    []int{1, 2, 3},
		"token_name":   "daily-key",
		"access_token": "secret-token",
	}

	sanitized := sanitizeOperationDetail(detail)
	payload, err := common.Marshal(sanitized)
	require.NoError(t, err)
	payloadStr := string(payload)

	require.Contains(t, payloadStr, "daily-key")
	require.Contains(t, payloadStr, "token_ids")
	require.NotContains(t, payloadStr, "secret-token")
	require.Contains(t, payloadStr, `"access_token":"***"`)
}

func TestGetUserOperationLogsIncludesTargetedAccountEvents(t *testing.T) {
	truncateTables(t)
	require.NoError(t, LOG_DB.Create(&OperationLog{
		OperatorId:   9,
		OperatorName: "admin",
		OperatorRole: 100,
		Action:       OpActionPasskeyAdminRst,
		Category:     OpCategoryAuth,
		TargetType:   "user",
		TargetId:     "1",
		Success:      true,
		Detail:       `{"reason":"admin_reset"}`,
	}).Error)
	require.NoError(t, LOG_DB.Create(&OperationLog{
		OperatorId:   1,
		OperatorName: "user",
		Action:       OpActionTokenCreate,
		Category:     OpCategoryToken,
		TargetType:   "token",
		TargetId:     "10",
		Success:      true,
	}).Error)
	require.NoError(t, LOG_DB.Create(&OperationLog{
		OperatorId: 9,
		Action:     OpActionUserManage,
		Category:   OpCategoryUser,
		TargetType: "user",
		TargetId:   "2",
		Success:    true,
	}).Error)

	logs, total, err := GetUserOperationLogs(1, "", "", 0, 0, 0, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, logs, 2)

	joinedActions := logs[0].Action + "," + logs[1].Action
	require.Contains(t, joinedActions, OpActionPasskeyAdminRst)
	require.Contains(t, joinedActions, OpActionTokenCreate)
	require.False(t, strings.Contains(joinedActions, OpActionUserManage))
}
