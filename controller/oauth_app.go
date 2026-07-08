package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func GetMyOAuthApps(c *gin.Context) {
	// 路由使用 AdminAuth，session 与 Bearer token 两种鉴权都会写入 c 的 "id"。
	// 此处必须用 c.GetInt("id")：若用 session.Get("id").(int)，管理员用 Access Token
	// 调用时 session 为空，裸类型断言会 panic 导致 500（DoS）。
	userId := c.GetInt("id")
	keyword := c.Query("keyword")

	apps, err := model.GetOAuthAppsByUserId(userId, keyword)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, apps)
}

func GetOAuthAppDetail(c *gin.Context) {
	userId := c.GetInt("id")

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	app, err := model.GetOAuthAppById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "app not found"})
		return
	}
	if app.UserId != userId {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "no permission"})
		return
	}

	common.ApiSuccess(c, app)
}

func CreateMyOAuthApp(c *gin.Context) {
	userId := c.GetInt("id")

	var req struct {
		Name         string   `json:"name" binding:"required"`
		Description  string   `json:"description"`
		Logo         string   `json:"logo"`
		RedirectUris []string `json:"redirect_uris" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request: " + err.Error()})
		return
	}
	name, description, logo, err := normalizeOAuthAppFields(req.Name, req.Description, req.Logo)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	clientIdSuffix, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	clientId := "gai_" + clientIdSuffix
	clientSecret, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	secretHash, err := common.Password2Hash(clientSecret)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	app := &model.OAuthApp{
		Name:             name,
		Description:      description,
		Logo:             logo,
		ClientId:         clientId,
		ClientSecretHash: secretHash,
		UserId:           userId,
		Status:           common.UserStatusEnabled,
	}
	if err := app.SetRedirectUris(req.RedirectUris); err != nil {
		common.ApiError(c, err)
		return
	}

	if err := model.CreateOAuthApp(app); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"id":            app.Id,
		"name":          app.Name,
		"client_id":     app.ClientId,
		"client_secret": clientSecret,
		"redirect_uris": req.RedirectUris,
		"created_at":    app.CreatedAt,
	})
}

func UpdateMyOAuthApp(c *gin.Context) {
	userId := c.GetInt("id")

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	app, err := model.GetOAuthAppById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "app not found"})
		return
	}
	if app.UserId != userId {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "no permission"})
		return
	}

	var req struct {
		Name         *string  `json:"name"`
		Description  *string  `json:"description"`
		Logo         *string  `json:"logo"`
		RedirectUris []string `json:"redirect_uris"`
		Status       *int     `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request"})
		return
	}

	if req.Name != nil {
		name, _, _, err := normalizeOAuthAppFields(*req.Name, "", "")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		app.Name = name
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if len(description) > 512 {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "description is too long"})
			return
		}
		app.Description = description
	}
	if req.Logo != nil {
		logo := strings.TrimSpace(*req.Logo)
		if err := validateOAuthAppLogo(logo); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		app.Logo = logo
	}
	if req.RedirectUris != nil {
		if err := app.SetRedirectUris(req.RedirectUris); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	if req.Status != nil {
		app.Status = *req.Status
	}

	if err := model.UpdateOAuthApp(app); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, app)
}

func DeleteMyOAuthApp(c *gin.Context) {
	userId := c.GetInt("id")

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	if err := model.DeleteOAuthApp(id, userId); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, nil)
}

func ResetOAuthAppSecret(c *gin.Context) {
	userId := c.GetInt("id")

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid id"})
		return
	}

	app, err := model.GetOAuthAppById(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "app not found"})
		return
	}
	if app.UserId != userId {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "no permission"})
		return
	}

	newSecret, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	secretHash, err := common.Password2Hash(newSecret)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	app.ClientSecretHash = secretHash
	if err := model.UpdateOAuthApp(app); err != nil {
		common.ApiError(c, err)
		return
	}

	common.ApiSuccess(c, gin.H{
		"client_secret": newSecret,
	})
}

func normalizeOAuthAppFields(name string, description string, logo string) (string, string, string, error) {
	normalizedName := strings.TrimSpace(name)
	if normalizedName == "" {
		return "", "", "", fmt.Errorf("name is required")
	}
	if len(normalizedName) > 128 {
		return "", "", "", fmt.Errorf("name is too long")
	}
	normalizedDescription := strings.TrimSpace(description)
	if len(normalizedDescription) > 512 {
		return "", "", "", fmt.Errorf("description is too long")
	}
	normalizedLogo := strings.TrimSpace(logo)
	if err := validateOAuthAppLogo(normalizedLogo); err != nil {
		return "", "", "", err
	}
	return normalizedName, normalizedDescription, normalizedLogo, nil
}

func validateOAuthAppLogo(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > 512 {
		return fmt.Errorf("logo URL is too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("logo URL is invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return fmt.Errorf("logo URL scheme must be http or https")
	}
	if parsed.User != nil {
		return fmt.Errorf("logo URL must not contain userinfo")
	}
	return nil
}
