// ws.go — WebSocket 传输层（gobwas/ws）。
//
// 真实协议为 WebSocket 长连接 wss://bot-wss.yuanbao.tencent.com/wss/connection，
// 每帧为一条二进制 ConnMsg protobuf（见 proto.go）。
package yuanbao

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/gobwas/ws"
)

const connectTimeout = 15 * time.Second

var errConnClosed = errors.New("websocket connection closed")

// wsTransport 是一条已握手的 WebSocket 连接。
type wsTransport struct {
	conn   net.Conn
	reader *bufio.Reader
	wmu    sync.Mutex
}

// dialWS 建立 WebSocket 连接（ctx 控制连接超时）。
func dialWS(ctx context.Context, rawURL string) (*wsTransport, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("yuanbao: parse ws url: %w", err)
	}
	switch u.Scheme {
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("yuanbao: unsupported ws scheme %q", u.Scheme)
	}
	d := ws.Dialer{
		TLSConfig: &tls.Config{ServerName: u.Hostname()},
	}
	// gobwas Dialer.Timeout 与 ctx 并存：取两者中更紧的约束。
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl); remaining < connectTimeout {
			d.Timeout = remaining
		}
	}
	if d.Timeout == 0 {
		d.Timeout = connectTimeout
	}
	conn, br, _, err := d.Dial(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("yuanbao: ws dial: %w", err)
	}
	// gobwas 在握手后无缓冲残留时返回 nil reader，需自建。
	if br == nil {
		br = bufio.NewReader(conn)
	}
	return &wsTransport{conn: conn, reader: br}, nil
}

func (t *wsTransport) writeFrame(op ws.OpCode, payload []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	f := ws.MaskFrame(ws.NewFrame(op, true, payload))
	return ws.WriteFrame(t.conn, f)
}

// writeBinary 发送一帧二进制数据（客户端帧必须掩码）。
func (t *wsTransport) writeBinary(data []byte) error {
	return t.writeFrame(ws.OpBinary, data)
}

// readMessage 读取下一条消息。透明处理协议层 ping/pong/close；连接关闭或
// 收到对端 close 帧时返回错误。
func (t *wsTransport) readMessage() ([]byte, error) {
	for {
		f, err := ws.ReadFrame(t.reader)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil, errConnClosed
			}
			return nil, err
		}
		if f.Header.Masked {
			f = ws.UnmaskFrame(f)
		}
		switch f.Header.OpCode {
		case ws.OpBinary, ws.OpText:
			return f.Payload, nil
		case ws.OpPing:
			_ = t.writeFrame(ws.OpPong, f.Payload)
		case ws.OpPong:
			// 忽略
		case ws.OpClose:
			_ = t.writeFrame(ws.OpClose, f.Payload)
			return nil, errConnClosed
		default:
			// continuation 等不支持的帧：忽略
		}
	}
}

func (t *wsTransport) close() error {
	return t.conn.Close()
}
