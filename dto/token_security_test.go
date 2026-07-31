package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserTokenSecurityPolicyRejectsAdministratorFields(t *testing.T) {
	zero := 0
	zeroQuota := int64(0)
	disabled := false
	tests := []struct {
		name    string
		request *UserTokenSecurityPolicyRequest
		field   string
	}{
		{
			name:    "sustained rps",
			request: &UserTokenSecurityPolicyRequest{SustainedRps: &zero},
			field:   "sustained_rps",
		},
		{
			name:    "sustained rpm",
			request: &UserTokenSecurityPolicyRequest{SustainedRpm: &zero},
			field:   "sustained_rpm",
		},
		{
			name:    "shared user concurrency",
			request: &UserTokenSecurityPolicyRequest{UserMaxConcurrency: &zero},
			field:   "user_max_concurrency",
		},
		{
			name:    "shared user hourly quota",
			request: &UserTokenSecurityPolicyRequest{UserHourlyQuota: &zeroQuota},
			field:   "user_hourly_quota",
		},
		{
			name:    "fail closed",
			request: &UserTokenSecurityPolicyRequest{FailClosed: &disabled},
			field:   "fail_closed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.ValidateUserWritable()
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.field)
		})
	}
}

func TestUserTokenSecurityPolicyAcceptsUserFields(t *testing.T) {
	quota := int64(100)
	riskMode := "notify"
	request := &UserTokenSecurityPolicyRequest{
		MaxQuotaPerRequest: &quota,
		HourlyQuota:        &quota,
		DailyQuota:         &quota,
		RiskMode:           &riskMode,
	}

	require.NoError(t, request.ValidateUserWritable())
}
