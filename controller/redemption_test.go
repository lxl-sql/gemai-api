package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func TestIsValidSingleRedemptionQuota(t *testing.T) {
	tests := []struct {
		name      string
		quota     int
		giftQuota int
		want      bool
	}{
		{name: "recharge only", quota: 100, giftQuota: 0, want: true},
		{name: "gift only", quota: 0, giftQuota: 100, want: true},
		{name: "mixed", quota: 100, giftQuota: 100, want: false},
		{name: "zero", quota: 0, giftQuota: 0, want: false},
		{name: "negative recharge", quota: -1, giftQuota: 0, want: false},
		{name: "negative gift", quota: 0, giftQuota: -1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidSingleRedemptionQuota(tt.quota, tt.giftQuota); got != tt.want {
				t.Fatalf("isValidSingleRedemptionQuota(%d, %d) = %v, want %v", tt.quota, tt.giftQuota, got, tt.want)
			}
		})
	}
}

func TestIsValidRedemptionToggleStatus(t *testing.T) {
	if !isValidRedemptionToggleStatus(common.RedemptionCodeStatusEnabled) {
		t.Fatal("enabled status should be toggleable")
	}
	if !isValidRedemptionToggleStatus(common.RedemptionCodeStatusDisabled) {
		t.Fatal("disabled status should be toggleable")
	}
	if isValidRedemptionToggleStatus(common.RedemptionCodeStatusUsed) {
		t.Fatal("used status must not be toggleable")
	}
	if isValidRedemptionToggleStatus(0) {
		t.Fatal("zero status must not be toggleable")
	}
}

func TestIsRedemptionExpired(t *testing.T) {
	now := common.GetTimestamp()
	if isRedemptionExpired(&model.Redemption{ExpiredTime: 0}) {
		t.Fatal("zero expiration should never expire")
	}
	if !isRedemptionExpired(&model.Redemption{ExpiredTime: now - 1}) {
		t.Fatal("past expiration should expire")
	}
	if isRedemptionExpired(&model.Redemption{ExpiredTime: now + 60}) {
		t.Fatal("future expiration should not expire")
	}
}
