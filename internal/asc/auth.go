package asc

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// audience is the fixed JWT audience required by App Store Connect.
const audience = "appstoreconnect-v1"

// tokenLifetime is how long each minted token is valid. Apple caps this at
// 20 minutes; we use 15 to leave headroom.
const tokenLifetime = 15 * time.Minute

// TokenSource mints and caches short-lived ES256 JWTs for the ASC API.
type TokenSource struct {
	keyID    string
	issuerID string
	privKey  *ecdsa.PrivateKey

	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewTokenSource parses the PKCS#8 (.p8) private key and returns a token source.
func NewTokenSource(keyID, issuerID string, pemKey []byte) (*TokenSource, error) {
	key, err := parseP8(pemKey)
	if err != nil {
		return nil, err
	}
	return &TokenSource{keyID: keyID, issuerID: issuerID, privKey: key}, nil
}

// Token returns a cached token if still fresh, otherwise mints a new one.
func (ts *TokenSource) Token() (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Refresh a minute before expiry to avoid races with in-flight requests.
	if ts.token != "" && time.Now().Before(ts.expiry.Add(-time.Minute)) {
		return ts.token, nil
	}

	now := time.Now()
	exp := now.Add(tokenLifetime)
	claims := jwt.MapClaims{
		"iss": ts.issuerID,
		"iat": now.Unix(),
		"exp": exp.Unix(),
		"aud": audience,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	tok.Header["kid"] = ts.keyID

	signed, err := tok.SignedString(ts.privKey)
	if err != nil {
		return "", fmt.Errorf("signing ASC token: %w", err)
	}
	ts.token = signed
	ts.expiry = exp
	return signed, nil
}

// parseP8 parses an Apple .p8 key, which is PEM-wrapped PKCS#8 EC private key.
func parseP8(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("private key is not valid PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing PKCS#8 private key: %w", err)
	}
	ecKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want *ecdsa.PrivateKey", parsed)
	}
	return ecKey, nil
}
