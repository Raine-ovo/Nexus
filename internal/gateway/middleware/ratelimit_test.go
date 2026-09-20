package middleware

import (
	"net/http"
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
