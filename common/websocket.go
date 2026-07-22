package common

import (
	"context"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
)

type contextBoundConn struct {
	net.Conn
	stopContextWatch func() bool
}

func (c *contextBoundConn) Close() error {
	if c.stopContextWatch != nil {
		c.stopContextWatch()
	}
	return c.Conn.Close()
}

// DialWebSocketWithContext keeps the request context attached after the TCP
// connection is established. gorilla/websocket's DialContext only guarantees
// cancellation while dialing; closing the underlying connection is also needed
// to interrupt a blocked TLS/WebSocket handshake or a later read.
func DialWebSocketWithContext(ctx context.Context, dialer *websocket.Dialer, urlStr string, requestHeader http.Header) (*websocket.Conn, *http.Response, error) {
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}

	contextDialer := *dialer
	baseDialContext := contextDialer.NetDialContext
	if baseDialContext == nil {
		netDialer := &net.Dialer{}
		baseDialContext = netDialer.DialContext
	}
	contextDialer.NetDialContext = func(dialContext context.Context, network, address string) (net.Conn, error) {
		conn, err := baseDialContext(dialContext, network, address)
		if err != nil {
			return nil, err
		}

		boundConn := &contextBoundConn{Conn: conn}
		boundConn.stopContextWatch = context.AfterFunc(ctx, func() {
			_ = conn.Close()
		})
		return boundConn, nil
	}

	return contextDialer.DialContext(ctx, urlStr, requestHeader)
}
