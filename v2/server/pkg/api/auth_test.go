package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestParseBearerToken(t *testing.T) {
	token, err := parseBearerToken("Bearer abc123")
	if err != nil {
		t.Fatalf("parseBearerToken failed: %v", err)
	}
	if token != "abc123" {
		t.Fatalf("unexpected token: %s", token)
	}
}

func TestParseBearerTokenInvalid(t *testing.T) {
	cases := []string{
		"",
		"Bearer",
		"Basic abc123",
		"Bearer   ",
	}

	for _, in := range cases {
		if _, err := parseBearerToken(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestVerifyJWTToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	token := issueTestJWT(t, "secret-a", jwtClaims{
		Namespace: "ns-a",
		Iat:       now.Unix() - 10,
		Nbf:       now.Unix() - 5,
		Exp:       now.Unix() + 3600,
	})

	claims, err := verifyJWTToken(token, "secret-a", now)
	if err != nil {
		t.Fatalf("verifyJWTToken failed: %v", err)
	}
	if claims.Namespace != "ns-a" {
		t.Fatalf("unexpected namespace: %q", claims.Namespace)
	}
}

func TestVerifyJWTTokenInvalidSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	token := issueTestJWT(t, "secret-a", jwtClaims{
		Namespace: "ns-a",
		Exp:       now.Unix() + 3600,
	})

	if _, err := verifyJWTToken(token, "secret-b", now); err == nil {
		t.Fatalf("expected signature verify error")
	}
}

func TestVerifyJWTTokenExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	token := issueTestJWT(t, "secret-a", jwtClaims{
		Namespace: "ns-a",
		Exp:       now.Unix() - 1,
	})

	if _, err := verifyJWTToken(token, "secret-a", now); err == nil {
		t.Fatalf("expected expired error")
	}
}

func issueTestJWT(t *testing.T, key string, claims jwtClaims) string {
	t.Helper()

	headerBytes, err := json.Marshal(jwtHeader{
		Alg: "HS256",
		Typ: "JWT",
	})
	if err != nil {
		t.Fatalf("marshal header failed: %v", err)
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims failed: %v", err)
	}

	headerSeg := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimSeg := base64.RawURLEncoding.EncodeToString(claimBytes)
	payload := fmt.Sprintf("%s.%s", headerSeg, claimSeg)

	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(payload))
	sigSeg := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sigSeg
}
