package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeManager answers getCurrentSession like 629Manager: one valid account
// token, one guest token, everything else 401.
func fakeManager(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions/current" || r.Header.Get("X-Service-Key") != "test-key" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("Authorization") {
		case "Bearer account-token":
			w.Write([]byte(`{"expiresAt":"2030-01-01T00:00:00Z","principal":{"principalId":"6f1c2e1a-0000-4000-8000-000000000001","type":"account","username":"kosmolit","role":"admin","createdAt":"2026-01-01T00:00:00Z"}}`))
		case "Bearer guest-token":
			w.Write([]byte(`{"expiresAt":"2030-01-01T00:00:00Z","principal":{"principalId":"6f1c2e1a-0000-4000-8000-000000000002","type":"guest","username":null,"role":"user","createdAt":"2026-01-01T00:00:00Z"}}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"title":"Unauthorized","status":401}`))
		}
	}))
	t.Cleanup(srv.Close)

	oldURL, oldKey := managerBaseURL, managerKey
	managerBaseURL, managerKey = srv.URL, "test-key"
	t.Cleanup(func() { managerBaseURL, managerKey = oldURL, oldKey })
}

func TestResolveSessionAccount(t *testing.T) {
	fakeManager(t)
	id, err := resolveSession(context.Background(), "account-token")
	if err != nil {
		t.Fatal(err)
	}
	p := id.Principal
	if p.Type != "account" || p.Username == nil || *p.Username != "kosmolit" || p.Role != "admin" {
		t.Fatalf("unexpected principal %+v", p)
	}
	if id.ExpiresAt.Year() != 2030 {
		t.Fatalf("expiresAt = %v", id.ExpiresAt)
	}
}

func TestResolveSessionInvalidToken(t *testing.T) {
	fakeManager(t)
	if _, err := resolveSession(context.Background(), "stale-token"); !errors.Is(err, errInvalidSession) {
		t.Fatalf("err = %v, want errInvalidSession", err)
	}
}

func TestResolveSessionWrongServiceKeyIsNotALogout(t *testing.T) {
	fakeManager(t)
	managerKey = "wrong-key"
	_, err := resolveSession(context.Background(), "account-token")
	if err == nil || errors.Is(err, errInvalidSession) {
		t.Fatalf("err = %v, want a Manager error distinct from an invalid session", err)
	}
}

// Guests are refused before the DB is touched, so a nil *sql.DB is fine.
func TestGuestsCannotPlay(t *testing.T) {
	fakeManager(t)
	if _, _, err := userFromToken(context.Background(), nil, "guest-token"); !errors.Is(err, errNotAccount) {
		t.Fatalf("err = %v, want errNotAccount", err)
	}
}

func TestNoTokenIsNotLoggedIn(t *testing.T) {
	if _, _, err := userFromToken(context.Background(), nil, ""); !errors.Is(err, errNoSession) {
		t.Fatalf("err = %v, want errNoSession", err)
	}
}
