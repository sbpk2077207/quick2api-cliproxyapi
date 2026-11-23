package quick

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"quick2api-plugin/internal/core"
)

// WSConn 是 AIDP 双向流连接。上行是二进制帧承载 UTF-8 JSON，
// 下行是 AWS event-stream 二进制帧（可能跨多个 WS 帧）。
type WSConn struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	buf    []byte
	events []*InnerEvent
}

// DialWS 建立聊天 WebSocket。
// 注意：id_token 在 query 里，任何日志都不能打印 u.String()。
func DialWS(ctx context.Context, sess *core.Session, proxy string) (*WSConn, error) {
	u := url.URL{
		Scheme: "wss",
		Host:   sess.WSHost,
		Path:   "/streaming/quickwork-chat-bidirectional-stream",
	}
	q := u.Query()
	q.Set("surfaceType", SurfaceDesktop)
	q.Set("token", sess.IDToken)
	q.Set("outpostId", sess.DeviceID)
	q.Set("workingSessionId", NewUUID())
	q.Set("channel", "prod")
	u.RawQuery = q.Encode()

	d := websocket.Dialer{
		HandshakeTimeout: 20 * time.Second,
	}
	if proxy != "" {
		p, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy: %w", err)
		}
		d.Proxy = func(*http.Request) (*url.URL, error) { return p, nil }
	}
	c, resp, err := d.DialContext(ctx, u.String(), http.Header{
		"Origin": []string{"https://quick.aws.com"},
	})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed (status %d): %w", resp.StatusCode, err)
		}
		return nil, err
	}
	return &WSConn{ws: c}, nil
}

func (c *WSConn) Close() error { return c.ws.Close() }

// Send 发送一条 inner JSON 帧。
func (c *WSConn) Send(typ string, payload any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	inner, err := json.Marshal(map[string]any{"type": typ, "payload": payload})
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(map[string]any{
		"eventType": "rawInputEvent",
		"payload":   map[string]string{"payload": base64.StdEncoding.EncodeToString(inner)},
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteMessage(websocket.BinaryMessage, envelope)
}

// Recv 返回下一条 inner 事件。读循环负责把 event-stream 字节流重组为消息。
func (c *WSConn) Recv(ctx context.Context) (*InnerEvent, error) {
	for {
		if len(c.events) > 0 {
			ev := c.events[0]
			c.events = c.events[1:]
			return ev, nil
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(70 * time.Second))
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		c.buf = append(c.buf, data...)
		msgs, consumed, err := DecodeEventStream(c.buf)
		if err != nil {
			return nil, err
		}
		if consumed > 0 {
			c.buf = c.buf[consumed:]
		}
		for _, m := range msgs {
			ev, err := ExtractInner(m.Payload)
			if err != nil {
				continue
			}
			c.events = append(c.events, ev)
		}
	}
}
