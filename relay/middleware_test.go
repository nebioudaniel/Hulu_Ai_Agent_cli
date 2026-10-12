package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeStore struct {
	userID  string
	err     error
	gotHash string
}

func (f *fakeStore) sessionUserID(_ context.Context, tokenHash string, _ time.Time) (string, error) {
	f.gotHash = tokenHash
	return f.userID, f.err
}

func TestRequireAuthMissingToken(t *testing.T) {
	h := requireAuth(&fakeStore{}, func(w http.ResponseWriter, r *http.Request) {})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/command", nil)

	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuthGoodTokenSetsUser(t *testing.T) {
	store := &fakeStore{userID: "user-1"}
	var gotUser string
	h := requireAuth(store, func(w http.ResponseWriter, r *http.Request) {
		gotUser = userIDFromContext(r.Context())
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/command", nil)
	req.Header.Set("Authorization", "Bearer raw-token")

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotUser != "user-1" {
		t.Fatalf("expected user-1 in context, got %q", gotUser)
	}
	if store.gotHash != hashToken("raw-token") {
		t.Fatal("middleware did not hash the token before lookup")
	}
}

func TestRequireAuthBadToken(t *testing.T) {
	store := &fakeStore{err: errors.New("no row")}
	h := requireAuth(store, func(w http.ResponseWriter, r *http.Request) {})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/command", nil)
	req.Header.Set("Authorization", "Bearer nope")

	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
