package dto

import "fmt"

// UserTokenSecurityPolicyRequest is the user-writable subset of a token
// security policy. Administrator-managed fields remain present only so direct
// API callers receive an explicit error instead of having those fields ignored.
type UserTokenSecurityPolicyRequest struct {
	MaxQuotaPerRequest *int64  `json:"max_quota_per_request,omitempty"`
	HourlyQuota        *int64  `json:"hourly_quota,omitempty"`
	DailyQuota         *int64  `json:"daily_quota,omitempty"`
	RiskMode           *string `json:"risk_mode,omitempty"`

	SustainedRps        *int  `json:"sustained_rps,omitempty"`
	BurstCapacity       *int  `json:"burst_capacity,omitempty"`
	MaxConcurrency      *int  `json:"max_concurrency,omitempty"`
	MaxDistinctModels5m *int  `json:"max_distinct_models_5m,omitempty"`
	FailClosed          *bool `json:"fail_closed,omitempty"`
}

func (request *UserTokenSecurityPolicyRequest) ValidateUserWritable() error {
	if request == nil {
		return nil
	}
	administratorFields := []struct {
		name    string
		present bool
	}{
		{name: "sustained_rps", present: request.SustainedRps != nil},
		{name: "burst_capacity", present: request.BurstCapacity != nil},
		{name: "max_concurrency", present: request.MaxConcurrency != nil},
		{name: "max_distinct_models_5m", present: request.MaxDistinctModels5m != nil},
		{name: "fail_closed", present: request.FailClosed != nil},
	}
	for _, field := range administratorFields {
		if field.present {
			return fmt.Errorf("token security field %s is managed by an administrator", field.name)
		}
	}
	return nil
}
