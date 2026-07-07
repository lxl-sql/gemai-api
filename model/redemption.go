package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

type Redemption struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id"`
	Key          string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status       int            `json:"status" gorm:"default:1"`
	Name         string         `json:"name" gorm:"index"`
	Quota        int            `json:"quota" gorm:"default:0"`
	GiftQuota    int            `json:"gift_quota" gorm:"default:0;column:gift_quota"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime int64          `json:"redeemed_time" gorm:"bigint"`
	Count        int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId   int            `json:"used_user_id"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

type RedemptionRedeemResult struct {
	Quota         int `json:"quota"`
	GiftQuota     int `json:"gift_quota"`
	TotalQuota    int `json:"total_quota"`
	TransactionId int `json:"transaction_id,omitempty"`
	RedemptionId  int `json:"redemption_id"`
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func SearchRedemptions(keyword string, status string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{})

	if keyword != "" {
		if id, err := strconv.Atoi(keyword); err == nil {
			query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
		} else {
			query = query.Where("name LIKE ?", keyword+"%")
		}
	}

	if status != "" {
		now := common.GetTimestamp()
		switch status {
		case "expired":
			query = query.Where(
				"status = ? AND expired_time != 0 AND expired_time < ?",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusEnabled):
			query = query.Where(
				"status = ? AND (expired_time = 0 OR expired_time >= ?)",
				common.RedemptionCodeStatusEnabled,
				now,
			)
		case strconv.Itoa(common.RedemptionCodeStatusDisabled):
			query = query.Where("status = ?", common.RedemptionCodeStatusDisabled)
		case strconv.Itoa(common.RedemptionCodeStatusUsed):
			query = query.Where("status = ?", common.RedemptionCodeStatusUsed)
		}
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func Redeem(key string, userId int) (result *RedemptionRedeemResult, err error) {
	if key == "" {
		return nil, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return nil, errors.New("无效的 user id")
	}
	redemption := &Redemption{}
	var breakdown *QuotaBreakdown

	common.RandomSleep()
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where(commonKeyCol+" = ?", key).First(redemption).Error; err != nil {
			return errors.New("无效的兑换码")
		}
		now := common.GetTimestamp()
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < now {
			return errors.New("该兑换码已过期")
		}
		if redemption.Quota < 0 || redemption.GiftQuota < 0 || redemption.Quota+redemption.GiftQuota <= 0 {
			return errors.New("兑换码额度无效")
		}
		// Compare-and-swap on status: only the transaction that flips
		// enabled -> used may credit quota, so a concurrent redeem of the
		// same code loses here even without a row lock (e.g. on SQLite).
		claim := tx.Model(&Redemption{}).
			Where("id = ? AND status = ? AND (expired_time = 0 OR expired_time >= ?)", redemption.Id, common.RedemptionCodeStatusEnabled, now).
			Updates(map[string]interface{}{
				"redeemed_time": now,
				"status":        common.RedemptionCodeStatusUsed,
				"used_user_id":  userId,
			})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			return errors.New("该兑换码已被使用或已过期")
		}
		redemption.RedeemedTime = now
		redemption.Status = common.RedemptionCodeStatusUsed
		redemption.UsedUserId = userId
		breakdown, err = CreditQuotaBreakdownTx(tx, userId, redemption.Quota, redemption.GiftQuota, QuotaTransactionRef{
			Type:           QuotaTransactionTypeRedemption,
			Source:         "redemption",
			ReferenceType:  "redemption",
			ReferenceID:    strconv.Itoa(redemption.Id),
			IdempotencyKey: "redemption:" + strconv.Itoa(redemption.Id) + ":" + strconv.Itoa(userId),
			Metadata: map[string]interface{}{
				"quota":      redemption.Quota,
				"gift_quota": redemption.GiftQuota,
			},
		})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return nil, ErrRedeemFailed
	}
	if cacheErr := invalidateUserCache(userId); cacheErr != nil {
		common.SysLog("failed to invalidate user cache after redemption: " + cacheErr.Error())
	}
	transactionId := 0
	if breakdown != nil {
		transactionId = breakdown.TransactionID
	}
	result = &RedemptionRedeemResult{
		Quota:         redemption.Quota,
		GiftQuota:     redemption.GiftQuota,
		TotalQuota:    redemption.Quota + redemption.GiftQuota,
		TransactionId: transactionId,
		RedemptionId:  redemption.Id,
	}
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码到账：充值额度 %s，赠送额度 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), logger.LogQuota(redemption.GiftQuota), redemption.Id))
	return result, nil
}

func (redemption *Redemption) Insert() error {
	var err error
	err = DB.Create(redemption).Error
	return err
}

func InsertRedemptions(redemptions []Redemption) error {
	if len(redemptions) == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&redemptions).Error
	})
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	var err error
	err = DB.Model(redemption).Select("name", "status", "quota", "gift_quota", "redeemed_time", "expired_time").Updates(redemption).Error
	return err
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
