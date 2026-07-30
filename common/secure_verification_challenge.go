package common

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

const SecureVerificationPurposeAPIKey = "api_key_security"

type SecureVerificationChallenge struct {
	UserId          int    `json:"user_id"`
	SecurityVersion int64  `json:"security_version"`
	Purpose         string `json:"purpose,omitempty"`
	ExpiresAt       int64  `json:"expires_at"`
	Nonce           string `json:"nonce"`
}

func GenerateSecureVerificationChallenge(userId int, securityVersion int64, purpose string, expiresAt int64) (string, error) {
	nonce, err := GenerateKey()
	if err != nil {
		return "", err
	}
	payload, err := Marshal(SecureVerificationChallenge{
		UserId:          userId,
		SecurityVersion: securityVersion,
		Purpose:         purpose,
		ExpiresAt:       expiresAt,
		Nonce:           nonce,
	})
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := GenerateHMACWithKey([]byte(SessionSecret), encodedPayload)
	return encodedPayload + "." + signature, nil
}

func ParseSecureVerificationChallenge(token string, now int64) (*SecureVerificationChallenge, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, errors.New("invalid secure verification challenge")
	}
	expectedSignature := GenerateHMACWithKey([]byte(SessionSecret), parts[0])
	if subtle.ConstantTimeCompare([]byte(expectedSignature), []byte(parts[1])) != 1 {
		return nil, errors.New("invalid secure verification challenge signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("invalid secure verification challenge payload")
	}
	challenge := &SecureVerificationChallenge{}
	if err := Unmarshal(payload, challenge); err != nil {
		return nil, errors.New("invalid secure verification challenge payload")
	}
	if challenge.UserId <= 0 || challenge.Nonce == "" || challenge.ExpiresAt <= now {
		return nil, errors.New("secure verification challenge expired or invalid")
	}
	return challenge, nil
}
