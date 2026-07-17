package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
)

type testStore struct {
	token string
}

func (s *testStore) Save(token string) error {
	s.token = token
	return nil
}

func (s *testStore) Load() (string, error) {
	if s.token == "" {
		return "", auth.ErrTokenNotFound
	}
	return s.token, nil
}

func (s *testStore) Delete() error {
	s.token = ""
	return nil
}

type testAsana struct {
	user *asana.User
	err  error
}

func (c *testAsana) CurrentUser(_ context.Context, _ string) (*asana.User, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.user, nil
}

func TestAuthConfigureJSONDoesNotPrintFullToken(t *testing.T) {
	store := &testStore{}
	app := &app{auth: &auth.Service{
		Store: store,
		Asana: &testAsana{user: &asana.User{GID: "123", Name: "Test User"}},
	}}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"auth", "configure", "--token", "asana_pat_1234567890", "--json", "--validate"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "asana_pat_1234567890") {
		t.Fatalf("full token leaked in stdout: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"ok": true`) {
		t.Fatalf("expected ok JSON envelope: %s", stdout.String())
	}
	if store.token != "asana_pat_1234567890" {
		t.Fatal("token was not saved")
	}
}

func TestAuthValidateJSONInvalidAuth(t *testing.T) {
	app := &app{auth: &auth.Service{
		Store: &testStore{token: "bad-token"},
		Asana: &testAsana{err: &asana.APIError{StatusCode: http.StatusUnauthorized}},
	}}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"auth", "validate", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout, got %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `"ok": false`) || !strings.Contains(stderr.String(), `"code": "INVALID_AUTH"`) {
		t.Fatalf("expected INVALID_AUTH JSON, got %s", stderr.String())
	}
	if strings.Contains(stderr.String(), "bad-token") {
		t.Fatalf("full token leaked in stderr: %s", stderr.String())
	}
}

func TestAuthValidateMissingToken(t *testing.T) {
	app := &app{auth: &auth.Service{
		Store: &testStore{},
		Asana: &testAsana{err: errors.New("should not be called")},
	}}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"auth", "validate", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "TOKEN_NOT_CONFIGURED"`) {
		t.Fatalf("expected TOKEN_NOT_CONFIGURED JSON, got %s", stderr.String())
	}
}
