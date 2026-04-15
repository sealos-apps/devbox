package api

import (
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

func TestSignJWTToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	token, err := signJWTToken("secret-a", jwtClaims{
		Namespace: "ns-a",
		Iat:       now.Unix() - 10,
		Exp:       now.Unix() + 3600,
	})
	if err != nil {
		t.Fatalf("signJWTToken failed: %v", err)
	}

	claims, err := verifyJWTToken(token, "secret-a", now)
	if err != nil {
		t.Fatalf("verify signed token failed: %v", err)
	}
	if claims.Namespace != "ns-a" {
		t.Fatalf("unexpected namespace: %q", claims.Namespace)
	}
}

func issueTestJWT(t *testing.T, key string, claims jwtClaims) string {
	t.Helper()

	token, err := signJWTToken(key, claims)
	if err != nil {
		t.Fatalf("sign test jwt failed: %v", err)
	}
	return token
}
