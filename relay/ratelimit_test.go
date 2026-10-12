package main

import (
	"testing"
	"time"
)

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !rl.allow("k") {
			t.Fatalf("attempt %d should pass", i+1)
		}
	}
	if rl.allow("k") {
		t.Fatal("4th attempt should be blocked")
	}
}

func TestRateLimiterKeysAreIndependent(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	if !rl.allow("a") {
		t.Fatal("first key should pass")
	}
	if !rl.allow("b") {
		t.Fatal("second key should pass")
	}
	if rl.allow("a") {
		t.Fatal("first key should be blocked")
	}
}

func TestRateLimiterResetsAfterWindow(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	if !rl.allow("k") {
		t.Fatal("first should pass")
	}
	if rl.allow("k") {
		t.Fatal("second should block")
	}

	now = now.Add(61 * time.Second)
	if !rl.allow("k") {
		t.Fatal("after the window it should pass again")
	}
}
