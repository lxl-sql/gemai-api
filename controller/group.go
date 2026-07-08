package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]interface{})
	userGroup := ""
	userId := c.GetInt("id")
	// 该处理器同时服务 /api/user/groups（匿名，无鉴权中间件，userId 恒为 0）
	// 与 /api/user/self/groups（登录，UserAuth）。匿名访问时不返回分组倍率(ratio)，
	// 避免未登录即可探测计费倍率等商业信息；分组名与描述仍返回，保证登录前页面可用。
	includeRatio := userId > 0
	var err error
	userGroup, err = model.GetUserGroup(userId, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to get user group",
		})
		return
	}
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			entry := map[string]interface{}{
				"desc": desc,
			}
			if includeRatio {
				entry["ratio"] = service.GetUserGroupRatio(userGroup, groupName)
			}
			usableGroups[groupName] = entry
		}
	}
	if _, ok := userUsableGroups["auto"]; ok {
		entry := map[string]interface{}{
			"desc": setting.GetUsableGroupDescription("auto"),
		}
		if includeRatio {
			entry["ratio"] = "自动"
		}
		usableGroups["auto"] = entry
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
