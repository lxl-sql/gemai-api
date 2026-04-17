package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

func TestKlingRequestConvert_InvalidJSONReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/kling", KlingRequestConvert(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/kling", io.NopCloser(strings.NewReader("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestKlingRequestConvert_RewritesBodyAndAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/kling", KlingRequestConvert(), func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		action, _ := c.Get("action")
		c.JSON(http.StatusOK, gin.H{
			"path":   c.Request.URL.Path,
			"body":   string(body),
			"action": action,
		})
	})

	reqBody := `{"model":"kling-v1","prompt":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/kling", io.NopCloser(strings.NewReader(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["path"] != "/v1/video/generations" {
		t.Fatalf("expected rewritten path, got %v", resp["path"])
	}
	if resp["action"] != constant.TaskActionTextGenerate {
		t.Fatalf("expected action %s, got %v", constant.TaskActionTextGenerate, resp["action"])
	}
}
