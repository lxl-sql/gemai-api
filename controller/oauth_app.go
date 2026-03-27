package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func GetMyOAuthApps(c *gin.Context) {
	session := sessions.Default(c)
	userId := session.Get("id").(int)
	keyword := c.Query("keyword")

	apps, err := model.GetOAuthAppsByUserId(userId, keyword)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, apps)
}

func GetOAuthAppDetail(c *gin.Context) {
	session := sessions.Default(c)
	userId := session.Get("id").(int)

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
	session := sessions.Default(c)
	userId := session.Get("id").(int)

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

	clientId := "gai_" + common.GetRandomString(32)
	clientSecret := common.GetRandomString(48)
	secretHash, err := common.Password2Hash(clientSecret)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	app := &model.OAuthApp{
		Name:             req.Name,
		Description:      req.Description,
		Logo:             req.Logo,
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
	session := sessions.Default(c)
	userId := session.Get("id").(int)

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
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Logo         string   `json:"logo"`
		RedirectUris []string `json:"redirect_uris"`
		Status       *int     `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request"})
		return
	}

	if req.Name != "" {
		app.Name = req.Name
	}
	if req.Description != "" {
		app.Description = req.Description
	}
	if req.Logo != "" {
		app.Logo = req.Logo
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
	session := sessions.Default(c)
	userId := session.Get("id").(int)

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
	session := sessions.Default(c)
	userId := session.Get("id").(int)

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

	newSecret := common.GetRandomString(48)
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
