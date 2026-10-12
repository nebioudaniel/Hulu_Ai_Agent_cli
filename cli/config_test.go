package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}

	dir := t.TempDir()

	if _, err := resolveProjectDir(""); err == nil {
		t.Fatal("empty project folder must be rejected")
	}
	if _, err := resolveProjectDir("relative/path"); err == nil {
		t.Fatal("relative project folder must be rejected")
	}
	if _, err := resolveProjectDir("/"); err == nil {
		t.Fatal("/ must be rejected")
	}
	if _, err := resolveProjectDir(home); err == nil {
		t.Fatal("home directory must be rejected")
	}
	if _, err := resolveProjectDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("nonexistent project folder must be rejected")
	}

	got, err := resolveProjectDir(dir)
	if err != nil {
		t.Fatalf("existing dir should resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if got != want {
		t.Fatalf("resolved = %q, want %q", got, want)
	}
}
