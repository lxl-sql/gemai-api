package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func parseQuotaTransactionFilters(c *gin.Context) model.QuotaTransactionFilters {
	userId, _ := strconv.Atoi(c.Query("user_id"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	return model.QuotaTransactionFilters{
		UserId:        userId,
		Username:      c.Query("username"),
		Type:          c.Query("type"),
		Source:        c.Query("source"),
		ReferenceType: c.Query("reference_type"),
		ReferenceId:   c.Query("reference_id"),
		Direction:     c.Query("direction"),
		Bucket:        c.Query("bucket"),
		StartTime:     startTimestamp,
		EndTime:       endTimestamp,
	}
}

func GetAllQuotaTransactions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	transactions, total, err := model.GetQuotaTransactions(parseQuotaTransactionFilters(c), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(transactions)
	common.ApiSuccess(c, pageInfo)
}

func GetSelfQuotaTransactions(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filters := parseQuotaTransactionFilters(c)
	filters.UserId = c.GetInt("id")
	filters.Username = ""
	transactions, total, err := model.GetQuotaTransactions(filters, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(transactions)
	common.ApiSuccess(c, pageInfo)
}
