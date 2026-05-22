package asc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func testP8(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return pemBytes, key
}

func TestTokenSource(t *testing.T) {
	pemBytes, key := testP8(t)
	ts, err := NewTokenSource("KID123", "ISS456", pemBytes)
	if err != nil {
		t.Fatalf("NewTokenSource: %v", err)
	}

	tokStr, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	parsed, err := jwt.Parse(tokStr, func(tok *jwt.Token) (any, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodECDSA); !ok {
			t.Fatalf("unexpected alg: %v", tok.Header["alg"])
		}
		if tok.Header["kid"] != "KID123" {
			t.Fatalf("kid = %v, want KID123", tok.Header["kid"])
		}
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	claims := parsed.Claims.(jwt.MapClaims)
	if claims["iss"] != "ISS456" {
		t.Errorf("iss = %v, want ISS456", claims["iss"])
	}
	if claims["aud"] != audience {
		t.Errorf("aud = %v, want %s", claims["aud"], audience)
	}

	// Cached token should be returned on the second call.
	if tok2, _ := ts.Token(); tok2 != tokStr {
		t.Errorf("token not cached")
	}
}

func TestParseP8Invalid(t *testing.T) {
	if _, err := parseP8([]byte("not a pem")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}
