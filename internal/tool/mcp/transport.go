package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Transport moves JSON-RPC messages between client and server.
type Transport interface {
	// Send writes one JSON-RPC message (full JSON object on one line or frame).
	Send(ctx context.Context, payload []byte) error
	// Receive reads the next JSON-RPC response or server event.
	Receive(ctx context.Context) ([]byte, error)
	Close() error
}

// StdioTransport uses newline-delimited JSON on stdin/stdout (typical for MCP subprocess servers).
type StdioTransport struct {
	mu     sync.Mutex
	reader *bufio.Reader
	writer io.Writer
	in     io.ReadCloser
	out    io.WriteCloser
}

// NewStdioTransport wraps stdin/stdout (or any read/write pair).
func NewStdioTransport(in io.ReadCloser, out io.WriteCloser) *StdioTransport {
	return &StdioTransport{
		reader: bufio.NewReaderSize(in, 1<<20),
		writer: out,
		in:     in,
		out:    out,
	}
}

// Send writes one line of JSON terminated by '\n'.
func (t *StdioTransport) Send(ctx context.Context, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.writer.Write(payload); err != nil {
		return err
	}
	if _, err := t.writer.Write([]byte{'\n'}); err != nil {
		return err
	}
	return nil
}

// Receive reads one line (delimited by '\n'), trimming trailing \r.
func (t *StdioTransport) Receive(ctx context.Context) ([]byte, error) {
	type line struct {
		b   []byte
		err error
	}
	ch := make(chan line, 1)
	go func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		raw, err := t.reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			ch <- line{nil, err}
			return
		}
		raw = bytes.TrimSuffix(raw, []byte{'\n'})
		raw = bytes.TrimSuffix(raw, []byte{'\r'})
		ch <- line{raw, nil}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case l := <-ch:
		return l.b, l.err
	}
}

// Close closes underlying streams.
func (t *StdioTransport) Close() error {
	var first error
	if t.in != nil {
		if err := t.in.Close(); err != nil {
			first = err
		}
	}
	if t.out != nil && !sameCloserTarget(t.out, t.in) {
		if err := t.out.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func sameCloserTarget(a io.WriteCloser, b io.ReadCloser) bool {
	if a == nil || b == nil {
		return false
	}
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	if va.Kind() != reflect.Pointer || vb.Kind() != reflect.Pointer {
		return false
	}
	return va.Pointer() == vb.Pointer() && va.Type() == vb.Type()
}

// SSETransport performs HTTP POST for outbound JSON-RPC and optional SSE for server-pushed events.
// Send stores the POST response for the next Receive (paired round-trip). After ConnectSSE, Receive can read data: lines.
type SSETransport struct {
	baseURL    string
	httpClient *http.Client
	postPath   string

	mu       sync.Mutex
	resp     *http.Response
	reader   *bufio.Reader
	syncBody []byte
	syncErr  error
}

// NewSSETransport creates a client that POSTs JSON-RPC to baseURL+postPath.
func NewSSETransport(baseURL, postPath string) *SSETransport {
	if postPath == "" {
		postPath = "/mcp/rpc"
	}
	return &SSETransport{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		postPath: postPath,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// WithHTTPClient overrides the HTTP client (timeouts, TLS, etc.).
func (t *SSETransport) WithHTTPClient(c *http.Client) *SSETransport {
	if c != nil {
		t.httpClient = c
	}
	return t
}

// ConnectSSE opens a GET stream at streamPath (e.g. /mcp/sse) for server-pushed `data:` events.
func (t *SSETransport) ConnectSSE(ctx context.Context, streamPath string) error {
	if streamPath == "" {
		streamPath = "/mcp/sse"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+streamPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("sse connect: %s", resp.Status)
	}

	t.mu.Lock()
	if t.resp != nil {
		_ = t.resp.Body.Close()
	}
	t.resp = resp
	t.reader = bufio.NewReaderSize(resp.Body, 1<<20)
	t.mu.Unlock()
	return nil
}

// Send POSTs a JSON-RPC payload; the response body is consumed by the next Receive call.
func (t *SSETransport) Send(ctx context.Context, payload []byte) error {
	body, err := t.SendPOST(ctx, payload)
	t.mu.Lock()
	t.syncBody = body
	t.syncErr = err
	t.mu.Unlock()
	return nil
}

// SendPOST performs HTTP POST and returns the response body bytes.
func (t *SSETransport) SendPOST(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+t.postPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("http %d: %s", resp.StatusCode, string(bytes.TrimSpace(body)))
	}
	return body, nil
}

// Receive returns the body from the most recent Send (HTTP sync), or the next `data:` line from SSE.
func (t *SSETransport) Receive(ctx context.Context) ([]byte, error) {
	t.mu.Lock()
	if t.syncErr != nil {
		err := t.syncErr
		t.syncErr = nil
		t.syncBody = nil
		t.mu.Unlock()
		return nil, err
	}
	if len(t.syncBody) > 0 {
		b := t.syncBody
		t.syncBody = nil
		t.mu.Unlock()
		return b, nil
	}
	r := t.reader
	t.mu.Unlock()

	if r == nil {
		return nil, fmt.Errorf("no pending http response and sse not connected")
	}

	for {
		type scan struct {
			line string
			err  error
		}
		ch := make(chan scan, 1)
		go func() {
			s, err := r.ReadString('\n')
			ch <- scan{s, err}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case s := <-ch:
			if s.err != nil {
				return nil, s.err
			}
			line := strings.TrimSpace(s.line)
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data != "" {
					return []byte(data), nil
				}
			}
		}
	}
}

// Close releases the SSE response body.
func (t *SSETransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resp != nil {
		err := t.resp.Body.Close()
		t.resp = nil
		t.reader = nil
		return err
	}
	return nil
}
