// کلاینت مینیمال WebSocket برای CDP (بدون وابستگی خارجی).
//
// فقط آنچه Chrome DevTools Protocol لازم دارد: handshake کلاینت، فریم متنی
// ماسک‌شده برای ارسال، خواندن فریم (با reassembly continuation)، پاسخ به ping
// و بستن تمیز. سقف هر پیام برای جلوگیری از بمب حافظه اعمال می‌شود.
package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const wsMaxMessage = 32 << 20 // 32MB — اسکرین‌شات base64 هم جا می‌شود

// wsConn یک اتصال WebSocket کلاینت.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
}

// wsDial اتصال WebSocket به ws://host:port/path برقرار می‌کند.
func wsDial(rawURL string, timeout time.Duration) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return nil, fmt.Errorf("invalid websocket url %q", rawURL)
	}
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		return nil, err
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake failed: HTTP %d", resp.StatusCode)
	}
	want := wsAcceptKey(key)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != want {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake: bad Sec-WebSocket-Accept")
	}
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br}, nil
}

// wsAcceptKey مقدار مورد انتظار Sec-WebSocket-Accept را می‌سازد.
func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

// WriteText یک پیام متنی (ماسک‌شده) می‌فرستد. نوشتن هم‌زمان سریال می‌شود.
func (c *wsConn) WriteText(payload []byte) error {
	if len(payload) > wsMaxMessage {
		return fmt.Errorf("websocket message too large (%d bytes)", len(payload))
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	header := make([]byte, 0, 14)
	header = append(header, 0x81) // FIN + text
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n <= 0xffff:
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(n))
	default:
		header = append(header, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	frame := make([]byte, len(header)+n)
	copy(frame, header)
	for i := 0; i < n; i++ {
		frame[len(header)+i] = payload[i] ^ mask[i&3]
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
	_, err := c.conn.Write(frame)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

// ReadMessage یک پیام کامل (متن/باینری) می‌خواند؛ ping را پاسخ می‌دهد و
// close را io.EOF برمی‌گرداند.
func (c *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	for {
		op, fin, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case 0x1, 0x2, 0x0: // text, binary, continuation
			msg = append(msg, payload...)
			if len(msg) > wsMaxMessage {
				return nil, fmt.Errorf("websocket message too large")
			}
			if fin {
				return msg, nil
			}
		case 0x8: // close
			return nil, io.EOF
		case 0x9: // ping → pong
			if err := c.writeControl(0xA, payload); err != nil {
				return nil, err
			}
		case 0xA: // pong — نادیده
		}
	}
}

func (c *wsConn) writeControl(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	frame := []byte{0x80 | op, 0x80 | byte(len(payload))}
	frame = append(frame, mask...)
	for i := range payload {
		frame = append(frame, payload[i]^mask[i&3])
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Write(frame)
	_ = c.conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *wsConn) readFrame() (op byte, fin bool, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(c.br, hdr); err != nil {
		return
	}
	fin = hdr[0]&0x80 != 0
	op = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7f)
	switch length {
	case 126:
		b := make([]byte, 2)
		if _, err = io.ReadFull(c.br, b); err != nil {
			return
		}
		length = int64(binary.BigEndian.Uint16(b))
	case 127:
		b := make([]byte, 8)
		if _, err = io.ReadFull(c.br, b); err != nil {
			return
		}
		length = int64(binary.BigEndian.Uint64(b))
	}
	if length < 0 || length > wsMaxMessage {
		return op, fin, nil, fmt.Errorf("websocket frame too large (%d bytes)", length)
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return op, fin, payload, nil
}

func (c *wsConn) Close() error {
	_ = c.writeControl(0x8, nil)
	return c.conn.Close()
}

// wsIsLocalURL فقط آدرس‌های http(s) را می‌پذیرد (بدون file:/javascript:).
func wsIsLocalURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
