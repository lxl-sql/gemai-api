package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestRedactSelfOperationLogsHidesSensitiveFields(t *testing.T) {
	logs := []*model.OperationLog{
		{
			OperatorId:   1,
			OperatorName: "self",
			OperatorRole: 1,
			Ip:           "192.0.2.10",
			Detail:       `{"quota":100}`,
		},
		{
			OperatorId:   9,
			OperatorName: "admin",
			OperatorRole: 100,
			Ip:           "192.0.2.11",
			Detail:       `{"reason":"admin_reset"}`,
		},
	}

	redactSelfOperationLogs(logs, 1)

	if logs[0].Ip != "" || logs[0].Detail != "" {
		t.Fatalf("expected self log ip/detail to be redacted, got ip=%q detail=%q", logs[0].Ip, logs[0].Detail)
	}
	if logs[0].OperatorId != 1 || logs[0].OperatorName != "self" || logs[0].OperatorRole != 1 {
		t.Fatalf("expected self operator identity to be preserved, got %+v", logs[0])
	}

	if logs[1].Ip != "" || logs[1].Detail != "" {
		t.Fatalf("expected targeted log ip/detail to be redacted, got ip=%q detail=%q", logs[1].Ip, logs[1].Detail)
	}
	if logs[1].OperatorId != 0 || logs[1].OperatorName != "" || logs[1].OperatorRole != 0 {
		t.Fatalf("expected non-self operator identity to be hidden, got %+v", logs[1])
	}
}
