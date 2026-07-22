package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDoMidjourneyHTTPRequestInheritsClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if GetHttpClient() == nil {
		InitHttpClient()
	}

	upstreamStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(upstreamStarted)
		<-releaseUpstream
	}))
	defer server.Close()
	defer close(releaseUpstream)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(requestContext, http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	requestDone := make(chan error, 1)

	go func() {
		_, _, err := DoMidjourneyHttpRequest(c, time.Minute, server.URL)
		requestDone <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "midjourney upstream request did not start")
	}

	cancelRequest()

	select {
	case err := <-requestDone:
		require.Error(t, err)
		require.True(t, errors.Is(err, context.Canceled))
	case <-time.After(5 * time.Second):
		require.FailNow(t, "midjourney request did not return after cancellation")
	}
}
