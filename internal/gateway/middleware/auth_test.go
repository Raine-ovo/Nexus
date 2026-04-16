package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthMiddleware_APIKey(t *testing.T) {
	a := NewAuth([]string{"secret-key"})
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("want teapot, got %d", rec.Code)
	}
}

func TestAuthMiddleware_JWT(t *testing.T) {
	secret := []byte("jwt-secret")
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]interface{}{
		"sub": "user",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signing))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	token := signing + "." + sig

	a := NewAuthWithJWT(nil, string(secret))
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("jwt auth: got %d", rec.Code)
	}
}

func TestVerifyHS256JWT_BadSig(t *testing.T) {
	_, err := verifyHS256JWT("a.b.c", []byte("x"))
	if err == nil {
		t.Fatal("expected error for garbage token")
	}
}
