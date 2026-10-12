package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// tokenBytes is the entropy of every token we generate (32 bytes = 256 bits).
const tokenBytes = 32

// newToken returns a fresh random, URL-safe token. The raw value is shown to
// the client once and never stored.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the hex sha256 of a raw token. Only this value is stored.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
