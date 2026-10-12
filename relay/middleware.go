package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const userIDKey ctxKey = iota

// sessionStore looks up an active session by the hash of its token.
type sessionStore interface {
	sessionUserID(ctx context.Context, tokenHash string, now time.Time) (string, error)
}

type dbSessionStore struct{ db *sql.DB }

func (s dbSessionStore) sessionUserID(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM sessions
		 WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > $2`,
		tokenHash, now).Scan(&userID)
	if err != nil {
		return "", err
	}
	return userID, nil
}

var errNoSession = errors.New("no active session")

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
}

// requireAuth rejects requests without a valid session token and puts the
// authenticated user ID into the request context.
func requireAuth(store sessionStore, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		userID, err := store.sessionUserID(r.Context(), hashToken(raw), time.Now().UTC())
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next(w, r.WithContext(ctx))
	}
}

// userIDFromContext returns the authenticated user ID, or "" if not set.
func userIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}
