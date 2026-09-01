package service

import "github.com/QuantumNous/new-api/model"

// RequeueManualBillingSettlement only changes durable repair state. The normal
// repair task remains the single writer for the actual financial settlement.
func RequeueManualBillingSettlement(requestId string, expectedActualQuota int) (*model.BillingManualRequeueResult, error) {
	result, err := model.RequeueManualBillingReservation(requestId, expectedActualQuota)
	if err != nil {
		return nil, err
	}
	requestBillingSettlementRepair()
	return result, nil
}
