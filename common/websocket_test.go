package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestDialWebSocketWithContextClosesEstablishedConnectionOnCancel(t *testing.T) {
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer close(serverDone)
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	conn, _, err := DialWebSocketWithContext(ctx, websocket.DefaultDialer, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	cancel()

	readDone := make(chan error, 1)
	go func() {
		_, _, readErr := conn.ReadMessage()
		readDone <- readErr
	}()

	select {
	case readErr := <-readDone:
		require.Error(t, readErr)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "websocket read did not stop after context cancellation")
	}

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "websocket server connection did not close after context cancellation")
	}
}
