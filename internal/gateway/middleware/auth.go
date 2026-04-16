package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// AuthMiddleware validates API keys, opaque Bearer tokens in the allowlist, and optionally HS256 JWTs.
type AuthMiddleware struct {
	apiKeys       map[string]bool
	jwtHMACSecret []byte // when non-empty, Bearer tokens with three JWT segments are verified as HS256
}

// NewAuth builds middleware that accepts the given API keys (and matching Bearer values).
func NewAuth(validKeys []string) *AuthMiddleware {
	m := make(map[string]bool, len(validKeys))
	for _, k := range validKeys {
		k = strings.TrimSpace(k)
		if k != "" {
			m[k] = true
		}
	}
	return &AuthMiddleware{apiKeys: m}
}

// NewAuthWithJWT is like NewAuth but also accepts Bearer JWTs signed with HS256 using jwtSecret.
func NewAuthWithJWT(validKeys []string, jwtHMACSecret string) *AuthMiddleware {
	a := NewAuth(validKeys)
	s := strings.TrimSpace(jwtHMACSecret)
	if s != "" {
		a.jwtHMACSecret = []byte(s)
	}
	return a
}

// Wrap enforces authentication when at least one key is configured or JWT verification is enabled.
// With an empty key set and no JWT secret, all requests pass through (use only in dev).
func (a *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	if len(a.apiKeys) == 0 && len(a.jwtHMACSecret) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if token == "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				token = strings.TrimSpace(auth[7:])
			}
		}
		if token == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if a.apiKeys[token] {
			next.ServeHTTP(w, r)
			return
		}
		if len(a.jwtHMACSecret) > 0 && strings.Count(token, ".") == 2 {
			if claims, err := verifyHS256JWT(token, a.jwtHMACSecret); err == nil && jwtClaimsValid(claims) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

func verifyHS256JWT(token string, secret []byte) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("jwt: malformed")
	}
	signing := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signing))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("jwt: bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func jwtClaimsValid(claims map[string]interface{}) bool {
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() >= int64(exp) {
			return false
		}
	}
	return true
}
