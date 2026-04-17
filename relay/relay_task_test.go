package relay

import (
	"strings"
	"testing"
)

func TestFormatRealtimeFetchFailReason(t *testing.T) {
	got := formatRealtimeFetchFailReason("parse_task_result_failed", "bad json")
	want := "realtime_fetch_failed:parse_task_result_failed (bad json)"
	if got != want {
		t.Fatalf("unexpected reason, want %q, got %q", want, got)
	}
}

func TestFormatRealtimeFetchFailReason_TrimLength(t *testing.T) {
	longDetail := strings.Repeat("x", 400)
	got := formatRealtimeFetchFailReason("fetch_upstream_failed", longDetail)
	if len(got) != 256 {
		t.Fatalf("expected trimmed length 256, got %d", len(got))
	}
	if !strings.HasPrefix(got, "realtime_fetch_failed:fetch_upstream_failed") {
		t.Fatalf("unexpected prefix: %q", got)
	}
}
