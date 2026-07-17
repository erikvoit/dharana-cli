package asana

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurrentUserSendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"gid":"123","name":"Test User","email":"test@example.com"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	user, err := client.CurrentUser(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("CurrentUser returned error: %v", err)
	}
	if user.GID != "123" || user.Name != "Test User" {
		t.Fatalf("unexpected user: %#v", user)
	}
}

func TestCurrentUserReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Not Authorized"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.CurrentUser(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", apiErr.StatusCode)
	}
}
