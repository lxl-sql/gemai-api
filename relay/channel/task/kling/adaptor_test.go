package kling

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestParseStatusCodeFromKlingMessage(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    int
	}{
		{
			name:    "equal format",
			message: "status_code=429, No capacity available",
			want:    http.StatusTooManyRequests,
		},
		{
			name:    "colon format with spaces",
			message: "status_code : 429 temporary overloaded",
			want:    http.StatusTooManyRequests,
		},
		{
			name:    "missing status code",
			message: "request failed",
			want:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStatusCodeFromKlingMessage(tc.message)
			if got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestDoResponse_Uses429FromKlingMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	adaptor := &TaskAdaptor{}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(
			`{"code":1001,"message":"status_code=429, No capacity available for this model"}`,
		)),
	}

	_, _, taskErr := adaptor.DoResponse(c, resp, &relaycommon.RelayInfo{})
	if taskErr == nil {
		t.Fatal("expected taskErr, got nil")
	}
	if taskErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", taskErr.StatusCode)
	}
	if taskErr.LocalError {
		t.Fatal("expected non-local error for parsed 429")
	}
}
