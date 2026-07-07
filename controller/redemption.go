package controller

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

func GetAllRedemptions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.GetAllRedemptions(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchRedemptions(c *gin.Context) {
	keyword := c.Query("keyword")
	status := c.Query("status")
	pageInfo := common.GetPageQuery(c)
	redemptions, total, err := model.SearchRedemptions(keyword, status, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(redemptions)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetRedemption(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	redemption, err := model.GetRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    redemption,
	})
	return
}

func AddRedemption(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorI18n(c, i18n.MsgPaymentComplianceRequired)
		return
	}

	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionNameLength)
		return
	}
	if redemption.Count <= 0 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionCountPositive)
		return
	}
	if redemption.Count > 100 {
		common.ApiErrorI18n(c, i18n.MsgRedemptionCountMax)
		return
	}
	if !isValidSingleRedemptionQuota(redemption.Quota, redemption.GiftQuota) {
		common.ApiErrorMsg(c, "兑换码只能选择一种额度类型，且额度必须大于 0")
		return
	}
	if valid, msg := validateExpiredTime(c, redemption.ExpiredTime); !valid {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
		return
	}
	keys := make([]string, 0, redemption.Count)
	redemptions := make([]model.Redemption, 0, redemption.Count)
	for i := 0; i < redemption.Count; i++ {
		key := common.GetUUID()
		redemptions = append(redemptions, model.Redemption{
			UserId:      c.GetInt("id"),
			Name:        redemption.Name,
			Key:         key,
			CreatedTime: common.GetTimestamp(),
			Quota:       redemption.Quota,
			GiftQuota:   redemption.GiftQuota,
			ExpiredTime: redemption.ExpiredTime,
		})
		keys = append(keys, key)
	}
	if err := model.InsertRedemptions(redemptions); err != nil {
		common.SysError("failed to insert redemptions: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgRedemptionCreateFailed),
			"data":    []string{},
		})
		return
	}
	model.RecordOperationLog(c, model.OpActionRedemptionCreate, "redemption", "", true, map[string]interface{}{
		"name":         redemption.Name,
		"quota":        redemption.Quota,
		"gift_quota":   redemption.GiftQuota,
		"total_quota":  redemption.Quota + redemption.GiftQuota,
		"count":        redemption.Count,
		"expired_time": redemption.ExpiredTime,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    keys,
	})
	return
}

func DeleteRedemption(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteRedemptionById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionRedemptionDelete, "redemption", strconv.Itoa(id), true, nil)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateRedemption(c *gin.Context) {
	statusOnly := c.Query("status_only")
	redemption := model.Redemption{}
	err := c.ShouldBindJSON(&redemption)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	cleanRedemption, err := model.GetRedemptionById(redemption.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if statusOnly == "" {
		if cleanRedemption.Status != common.RedemptionCodeStatusEnabled || isRedemptionExpired(cleanRedemption) {
			common.ApiErrorMsg(c, "仅未使用且未过期的兑换码允许编辑")
			return
		}
		if utf8.RuneCountInString(redemption.Name) == 0 || utf8.RuneCountInString(redemption.Name) > 20 {
			common.ApiErrorI18n(c, i18n.MsgRedemptionNameLength)
			return
		}
		if !isValidSingleRedemptionQuota(redemption.Quota, redemption.GiftQuota) {
			common.ApiErrorMsg(c, "兑换码只能选择一种额度类型，且额度必须大于 0")
			return
		}
		if valid, msg := validateExpiredTime(c, redemption.ExpiredTime); !valid {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": msg})
			return
		}
		// If you add more fields, please also update redemption.Update()
		cleanRedemption.Name = redemption.Name
		cleanRedemption.Quota = redemption.Quota
		cleanRedemption.GiftQuota = redemption.GiftQuota
		cleanRedemption.ExpiredTime = redemption.ExpiredTime
	}
	if statusOnly != "" {
		if !isValidRedemptionToggleStatus(redemption.Status) {
			common.ApiErrorMsg(c, "无效的兑换码状态")
			return
		}
		if cleanRedemption.Status == common.RedemptionCodeStatusUsed {
			common.ApiErrorMsg(c, "已使用的兑换码不能变更状态")
			return
		}
		if isRedemptionExpired(cleanRedemption) {
			common.ApiErrorMsg(c, "已过期的兑换码不能变更状态")
			return
		}
		cleanRedemption.Status = redemption.Status
	}
	err = cleanRedemption.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordOperationLog(c, model.OpActionRedemptionUpdate, "redemption", strconv.Itoa(cleanRedemption.Id), true, map[string]interface{}{
		"name":        cleanRedemption.Name,
		"quota":       cleanRedemption.Quota,
		"gift_quota":  cleanRedemption.GiftQuota,
		"total_quota": cleanRedemption.Quota + cleanRedemption.GiftQuota,
		"status":      cleanRedemption.Status,
		"status_only": statusOnly != "",
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanRedemption,
	})
	return
}

func DeleteInvalidRedemption(c *gin.Context) {
	rows, err := model.DeleteInvalidRedemptions()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
	return
}

func isValidSingleRedemptionQuota(quota int, giftQuota int) bool {
	if quota < 0 || giftQuota < 0 {
		return false
	}
	return (quota > 0) != (giftQuota > 0)
}

func isValidRedemptionToggleStatus(status int) bool {
	return status == common.RedemptionCodeStatusEnabled || status == common.RedemptionCodeStatusDisabled
}

func isRedemptionExpired(redemption *model.Redemption) bool {
	return redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp()
}

func validateExpiredTime(c *gin.Context, expired int64) (bool, string) {
	if expired != 0 && expired < common.GetTimestamp() {
		return false, i18n.T(c, i18n.MsgRedemptionExpireTimeInvalid)
	}
	return true, ""
}
