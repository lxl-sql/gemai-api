package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHasUnsupportedLogStatFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, key := range unsupportedLogStatFilters {
		t.Run(key, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/log/stat?"+key+"=value", nil)
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = request
			assert.True(t, hasUnsupportedLogStatFilter(context))
		})
	}

	request := httptest.NewRequest(
		"GET",
		"/api/log/stat?model_name=gpt-5&username=alice&request_id=",
		nil,
	)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	assert.False(t, hasUnsupportedLogStatFilter(context))

	request = httptest.NewRequest(
		"GET",
		"/api/log/stat?request_id=&request_id=req-duplicate",
		nil,
	)
	context, _ = gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	assert.True(t, hasUnsupportedLogStatFilter(context))
}

func TestGetLogsStatRejectsUnsupportedFilterBeforeDatabaseQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		"GET",
		"/api/log/stat?request_ip=203.0.113.10",
		nil,
	)

	GetLogsStat(context)

	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), `"message":`)
}
