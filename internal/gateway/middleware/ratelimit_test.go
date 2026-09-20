package middleware

import (
	"net/http"
	"strings"
	"testing"
)

func TestClientIPIgnoresXFFWithoutTrustedProxy(t *testing.T) {
	req := &http.Request{Header: http.Header{}, RemoteAddr: "1.2.3.4:1234"}
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 8.8.8.8")
	if got := clientIP(req, nil); got != "1.2.3.4" {
		t.Fatalf("clientIP = %q, want 1.2.3.4", got)
	}
}

func TestClientIPTrustsXFFFromTrustedProxy(t *testing.T) {
	req := &http.Request{Header: http.Header{}, RemoteAddr: "10.0.0.1:1234"}
	req.Header.Set("X-Forwarded-For", "9.9.9.9, 8.8.8.8")
	if got := clientIP(req, map[string]bool{"10.0.0.1": true}); got != "9.9.9.9" {
		t.Fatalf("clientIP = %q, want 9.9.9.9", got)
	}
}

func TestClientIPIgnoresXFFFromUntrustedPeer(t *testing.T) {
	req := &http.Request{Header: http.Header{}, RemoteAddr: "1.2.3.4:1234"}
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	if got := clientIP(req, map[string]bool{"10.0.0.1": true}); got != "1.2.3.4" {
		t.Fatalf("clientIP = %q, want 1.2.3.4", got)
	}
}

func TestClientKeySeparatesTenants(t *testing.T) {
	r1 := &http.Request{Header: http.Header{}, RemoteAddr: "1.2.3.4:1234"}
	r1.Header.Set("X-API-Key", "tenant-a")
	r2 := &http.Request{Header: http.Header{}, RemoteAddr: "1.2.3.4:1234"}
	r2.Header.Set("X-API-Key", "tenant-b")

	a := clientKey(r1, nil)
	b := clientKey(r2, nil)
	if a == b {
		t.Fatalf("different API keys should get different buckets: %q", a)
	}
	if !strings.Contains(a, "tenant-a") || !strings.Contains(b, "tenant-b") {
		t.Fatalf("bucket keys should include the api key: %q / %q", a, b)
	}
}

func TestClientKeyUsesBearerToken(t *testing.T) {
	r := &http.Request{Header: http.Header{}, RemoteAddr: "1.2.3.4:1234"}
	r.Header.Set("Authorization", "Bearer tok-123")
	if got := clientKey(r, nil); !strings.Contains(got, "tok-123") {
		t.Fatalf("expected bearer token in bucket key, got %q", got)
	}
}
