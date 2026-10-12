package main

import "testing"

func TestNewTokenUniqueAndLong(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := newToken()
		if err != nil {
			t.Fatalf("newToken: %v", err)
		}
		if len(tok) < 40 {
			t.Fatalf("token too short: %d", len(tok))
		}
		if seen[tok] {
			t.Fatal("duplicate token generated")
		}
		seen[tok] = true
	}
}

func TestHashTokenStable(t *testing.T) {
	a := hashToken("hello")
	b := hashToken("hello")

	if a != b {
		t.Fatal("hash not stable for same input")
	}
	if a == hashToken("hello!") {
		t.Fatal("different inputs hashed equal")
	}
	if len(a) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(a))
	}
}
