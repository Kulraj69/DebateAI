package middlewares

import (
	"bytes"
	"crypto/rand"
	"log"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestValidateJWTDoesNotLogClaims(t *testing.T) {
	// Keep signing material and tokens in memory, never in test output.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("could not create test signing key")
	}
	claims := jwt.MapClaims{
		"sub": "privacy-test@example.invalid",
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		t.Fatal("could not sign test token")
	}

	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	got, err := validateJWT(token, string(key))
	if err != nil {
		t.Fatal("valid token was rejected")
	}
	if got["sub"] != claims["sub"] {
		t.Fatal("validation changed the subject claim")
	}
	if output.Len() != 0 {
		t.Fatal("successful validation must not write claims to the log")
	}
}

func TestValidateJWTRejectsInvalidTokens(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("could not create test signing key")
	}
	otherKey := make([]byte, 32)
	if _, err := rand.Read(otherKey); err != nil {
		t.Fatal("could not create alternate test signing key")
	}
	for _, tc := range []struct {
		name   string
		claims jwt.MapClaims
		key    []byte
	}{
		{"expired", jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()}, key},
		{"not yet valid", jwt.MapClaims{"nbf": time.Now().Add(time.Hour).Unix()}, key},
		{"wrong signature", jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}, otherKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tc.claims).SignedString(tc.key)
			if err != nil {
				t.Fatal("could not sign test token")
			}
			claims, err := validateJWT(token, string(key))
			if err == nil || claims != nil {
				t.Fatal("invalid token must be rejected without returning claims")
			}
		})
	}
	t.Run("malformed", func(t *testing.T) {
		claims, err := validateJWT("malformed", string(key))
		if err == nil || claims != nil {
			t.Fatal("malformed token must be rejected without returning claims")
		}
	})
}
