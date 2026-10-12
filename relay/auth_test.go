package main

import (
	"strings"
	"testing"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := CheckPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("expected match, got ok=%v err=%v", ok, err)
	}

	ok, err = CheckPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if ok {
		t.Fatal("wrong password matched")
	}
}

func TestHashPasswordDifferentSalts(t *testing.T) {
	h1, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("hash reused salt; outputs should differ")
	}
}

func TestCheckPasswordBadEncoding(t *testing.T) {
	if _, err := CheckPassword("x", "not-a-valid-hash"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}

func TestValidPasswordBoundaries(t *testing.T) {
	good := []string{
		strings.Repeat("a", 10),
		strings.Repeat("b", 128),
		"caf\u00e9-password", // 14 runes, 15 bytes
	}
	for _, pw := range good {
		if !validPassword(pw) {
			t.Errorf("expected valid (len %d runes): %q", len([]rune(pw)), pw)
		}
	}

	bad := []string{
		"",
		strings.Repeat("a", 9),
		strings.Repeat("b", 129),
	}
	for _, pw := range bad {
		if validPassword(pw) {
			t.Errorf("expected invalid (len %d runes): %q", len([]rune(pw)), pw)
		}
	}
}
