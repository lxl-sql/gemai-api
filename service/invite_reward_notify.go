package service

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

type InviteRewardNotifyPayload struct {
	Type          string  `json:"type"`
	UserID        int     `json:"user_id"`
	TradeNo       string  `json:"trade_no"`
	Amount        int64   `json:"amount"`
	Money         float64 `json:"money"`
	QuotaAdded    int     `json:"quota_added"`
	PaymentMethod string  `json:"payment_method"`
	Timestamp     int64   `json:"timestamp"`
}

// AsyncNotifyInviteReward 异步发送邀请奖励通知到外部平台，不阻塞主流程。
// 任何错误仅记录日志，不影响充值业务。
func AsyncNotifyInviteReward(payload InviteRewardNotifyPayload) {
	notifyUrl := operation_setting.InviteRewardNotifyUrl
	notifySecret := operation_setting.InviteRewardNotifySecret
	if notifyUrl == "" || notifySecret == "" {
		return
	}

	if payload.Timestamp == 0 {
		payload.Timestamp = time.Now().Unix()
	}
	if payload.Type == "" {
		payload.Type = "payment"
	}

	go doNotifyInviteReward(notifyUrl, notifySecret, payload)
}

func doNotifyInviteReward(url, secret string, payload InviteRewardNotifyPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[InviteReward] 序列化通知payload失败: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[InviteReward] 创建HTTP请求失败: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-notify-secret", secret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[InviteReward] 发送通知失败 trade_no=%s: %v", payload.TradeNo, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("[InviteReward] 通知成功 trade_no=%s user_id=%d", payload.TradeNo, payload.UserID)
	} else {
		log.Printf("[InviteReward] 通知响应异常 trade_no=%s status=%d", payload.TradeNo, resp.StatusCode)
	}
}
