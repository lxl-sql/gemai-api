package service

// FundingSource identifies the source selected for a billing reservation.
// Balance mutations are deliberately implemented in model/billing_reservation.go
// so they can share one database transaction with the durable reservation row.
type FundingSource interface {
	Source() string
}

type WalletFunding struct {
	consumed          int
	consumedQuota     int
	consumedGiftQuota int
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

type SubscriptionFunding struct {
	subscriptionId  int
	preConsumed     int64
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }
