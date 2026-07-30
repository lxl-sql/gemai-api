package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestSecureVerificationAuditAction(t *testing.T) {
	assert.Equal(t, model.OpActionAPIKeySecurityVerification, secureVerificationAuditAction(common.SecureVerificationPurposeAPIKey))
	assert.Equal(t, model.OpActionSecureVerification, secureVerificationAuditAction(""))
	assert.Equal(t, model.OpActionSecureVerification, secureVerificationAuditAction("unknown"))
}
