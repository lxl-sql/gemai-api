package model

import "errors"

// Common errors
var (
	ErrDatabase = errors.New("database error")
)

// User auth errors
var (
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrUserEmptyCredentials = errors.New("empty credentials")
	ErrEmailAlreadyTaken    = errors.New("email already taken")
	ErrEmailNotFound        = errors.New("email not found")
	ErrEmailAmbiguous       = errors.New("email matches multiple users")
)

// Token auth errors
var (
	ErrTokenNotProvided   = errors.New("token not provided")
	ErrTokenInvalid       = errors.New("token invalid")
	ErrTokenDisabled      = errors.New("token disabled")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenExhausted     = errors.New("token quota exhausted")
	ErrTokenLimitExceeded = errors.New("token limit exceeded")
	ErrTokenMutationRaced = errors.New("token changed concurrently")
)

// Redemption errors
var ErrRedeemFailed = errors.New("redeem.failed")

// 2FA errors
var ErrTwoFANotEnabled = errors.New("2fa not enabled")
