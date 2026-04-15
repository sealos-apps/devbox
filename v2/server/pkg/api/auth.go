package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

type authContextKey string

const namespaceContextKey authContextKey = "namespace"

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
}

type jwtClaims struct {
	Namespace string `json:"namespace"`
	Exp       int64  `json:"exp,omitempty"`
	Nbf       int64  `json:"nbf,omitempty"`
	Iat       int64  `json:"iat,omitempty"`
}

func (s *apiServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := parseBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			s.logWarnError("auth parse bearer token failed", err, "method", r.Method, "path", r.URL.Path, "http_status", http.StatusUnauthorized)
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}

		claims, err := verifyJWTToken(token, s.cfg.JWTSigningKey, time.Now())
		if err != nil {
			s.logWarnError("auth jwt verify failed", err, "method", r.Method, "path", r.URL.Path, "http_status", http.StatusUnauthorized)
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		namespace := strings.TrimSpace(claims.Namespace)
		if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
			s.logWarnError("auth jwt namespace invalid", fmt.Errorf("invalid namespace in token claims"), "method", r.Method, "path", r.URL.Path, "namespace", namespace, "http_status", http.StatusUnauthorized)
			writeError(w, http.StatusUnauthorized, "invalid token namespace")
			return
		}

		w.Header().Set("X-Namespace", namespace)
		ctx := context.WithValue(r.Context(), namespaceContextKey, namespace)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func parseBearerToken(authorization string) (string, error) {
	auth := strings.TrimSpace(authorization)
	if auth == "" {
		return "", errors.New("missing Authorization header")
	}

	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("authorization header must be: Bearer <token>")
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", errors.New("missing bearer token")
	}
	return token, nil
}

func verifyJWTToken(token string, signingKey string, now time.Time) (jwtClaims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return jwtClaims{}, errors.New("empty token")
	}
	if strings.TrimSpace(signingKey) == "" {
		return jwtClaims{}, errors.New("empty jwt signing key")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("malformed jwt token")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode jwt header failed: %w", err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return jwtClaims{}, fmt.Errorf("parse jwt header failed: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(header.Alg), "HS256") {
		return jwtClaims{}, fmt.Errorf("unsupported jwt alg: %s", header.Alg)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode jwt signature failed: %w", err)
	}
	payloadToSign := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(signingKey))
	_, _ = mac.Write([]byte(payloadToSign))
	expectedSignature := mac.Sum(nil)
	if !hmac.Equal(signature, expectedSignature) {
		return jwtClaims{}, errors.New("jwt signature mismatch")
	}

	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("decode jwt claims failed: %w", err)
	}
	var claims jwtClaims
	if err := json.Unmarshal(claimBytes, &claims); err != nil {
		return jwtClaims{}, fmt.Errorf("parse jwt claims failed: %w", err)
	}

	claims.Namespace = strings.TrimSpace(claims.Namespace)
	if claims.Namespace == "" {
		return jwtClaims{}, errors.New("jwt claim namespace is required")
	}

	nowUnix := now.Unix()
	if claims.Exp != 0 && nowUnix >= claims.Exp {
		return jwtClaims{}, errors.New("jwt token expired")
	}
	if claims.Nbf != 0 && nowUnix < claims.Nbf {
		return jwtClaims{}, errors.New("jwt token not active yet")
	}

	return claims, nil
}

func signJWTToken(signingKey string, claims interface{}) (string, error) {
	signingKey = strings.TrimSpace(signingKey)
	if signingKey == "" {
		return "", errors.New("empty jwt signing key")
	}

	headerBytes, err := json.Marshal(jwtHeader{
		Alg: "HS256",
		Typ: "JWT",
	})
	if err != nil {
		return "", fmt.Errorf("marshal jwt header failed: %w", err)
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal jwt claims failed: %w", err)
	}

	headerSeg := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimSeg := base64.RawURLEncoding.EncodeToString(claimBytes)
	payload := headerSeg + "." + claimSeg

	mac := hmac.New(sha256.New, []byte(signingKey))
	_, _ = mac.Write([]byte(payload))
	sigSeg := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sigSeg, nil
}

func namespaceFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}

	namespace, ok := ctx.Value(namespaceContextKey).(string)
	if !ok {
		return "", false
	}

	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", false
	}
	return namespace, true
}
