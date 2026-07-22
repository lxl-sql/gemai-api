package channel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type requestContextTestAdaptor struct {
	Adaptor
	requestURL string
}

func (a requestContextTestAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.requestURL, nil
}

func (requestContextTestAdaptor) SetupRequestHeader(_ *gin.Context, _ *http.Header, _ *relaycommon.RelayInfo) error {
	return nil
}

type requestContextTaskAdaptor struct {
	TaskAdaptor
	requestURL string
}

type contextRequestURLTestAdaptor struct {
	requestContextTestAdaptor
}

func (contextRequestURLTestAdaptor) GetRequestURLWithContext(ctx context.Context, _ *relaycommon.RelayInfo) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (a requestContextTaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.requestURL, nil
}

func (requestContextTaskAdaptor) BuildRequestHeader(_ *gin.Context, _ *http.Request, _ *relaycommon.RelayInfo) error {
	return nil
}

func TestContextAwareRequestURLResolutionReturnsClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestContext, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(requestContext, http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))

	response, err := DoApiRequest(
		contextRequestURLTestAdaptor{},
		c,
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
		strings.NewReader(`{}`),
	)

	require.Nil(t, response)
	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	require.True(t, types.IsClientDisconnectedError(apiErr))
}

func TestUpstreamHTTPRequestsInheritClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	generalSettings := operation_setting.GetGeneralSetting()
	previousPaddingEnabled := generalSettings.NonStreamPaddingEnabled
	previousPingEnabled := generalSettings.PingIntervalEnabled
	generalSettings.NonStreamPaddingEnabled = false
	generalSettings.PingIntervalEnabled = false
	t.Cleanup(func() {
		generalSettings.NonStreamPaddingEnabled = previousPaddingEnabled
		generalSettings.PingIntervalEnabled = previousPingEnabled
	})

	tests := []struct {
		name     string
		isStream bool
		run      func(*gin.Context, *relaycommon.RelayInfo, string) (*http.Response, error)
	}{
		{
			name: "api non-stream",
			run: func(c *gin.Context, info *relaycommon.RelayInfo, requestURL string) (*http.Response, error) {
				return DoApiRequest(requestContextTestAdaptor{requestURL: requestURL}, c, info, strings.NewReader(`{}`))
			},
		},
		{
			name:     "api stream",
			isStream: true,
			run: func(c *gin.Context, info *relaycommon.RelayInfo, requestURL string) (*http.Response, error) {
				return DoApiRequest(requestContextTestAdaptor{requestURL: requestURL}, c, info, strings.NewReader(`{}`))
			},
		},
		{
			name: "form",
			run: func(c *gin.Context, info *relaycommon.RelayInfo, requestURL string) (*http.Response, error) {
				return DoFormRequest(requestContextTestAdaptor{requestURL: requestURL}, c, info, strings.NewReader("field=value"))
			},
		},
		{
			name: "task",
			run: func(c *gin.Context, info *relaycommon.RelayInfo, requestURL string) (*http.Response, error) {
				return DoTaskApiRequest(requestContextTaskAdaptor{requestURL: requestURL}, c, info, strings.NewReader(`{}`))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstreamStarted := make(chan struct{})
			releaseUpstream := make(chan struct{})
			var releaseOnce sync.Once
			release := func() {
				releaseOnce.Do(func() { close(releaseUpstream) })
			}
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				close(upstreamStarted)
				<-releaseUpstream
			}))
			defer func() {
				release()
				server.Close()
			}()

			requestContext, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequestWithContext(requestContext, http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{
				IsStream:    test.isStream,
				ChannelMeta: &relaycommon.ChannelMeta{},
			}

			requestDone := make(chan error, 1)
			go func() {
				response, err := test.run(c, info, server.URL)
				if response != nil {
					_ = response.Body.Close()
				}
				requestDone <- err
			}()

			select {
			case <-upstreamStarted:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "upstream request did not start")
			}

			cancelRequest()

			select {
			case err := <-requestDone:
				require.Error(t, err)
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				require.True(t, types.IsClientDisconnectedError(apiErr))
			case <-time.After(5 * time.Second):
				release()
				require.FailNow(t, "relay request did not return after cancellation")
			}
		})
	}
}

func TestNonStreamPaddingRequestInheritsClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	generalSettings := operation_setting.GetGeneralSetting()
	previousPaddingEnabled := generalSettings.NonStreamPaddingEnabled
	previousPaddingDelay := generalSettings.NonStreamPaddingDelaySeconds
	previousPingEnabled := generalSettings.PingIntervalEnabled
	generalSettings.NonStreamPaddingEnabled = true
	generalSettings.NonStreamPaddingDelaySeconds = 60
	generalSettings.PingIntervalEnabled = false
	t.Cleanup(func() {
		generalSettings.NonStreamPaddingEnabled = previousPaddingEnabled
		generalSettings.NonStreamPaddingDelaySeconds = previousPaddingDelay
		generalSettings.PingIntervalEnabled = previousPingEnabled
	})

	upstreamStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseUpstream) })
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(upstreamStarted)
		<-releaseUpstream
	}))
	defer func() {
		release()
		server.Close()
	}()

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(requestContext, http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	requestDone := make(chan error, 1)

	go func() {
		response, err := DoApiRequest(
			requestContextTestAdaptor{requestURL: server.URL},
			c,
			&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
			strings.NewReader(`{}`),
		)
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "upstream padding request did not start")
	}

	cancelRequest()

	select {
	case err := <-requestDone:
		require.Error(t, err)
		var apiErr *types.NewAPIError
		require.True(t, errors.As(err, &apiErr))
		require.True(t, types.IsClientDisconnectedError(apiErr))
	case <-time.After(5 * time.Second):
		release()
		require.FailNow(t, "padding request did not return after cancellation")
	}
}

func TestNonStreamPaddingResponseBodyCloseReleasesUpstreamContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	generalSettings := operation_setting.GetGeneralSetting()
	previousPaddingEnabled := generalSettings.NonStreamPaddingEnabled
	previousPaddingDelay := generalSettings.NonStreamPaddingDelaySeconds
	previousPingEnabled := generalSettings.PingIntervalEnabled
	generalSettings.NonStreamPaddingEnabled = true
	generalSettings.NonStreamPaddingDelaySeconds = 60
	generalSettings.PingIntervalEnabled = false
	t.Cleanup(func() {
		generalSettings.NonStreamPaddingEnabled = previousPaddingEnabled
		generalSettings.NonStreamPaddingDelaySeconds = previousPaddingDelay
		generalSettings.PingIntervalEnabled = previousPingEnabled
	})

	upstreamCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(upstreamCanceled)
	}))
	defer server.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	response, err := DoApiRequest(
		requestContextTestAdaptor{requestURL: server.URL},
		c,
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
		strings.NewReader(`{}`),
	)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NoError(t, response.Body.Close())

	select {
	case <-upstreamCanceled:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "upstream context was not released when the response body closed")
	}
}

func TestUpstreamWebSocketDialInheritsClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamStarted := make(chan struct{})
	releaseUpstream := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseUpstream) })
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(upstreamStarted)
		<-releaseUpstream
	}))
	defer func() {
		release()
		server.Close()
	}()

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(requestContext, http.MethodGet, "/v1/realtime", nil)
	requestURL := "ws" + strings.TrimPrefix(server.URL, "http")
	requestDone := make(chan error, 1)

	go func() {
		connection, err := DoWssRequest(
			requestContextTestAdaptor{requestURL: requestURL},
			c,
			&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
			nil,
		)
		if connection != nil {
			_ = connection.Close()
		}
		requestDone <- err
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "upstream websocket dial did not start")
	}

	cancelRequest()

	select {
	case err := <-requestDone:
		require.Error(t, err)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		require.True(t, types.IsClientDisconnectedError(apiErr))
	case <-time.After(5 * time.Second):
		release()
		require.FailNow(t, "websocket dial did not return after cancellation")
	}
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}
