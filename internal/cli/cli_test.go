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
	"github.com/erikvoit/dharana-cli/internal/refcache"
	"github.com/erikvoit/dharana-cli/internal/work"
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

type cliWorkAsana struct {
	matches  []asana.Task
	page     *asana.TaskPage
	subtasks map[string]*asana.TaskPage
	task     *asana.Task
	tasks    map[string]*asana.Task
	created  *asana.Task
}

func (c *cliWorkAsana) TasksByName(_ context.Context, _ string, _ string, _ string) ([]asana.Task, error) {
	return c.matches, nil
}

func (c *cliWorkAsana) ProjectTasks(_ context.Context, _ string, _ string, _ int, _ string) (*asana.TaskPage, error) {
	if c.page == nil {
		return &asana.TaskPage{}, nil
	}
	return c.page, nil
}

func (c *cliWorkAsana) Subtasks(_ context.Context, _ string, taskGID string, _ int, _ string) (*asana.TaskPage, error) {
	if c.subtasks == nil || c.subtasks[taskGID] == nil {
		return &asana.TaskPage{}, nil
	}
	return c.subtasks[taskGID], nil
}

func (c *cliWorkAsana) Task(_ context.Context, _ string, gid string) (*asana.Task, error) {
	if c.tasks != nil && c.tasks[gid] != nil {
		return c.tasks[gid], nil
	}
	if c.task == nil {
		return &asana.Task{GID: "epic1", Name: "Epic"}, nil
	}
	return c.task, nil
}

func (c *cliWorkAsana) CreateTask(_ context.Context, _ string, input asana.CreateTaskInput) (*asana.Task, error) {
	if c.created != nil {
		return c.created, nil
	}
	return &asana.Task{GID: "created", Name: input.Name}, nil
}

func (c *cliWorkAsana) AddTaskToProject(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (c *cliWorkAsana) AddDependencies(_ context.Context, _ string, _ string, _ []string) error {
	return nil
}

func (c *cliWorkAsana) RemoveDependencies(_ context.Context, _ string, _ string, _ []string) error {
	return nil
}

func TestEpicCreateDryRunReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth:  authService,
			Asana: &cliWorkAsana{},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{Epic: "Epic"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"epic", "create", "Card provisioning", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dry_run": true`) || !strings.Contains(stdout.String(), `"type_mapping": "Epic"`) {
		t.Fatalf("expected dry-run epic JSON, got %s", stdout.String())
	}
}

func TestEpicCreateDuplicateReturnsJSONCandidates(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth:  authService,
			Asana: &cliWorkAsana{matches: []asana.Task{{GID: "existing", Name: "Card provisioning"}}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{Epic: "Epic"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"epic", "create", "Card provisioning", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "DUPLICATE_EPIC"`) || !strings.Contains(stderr.String(), `"candidates"`) {
		t.Fatalf("expected duplicate JSON candidates, got %s", stderr.String())
	}
}

func TestEpicCreateMissingNameReturnsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (&app{}).run(context.Background(), []string{"epic", "create", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "EPIC_NAME_REQUIRED"`) {
		t.Fatalf("expected missing name JSON error, got %s", stderr.String())
	}
}

func TestStoryCreateDryRunReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth:  authService,
			Asana: &cliWorkAsana{task: &asana.Task{GID: "123", Name: "Parent Epic"}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{Epic: "Epic", Story: "Story"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"story", "create", "--epic", "123", "Recovery story", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"dry_run": true`) || !strings.Contains(stdout.String(), `"type_mapping": "Story"`) {
		t.Fatalf("expected dry-run story JSON, got %s", stdout.String())
	}
}

func TestStoryCreateMissingEpicReturnsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (&app{}).run(context.Background(), []string{"story", "create", "Recovery story", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "EPIC_REFERENCE_REQUIRED"`) {
		t.Fatalf("expected missing epic JSON error, got %s", stderr.String())
	}
}

func TestBugCreateDryRunReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth:  authService,
			Asana: &cliWorkAsana{task: &asana.Task{GID: "123", Name: "Parent Epic"}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{Epic: "Epic", Bug: "Bug"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"bug", "create", "--epic", "123", "--priority", "P1", "--environment", "1841", "Provisioning regression", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"type_mapping": "Bug"`) || !strings.Contains(stdout.String(), `"priority": "P1"`) || !strings.Contains(stdout.String(), `"environment": "1841"`) {
		t.Fatalf("expected dry-run bug JSON, got %s", stdout.String())
	}
}

func TestBugCreateMissingNameReturnsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (&app{}).run(context.Background(), []string{"bug", "create", "--epic", "123", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "BUG_NAME_REQUIRED"`) {
		t.Fatalf("expected missing bug name JSON error, got %s", stderr.String())
	}
}

func TestSpikeCreateDryRunReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth:  authService,
			Asana: &cliWorkAsana{task: &asana.Task{GID: "123", Name: "Parent Epic"}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{Epic: "Epic", Spike: "Spike"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"spike", "create", "--epic", "123", "--timebox", "4h", "Investigate provisioning", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"type_mapping": "Spike"`) || !strings.Contains(stdout.String(), `"timebox": "4h"`) || !strings.Contains(stdout.String(), `"expected_outcomes"`) {
		t.Fatalf("expected dry-run spike JSON, got %s", stdout.String())
	}
}

func TestSpikeCreateMissingNameReturnsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (&app{}).run(context.Background(), []string{"spike", "create", "--epic", "123", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "SPIKE_NAME_REQUIRED"`) {
		t.Fatalf("expected missing spike name JSON error, got %s", stderr.String())
	}
}

func TestTaskCreateDryRunReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth:  authService,
			Asana: &cliWorkAsana{task: &asana.Task{GID: "456", Name: "Parent Bug"}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{Epic: "Epic"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"task", "create", "--parent", "456", "--assignee", "dev@example.com", "--due-on", "2026-07-18", "--estimate", "2h", "Normalize persistence", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"parent"`) || !strings.Contains(stdout.String(), `"assignee": "dev@example.com"`) || !strings.Contains(stdout.String(), `"estimate": "2h"`) {
		t.Fatalf("expected dry-run task JSON, got %s", stdout.String())
	}
}

func TestTaskCreateMissingParentReturnsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := (&app{}).run(context.Background(), []string{"task", "create", "Normalize persistence", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "PARENT_REFERENCE_REQUIRED"`) {
		t.Fatalf("expected missing parent JSON error, got %s", stderr.String())
	}
}

func TestDependencyAddReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{tasks: map[string]*asana.Task{
				"111": {GID: "111", Name: "Blocked"},
				"222": {GID: "222", Name: "Blocker"},
			}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"dependency", "add", "111", "--blocked-by", "222", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"blocked_by"`) || !strings.Contains(stdout.String(), `"added": true`) {
		t.Fatalf("expected dependency JSON, got %s", stdout.String())
	}
}

func TestDependencyRemoveReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{tasks: map[string]*asana.Task{
				"111": {
					GID:          "111",
					Name:         "Blocked",
					Dependencies: []asana.TaskSummary{{GID: "222", Name: "Blocker"}},
				},
				"222": {GID: "222", Name: "Blocker"},
			}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"dependency", "remove", "111", "--blocked-by", "222", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"removed": true`) || !strings.Contains(stdout.String(), `"found": true`) {
		t.Fatalf("expected dependency removal JSON, got %s", stdout.String())
	}
}

func TestWorkListReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{page: &asana.TaskPage{
				NextOffset: "next",
				Tasks: []asana.Task{{
					GID:       "story1",
					Name:      "Story",
					Completed: false,
					CustomFields: []asana.CustomField{{
						GID:          "field1",
						DisplayValue: "Story",
					}},
				}},
			}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{FieldGID: "field1", Story: "Story"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"work", "list", "--type", "story", "--status", "incomplete", "--limit", "10", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"type": "story"`) || !strings.Contains(stdout.String(), `"next_offset": "next"`) {
		t.Fatalf("expected work list JSON, got %s", stdout.String())
	}
}

func TestWorkTreeReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{
				page: &asana.TaskPage{Tasks: []asana.Task{{
					GID:  "epic1",
					Name: "Epic",
					CustomFields: []asana.CustomField{{
						GID:          "field1",
						DisplayValue: "Epic",
					}},
				}}},
				subtasks: map[string]*asana.TaskPage{
					"epic1": &asana.TaskPage{Tasks: []asana.Task{{
						GID:    "story1",
						Name:   "Story",
						Parent: &asana.TaskParent{GID: "epic1", Name: "Epic"},
						CustomFields: []asana.CustomField{{
							GID:          "field1",
							DisplayValue: "Story",
						}},
					}}},
				},
			},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{FieldGID: "field1", Epic: "Epic", Story: "Story"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"work", "tree", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"epics"`) || !strings.Contains(stdout.String(), `"ref": "STORY:Story"`) {
		t.Fatalf("expected work tree JSON, got %s", stdout.String())
	}
}

func TestRefsRefreshReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{page: &asana.TaskPage{
				Tasks: []asana.Task{{
					GID:  "story1",
					Name: "Story",
					CustomFields: []asana.CustomField{{
						GID:          "field1",
						DisplayValue: "Story",
					}},
				}},
			}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{FieldGID: "field1", Story: "Story"},
			}},
			Refs: &cliRefStore{},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"refs", "refresh", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"count": 1`) || !strings.Contains(stdout.String(), `"ref": "STORY:Story"`) {
		t.Fatalf("expected refs refresh JSON, got %s", stdout.String())
	}
}

type cliRefStore struct {
	cache refcache.Cache
}

func (s *cliRefStore) Load() (*refcache.Cache, error) {
	return &s.cache, nil
}

func (s *cliRefStore) Replace(entries []refcache.Entry) (*refcache.Cache, error) {
	s.cache = refcache.Cache{UpdatedAt: "now", Items: entries}
	return &s.cache, nil
}

func (s *cliRefStore) Resolve(ref string) (*refcache.Entry, error) {
	for _, entry := range s.cache.Items {
		if entry.Ref == ref || entry.GID == ref {
			copy := entry
			return &copy, nil
		}
	}
	return nil, refcache.ErrReferenceNotFound
}

type testConfigStore struct {
	cfg *config.File
}

func (s *testConfigStore) Load() (*config.File, error) {
	if s.cfg == nil {
		return &config.File{}, nil
	}
	return s.cfg, nil
}
