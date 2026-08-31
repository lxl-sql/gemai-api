package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrOAuthTokenClientUnavailable = errors.New("oauth token client is unavailable")
	ErrOAuthTokenUserUnavailable   = errors.New("oauth token user is unavailable")
)

type OAuthAuthorizationCodeExchangeResult struct {
	Grant                 *model.OAuthGrant
	User                  *model.User
	AccessToken           string
	AccessTokenExpiresIn  int
	RefreshToken          string
	RefreshTokenExpiresIn int
	Scope                 string
	RedirectURI           string
}

func ExchangeOAuthAuthorizationCode(
	ctx context.Context,
	code string,
	clientID string,
	redirectURI string,
	codeChallenge string,
	codeChallengeMethod string,
	now time.Time,
) (*OAuthAuthorizationCodeExchangeResult, error) {
	refreshToken, refreshExpiresIn, refreshExpiresAt, err := GenerateOAuthRefreshToken(now)
	if err != nil {
		return nil, err
	}
	result := &OAuthAuthorizationCodeExchangeResult{
		RefreshToken:          refreshToken,
		RefreshTokenExpiresIn: refreshExpiresIn,
	}
	err = model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		application := &model.OAuthApp{}
		if loadErr := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Select("id").
			Where("client_id = ? AND status = ?", clientID, common.UserStatusEnabled).
			First(application).Error; loadErr != nil {
			if errors.Is(loadErr, gorm.ErrRecordNotFound) {
				return ErrOAuthTokenClientUnavailable
			}
			return loadErr
		}
		authorizationCode := &model.OAuthAuthorizationCode{}
		if loadErr := tx.Where("code = ?", code).First(authorizationCode).Error; loadErr != nil {
			if errors.Is(loadErr, gorm.ErrRecordNotFound) {
				return model.ErrOAuthAuthorizationCodeInvalid
			}
			return loadErr
		}
		if authorizationCode.ClientId != clientID ||
			authorizationCode.RedirectUri != redirectURI ||
			authorizationCode.CodeChallenge != codeChallenge ||
			authorizationCode.CodeChallengeMethod != codeChallengeMethod {
			return model.ErrOAuthAuthorizationCodeInvalid
		}
		consumed, consumeErr := model.ConsumeOAuthAuthorizationCodeRecordTx(tx, authorizationCode, time.Now())
		if consumeErr != nil {
			return consumeErr
		}
		if !consumed {
			return model.ErrOAuthAuthorizationCodeInvalid
		}

		result.User = &model.User{}
		if userErr := tx.Select("id", "username", "role", "status").First(result.User, "id = ?", authorizationCode.UserId).Error; userErr != nil {
			if errors.Is(userErr, gorm.ErrRecordNotFound) {
				return ErrOAuthTokenUserUnavailable
			}
			return userErr
		}
		if result.User.Status != common.UserStatusEnabled {
			return ErrOAuthTokenUserUnavailable
		}

		result.Grant, consumeErr = model.UpsertOAuthGrantWithRefreshTokenTx(
			tx,
			authorizationCode.UserId,
			clientID,
			authorizationCode.Scope,
			refreshToken,
			refreshExpiresAt,
		)
		if consumeErr != nil {
			return consumeErr
		}
		result.AccessToken, result.AccessTokenExpiresIn, consumeErr = SignOAuthDelegatedAccessToken(
			authorizationCode.UserId,
			clientID,
			result.Grant.Id,
			result.Grant.AuthorizationVersion,
			authorizationCode.Scope,
			now,
		)
		return consumeErr
	})
	if err != nil {
		return nil, err
	}
	result.Scope = result.Grant.Scopes
	result.RedirectURI = redirectURI
	return result, nil
}

func GenerateOAuthRefreshToken(now time.Time) (string, int, time.Time, error) {
	refreshToken, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	expiresIn := common.OAuthDefaultRefreshTokenTTL
	expiresAt := now.Add(time.Duration(expiresIn) * time.Second)
	return refreshToken, expiresIn, expiresAt, nil
}

func SignOAuthDelegatedAccessToken(
	userID int,
	clientID string,
	grantID int,
	grantVersion int64,
	scope string,
	now time.Time,
) (string, int, error) {
	expiresIn := common.OAuthDefaultAccessTokenTTL
	jti, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		return "", 0, err
	}
	claims := jwt.MapClaims{
		"sub":           userID,
		"client_id":     clientID,
		"grant_id":      grantID,
		"grant_version": grantVersion,
		"aud":           clientID,
		"scope":         scope,
		"iat":           now.Unix(),
		"exp":           now.Add(time.Duration(expiresIn) * time.Second).Unix(),
		"iss":           common.OAuthTokenIssuerGemaiAPI,
		"jti":           jti,
		"typ":           common.OAuthAccessTokenType,
		"token_use":     common.OAuthTokenUseDelegatedAPI,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString([]byte(common.CryptoSecret))
	if err != nil {
		return "", 0, err
	}
	return accessToken, expiresIn, nil
}
