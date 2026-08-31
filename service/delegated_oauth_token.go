package service

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/golang-jwt/jwt/v5"
)

type DelegatedOAuthClaims struct {
	UserID              int
	ClientID            string
	GrantID             int
	GrantVersion        int64
	GrantVersionPresent bool
	Scope               string
}

func ParseDelegatedOAuthAccessToken(tokenString string) (*DelegatedOAuthClaims, error) {
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return nil, errors.New("access token is empty")
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(common.OAuthTokenIssuerGemaiAPI),
		jwt.WithExpirationRequired(),
	)
	token, err := parser.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(common.CryptoSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("access token is invalid or expired")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("access token claims are invalid")
	}
	typ, _ := claims["typ"].(string)
	tokenUse, _ := claims["token_use"].(string)
	if typ != common.OAuthAccessTokenType || tokenUse != common.OAuthTokenUseDelegatedAPI {
		return nil, errors.New("access token type is invalid")
	}
	clientID, _ := claims["client_id"].(string)
	if clientID == "" || !audienceContains(claims["aud"], clientID) {
		return nil, errors.New("access token audience is invalid")
	}
	userID, err := positiveIntegerClaim(claims, "sub")
	if err != nil {
		return nil, err
	}
	grantID, err := positiveIntegerClaim(claims, "grant_id")
	if err != nil {
		return nil, err
	}
	grantVersion := int64(0)
	_, grantVersionPresent := claims["grant_version"]
	if grantVersionPresent {
		grantVersion, err = nonNegativeIntegerClaim(claims, "grant_version")
		if err != nil {
			return nil, err
		}
	}
	if jti, _ := claims["jti"].(string); jti == "" {
		return nil, errors.New("access token jti is missing")
	}
	if issuedAt, err := nonNegativeIntegerClaim(claims, "iat"); err != nil || issuedAt <= 0 {
		return nil, errors.New("access token issued-at time is invalid")
	}
	scope, _ := claims["scope"].(string)
	return &DelegatedOAuthClaims{
		UserID:              userID,
		ClientID:            clientID,
		GrantID:             grantID,
		GrantVersion:        grantVersion,
		GrantVersionPresent: grantVersionPresent,
		Scope:               scope,
	}, nil
}

// AcceptLegacyOAuthGrantTokens is a rolling-upgrade compatibility gate. Keep
// it enabled while old nodes can still issue tokens without grant_version;
// disable it after every node is upgraded and the legacy access-token TTL has
// elapsed.
func AcceptLegacyOAuthGrantTokens() bool {
	raw := strings.TrimSpace(os.Getenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS"))
	if raw == "" {
		return true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return value
}

func ValidateOAuthCompatibilityConfig() error {
	raw := strings.TrimSpace(os.Getenv("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS"))
	if raw == "" {
		return nil
	}
	if _, err := strconv.ParseBool(raw); err != nil {
		return fmt.Errorf("OAUTH_ACCEPT_LEGACY_GRANT_TOKENS must be true or false")
	}
	return nil
}

func positiveIntegerClaim(claims jwt.MapClaims, name string) (int, error) {
	value, err := nonNegativeIntegerClaim(claims, name)
	if err != nil || value <= 0 || value > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("access token %s is invalid", name)
	}
	return int(value), nil
}

func nonNegativeIntegerClaim(claims jwt.MapClaims, name string) (int64, error) {
	value, ok := claims[name].(float64)
	if !ok || value < 0 || value > math.MaxInt64 || value != math.Trunc(value) {
		return 0, fmt.Errorf("access token %s is invalid", name)
	}
	return int64(value), nil
}

func audienceContains(raw any, expected string) bool {
	switch audience := raw.(type) {
	case string:
		return audience == expected
	case []interface{}:
		for _, value := range audience {
			if text, ok := value.(string); ok && text == expected {
				return true
			}
		}
	case []string:
		for _, value := range audience {
			if value == expected {
				return true
			}
		}
	}
	return false
}
