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
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/project"
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

type cliProjectAsana struct {
	projects []asana.Project
	project  *asana.Project
}

func (c *cliProjectAsana) CurrentUser(_ context.Context, _ string) (*asana.User, error) {
	return &asana.User{GID: "u1", Name: "Test User"}, nil
}

func (c *cliProjectAsana) Projects(_ context.Context, _ string, _ string) ([]asana.Project, error) {
	return c.projects, nil
}

func (c *cliProjectAsana) Project(_ context.Context, _ string, _ string) (*asana.Project, error) {
	return c.project, nil
}

func TestProjectSelectAmbiguousNameReturnsJSONCandidates(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		project: &project.Service{
			Auth:   authService,
			Config: &config.Store{Path: t.TempDir() + "/config.json"},
			Asana: &cliProjectAsana{projects: []asana.Project{
				{GID: "p1", Name: "Dharana", Workspace: asana.Workspace{GID: "w1", Name: "One"}},
				{GID: "p2", Name: "Dharana", Workspace: asana.Workspace{GID: "w2", Name: "Two"}},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"project", "select", "--name", "Dharana", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout, got %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), `"code": "AMBIGUOUS_PROJECT"`) {
		t.Fatalf("expected ambiguous project JSON, got %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), `"candidates"`) || !strings.Contains(stderr.String(), `"workspace_name": "Two"`) {
		t.Fatalf("expected candidate details, got %s", stderr.String())
	}
}
