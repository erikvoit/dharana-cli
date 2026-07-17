package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type memoryStore struct {
	token string
	err   error
}

func (s *memoryStore) Save(token string) error {
	s.token = token
	return s.err
}

func (s *memoryStore) Load() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.token == "" {
		return "", ErrTokenNotFound
	}
	return s.token, nil
}

func (s *memoryStore) Delete() error {
	s.token = ""
	return s.err
}

type fakeAsana struct {
	user *asana.User
	err  error
	got  string
}

func (f *fakeAsana) CurrentUser(_ context.Context, token string) (*asana.User, error) {
	f.got = token
	if f.err != nil {
		return nil, f.err
	}
	return f.user, nil
}

func TestMaskTokenDoesNotExposeFullSecret(t *testing.T) {
	token := "asana_pat_1234567890"
	masked := MaskToken(token)

	if masked == token {
		t.Fatal("mask returned full token")
	}
	if masked != "asan************7890" {
		t.Fatalf("unexpected mask: %q", masked)
	}
}

func TestResolveTokenPrefersDharanaEnvOverKeychain(t *testing.T) {
	t.Setenv(EnvDharanaPAT, "env-token")
	t.Setenv(EnvAsanaToken, "asana-env-token")

	service := &Service{Store: &memoryStore{token: "keychain-token"}}
	resolved, err := service.ResolveToken()
	if err != nil {
		t.Fatalf("ResolveToken returned error: %v", err)
	}
	if resolved.Token != "env-token" {
		t.Fatalf("expected env token, got %q", resolved.Token)
	}
	if resolved.Source != "env:"+EnvDharanaPAT {
		t.Fatalf("unexpected source: %q", resolved.Source)
	}
}

func TestValidateMapsUnauthorizedToStructuredInvalidAuth(t *testing.T) {
	service := &Service{
		Store: &memoryStore{token: "bad-token"},
		Asana: &fakeAsana{err: &asana.APIError{StatusCode: http.StatusUnauthorized}},
	}

	_, err := service.Validate(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	var appErr *output.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "INVALID_AUTH" {
		t.Fatalf("expected INVALID_AUTH, got %q", appErr.Code)
	}
}

func TestConfigureStoresTokenAndValidationReturnsMaskedToken(t *testing.T) {
	store := &memoryStore{}
	client := &fakeAsana{user: &asana.User{GID: "123", Name: "Test User", Email: "test@example.com"}}
	service := &Service{Store: store, Asana: client}

	result, err := service.Configure(context.Background(), "asana_pat_1234567890", true)
	if err != nil {
		t.Fatalf("Configure returned error: %v", err)
	}
	if store.token != "asana_pat_1234567890" {
		t.Fatal("token was not stored")
	}
	if client.got != "asana_pat_1234567890" {
		t.Fatal("token was not validated")
	}
	if result.Token.Masked == "asana_pat_1234567890" {
		t.Fatal("full token leaked in result")
	}
	if !result.Validated {
		t.Fatal("expected result to be validated")
	}
	if result.User == nil || result.User.GID != "123" {
		t.Fatalf("unexpected user: %#v", result.User)
	}
}
