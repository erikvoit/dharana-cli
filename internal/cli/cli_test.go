package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	planpkg "github.com/erikvoit/dharana-cli/internal/plan"
	"github.com/erikvoit/dharana-cli/internal/project"
	"github.com/erikvoit/dharana-cli/internal/refcache"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type testStore struct {
	token string
}

type testProfileStore struct{ state *auth.ProfileState }

func (s *testProfileStore) Load() (*auth.ProfileState, error)   { return s.state, nil }
func (s *testProfileStore) Save(value *auth.ProfileState) error { s.state = value; return nil }

type testCredentialStore struct{ values map[string]auth.Credential }

func (s *testCredentialStore) SaveCredential(name string, value auth.Credential) error {
	if s.values == nil {
		s.values = map[string]auth.Credential{}
	}
	s.values[name] = value
	return nil
}
func (s *testCredentialStore) LoadCredential(name string) (auth.Credential, error) {
	value, ok := s.values[name]
	if !ok {
		return auth.Credential{}, auth.ErrTokenNotFound
	}
	return value, nil
}
func (s *testCredentialStore) DeleteCredential(name string) error { delete(s.values, name); return nil }

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
	if code != 3 {
		t.Fatalf("expected exit 3, got %d", code)
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
	if code != 3 {
		t.Fatalf("expected exit 3, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "TOKEN_NOT_CONFIGURED"`) {
		t.Fatalf("expected TOKEN_NOT_CONFIGURED JSON, got %s", stderr.String())
	}
}

func TestAuthValidateAPIFailureExitCode(t *testing.T) {
	app := &app{auth: &auth.Service{
		Store: &testStore{token: "token"},
		Asana: &testAsana{err: &asana.APIError{StatusCode: http.StatusInternalServerError}},
	}}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"auth", "validate", "--json"}, &stdout, &stderr)
	if code != 5 {
		t.Fatalf("expected exit 5, got %d", code)
	}
	if !strings.Contains(stderr.String(), `"code": "ASANA_REQUEST_FAILED"`) {
		t.Fatalf("expected ASANA_REQUEST_FAILED JSON, got %s", stderr.String())
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

func (c *cliProjectAsana) CreateProject(_ context.Context, _ string, input asana.CreateProjectInput) (*asana.Project, error) {
	return &asana.Project{GID: "created", Name: input.Name, Workspace: asana.Workspace{GID: input.WorkspaceGID}}, nil
}

func (c *cliProjectAsana) InstantiateProjectTemplate(_ context.Context, _ string, templateGID string, _ string) (*asana.ProjectTemplateJob, error) {
	return &asana.ProjectTemplateJob{GID: templateGID, Status: "running"}, nil
}

func (c *cliProjectAsana) CustomFieldSettingsForProject(_ context.Context, _ string, _ string) ([]asana.CustomFieldSetting, error) {
	return nil, nil
}

func (c *cliProjectAsana) ProjectMemberships(_ context.Context, _ string, _ string) ([]asana.ProjectMembership, error) {
	return nil, nil
}

func (c *cliProjectAsana) User(_ context.Context, _ string, userGID string) (*asana.User, error) {
	return &asana.User{GID: userGID, Name: "Test User"}, nil
}

func (c *cliProjectAsana) Users(_ context.Context, _ string, _ string) ([]asana.User, error) {
	return nil, nil
}

func (c *cliProjectAsana) AddProjectMembers(_ context.Context, _ string, _ string, _ []string) error {
	return nil
}

func (c *cliProjectAsana) RemoveProjectMembers(_ context.Context, _ string, _ string, _ []string) error {
	return nil
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
	if code != 4 {
		t.Fatalf("expected exit 4, got %d", code)
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

func TestCapabilitiesAndCommandHelpDoNotRequireAuth(t *testing.T) {
	app := &app{auth: &auth.Service{Store: &testStore{}}}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"capabilities", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected capabilities exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"operation": "capabilities"`) || !strings.Contains(stdout.String(), `"schema_version": "mvp-plus-4"`) || !strings.Contains(stdout.String(), `"name": "work update"`) || !strings.Contains(stdout.String(), `"name": "description-file"`) {
		t.Fatalf("expected capability schema JSON, got %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = app.run(context.Background(), []string{"help", "work ready", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected help exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name": "work ready"`) || !strings.Contains(stdout.String(), `"requires_project": true`) {
		t.Fatalf("expected command help JSON, got %s", stdout.String())
	}
}

func TestPlanSchemaAndLocalValidationDoNotRequireAuth(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := (&app{}).run(context.Background(), []string{"plan", "schema", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected plan schema exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"operation": "plan.schema"`) || !strings.Contains(stdout.String(), `"const": "EpicPlan"`) {
		t.Fatalf("expected canonical plan schema JSON, got %s", stdout.String())
	}

	path := filepath.Join(t.TempDir(), "example.yaml")
	data := []byte("apiVersion: dharana.dev/v1alpha1\nkind: EpicPlan\nmetadata:\n  id: example\n  context: payments\nspec:\n  epic:\n    id: epic\n    name: Example\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = (&app{}).run(context.Background(), []string{"plan", "validate", path, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected local plan validation exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"operation": "plan.validate"`) || !strings.Contains(stdout.String(), `"valid": true`) {
		t.Fatalf("expected valid local plan JSON, got %s", stdout.String())
	}
}

func TestProjectScopedCommandRejectsProfileContextMismatch(t *testing.T) {
	profiles := &testProfileStore{state: &auth.ProfileState{SchemaVersion: auth.ProfileSchemaVersion, Active: "personal", Profiles: []auth.Profile{{Name: "personal", Provider: auth.ProviderOAuth, ScopeKnown: true, Scopes: []string{"projects:read", "tasks:read", "users:read"}, ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}}
	credentials := &testCredentialStore{values: map[string]auth.Credential{"personal": {AccessToken: "secret", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)}}}
	authService := &auth.Service{Profiles: profiles, Credentials: credentials, SelectedProfile: "personal"}
	store := &config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := store.Save(&config.File{ActiveContext: "payments", ActiveProject: &config.ProjectConfig{GID: "p1"}, Contexts: []config.Context{{Name: "payments", Project: config.ProjectConfig{GID: "p1"}, AuthProfile: "work", UserGID: "u1"}}}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := (&app{auth: authService, config: store}).run(context.Background(), []string{"work", "list", "--json"}, &stdout, &stderr)
	if code != 3 || !strings.Contains(stderr.String(), `"code": "AUTH_CONTEXT_MISMATCH"`) {
		t.Fatalf("expected profile mismatch, code=%d stderr=%s", code, stderr.String())
	}
}

func TestCommandsRequireExplicitStateMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	application := &app{config: &config.Store{Path: path}}
	var stdout, stderr bytes.Buffer
	code := application.run(context.Background(), []string{"context", "list", "--json"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), `"code": "STATE_MIGRATION_REQUIRED"`) {
		t.Fatalf("expected migration requirement, code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = application.run(context.Background(), []string{"migrate", "apply", "--json"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"applied": true`) {
		t.Fatalf("expected migration apply, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = application.run(context.Background(), []string{"context", "list", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected command after migration, code=%d stderr=%s", code, stderr.String())
	}
}

func TestPlanServiceUsesExplicitManifestProjectGID(t *testing.T) {
	store := &config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := store.Save(&config.File{ActiveProject: &config.ProjectConfig{GID: "active-project", Name: "Active", WorkspaceGID: "workspace-1"}}); err != nil {
		t.Fatal(err)
	}
	manifest := &planpkg.Manifest{Spec: planpkg.Spec{Project: "manifest-project"}}
	service := (&app{config: store}).planService(manifest)
	cfg, err := service.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActiveProject == nil || cfg.ActiveProject.GID != "manifest-project" || cfg.ActiveContext != "" {
		t.Fatalf("expected explicit manifest project override, got %#v", cfg)
	}
}

func TestPlanServiceDoesNotMutateSharedWorkServiceConfig(t *testing.T) {
	store := &config.Store{Path: filepath.Join(t.TempDir(), "config.json")}
	if err := store.Save(&config.File{ActiveProject: &config.ProjectConfig{GID: "active-project"}}); err != nil {
		t.Fatal(err)
	}
	shared := &work.Service{Config: store}
	manifest := &planpkg.Manifest{Spec: planpkg.Spec{Project: "manifest-project"}}
	service := (&app{config: store, work: shared}).planService(manifest)
	planWork, ok := service.Work.(*work.Service)
	if !ok {
		t.Fatalf("expected plan work service, got %T", service.Work)
	}
	if planWork == shared {
		t.Fatal("expected plan service to use a copied work service")
	}
	sharedConfig, err := shared.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if sharedConfig.ActiveProject == nil || sharedConfig.ActiveProject.GID != "active-project" {
		t.Fatalf("shared work service config was mutated: %#v", sharedConfig)
	}
}

func TestLoadMarkdownDescriptionValidatesFileContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "description.md")
	if err := os.WriteFile(path, []byte("## Criteria\n\n- **Works**\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	description, err := loadMarkdownDescription(path)
	if err != nil || description == nil || description.Format != "markdown" || !strings.Contains(description.Content, "**Works**") {
		t.Fatalf("unexpected description: %#v err=%v", description, err)
	}
	badPath := filepath.Join(t.TempDir(), "bad.md")
	if err := os.WriteFile(badPath, []byte("<script>bad</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMarkdownDescription(badPath); err == nil {
		t.Fatal("expected raw HTML description to fail")
	}
}

func TestContextCreateReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	cfgStore := &config.Store{Path: t.TempDir() + "/config.json"}
	app := &app{
		auth:   authService,
		config: cfgStore,
		project: &project.Service{
			Auth:   authService,
			Config: cfgStore,
			Asana:  &cliProjectAsana{project: &asana.Project{GID: "123", Name: "Payments", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"context", "create", "payments", "--project", "123", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected context create exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"operation": "context.create"`) {
		t.Fatalf("expected context create JSON, got %s", stdout.String())
	}
}

func TestContextUseSelectsExistingContext(t *testing.T) {
	cfgStore := &config.Store{Path: t.TempDir() + "/config.json"}
	err := cfgStore.Save(&config.File{Contexts: []config.Context{{Name: "payments", Project: config.ProjectConfig{GID: "123", Name: "Payments", WorkspaceGID: "w1", WorkspaceName: "Workspace"}}}})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	app := &app{config: cfgStore}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"context", "use", "payments", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected context use exit 0, got %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name": "payments"`) {
		t.Fatalf("expected context JSON, got %s", stdout.String())
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

func (c *cliWorkAsana) UpdateTask(_ context.Context, _ string, gid string, input asana.UpdateTaskInput) (*asana.Task, error) {
	task, _ := c.Task(context.Background(), "", gid)
	if input.Name != nil {
		task.Name = *input.Name
	}
	if input.Notes != nil {
		task.Notes = *input.Notes
	}
	if input.Completed != nil {
		task.Completed = *input.Completed
	}
	if input.DueOn != nil {
		task.DueOn = *input.DueOn
	}
	return task, nil
}

func (c *cliWorkAsana) AddTaskToProject(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (c *cliWorkAsana) SetParent(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (c *cliWorkAsana) AddStory(_ context.Context, _ string, _ string, _ string) (*asana.Story, error) {
	return &asana.Story{GID: "comment1"}, nil
}

func (c *cliWorkAsana) User(_ context.Context, _ string, userGID string) (*asana.User, error) {
	return &asana.User{GID: userGID, Name: "Test User"}, nil
}

func (c *cliWorkAsana) Users(_ context.Context, _ string, _ string) ([]asana.User, error) {
	return []asana.User{{GID: "u1", Name: "Test User", Email: "dev@example.com"}}, nil
}

func (c *cliWorkAsana) CustomFieldSettingsForProject(_ context.Context, _ string, _ string) ([]asana.CustomFieldSetting, error) {
	return nil, nil
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
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
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

func TestTaskCreateIdempotencyKeyReturnsExistingJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{
				task: &asana.Task{GID: "456", Name: "Parent Bug"},
				subtasks: map[string]*asana.TaskPage{
					"456": &asana.TaskPage{Tasks: []asana.Task{{
						GID:  "existing-task",
						Name: "Normalize persistence",
					}}},
				},
			},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"task", "create", "--parent", "456", "--idempotency-key", "retry-1", "Normalize persistence", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"idempotency_key": "retry-1"`) || !strings.Contains(stdout.String(), `"idempotent_existing": true`) {
		t.Fatalf("expected idempotent task JSON, got %s", stdout.String())
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

func TestDependencyAddDryRunReturnsJSON(t *testing.T) {
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

	code := app.run(context.Background(), []string{"dependency", "add", "111", "--blocked-by", "222", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"operation": "dependency.add"`) || !strings.Contains(stdout.String(), `"dry_run": true`) || !strings.Contains(stdout.String(), `"added": false`) {
		t.Fatalf("expected dry-run dependency JSON, got %s", stdout.String())
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
	if !strings.Contains(stdout.String(), `"operation": "work.list"`) || !strings.Contains(stdout.String(), `"type": "story"`) || !strings.Contains(stdout.String(), `"next_offset": "next"`) {
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

func TestWorkBlockedReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{page: &asana.TaskPage{
				Tasks: []asana.Task{{
					GID:          "story1",
					Name:         "Blocked Story",
					Dependencies: []asana.TaskSummary{{GID: "bug1", Name: "Blocker"}},
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
			Refs: &cliRefStore{cache: refcache.Cache{Items: []refcache.Entry{{
				Ref:  "BUG:Blocker",
				GID:  "bug1",
				Name: "Blocker",
				Type: "bug",
			}}}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"work", "blocked", "--type", "story", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"blockers"`) || !strings.Contains(stdout.String(), `"ref": "BUG:Blocker"`) {
		t.Fatalf("expected blocked work JSON, got %s", stdout.String())
	}
}

func TestWorkReadyReturnsJSON(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{page: &asana.TaskPage{
				Tasks: []asana.Task{{
					GID:    "story1",
					Name:   "Ready Story",
					Parent: &asana.TaskParent{GID: "epic1", Name: "Epic"},
					CustomFields: []asana.CustomField{
						{GID: "field1", DisplayValue: "Story"},
						{GID: "priority-field", DisplayValue: "P1"},
						{GID: "component-field", DisplayValue: "Cards"},
					},
				}},
			}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{FieldGID: "field1", Story: "Story"},
				Fields:        config.FieldMappings{PriorityGID: "priority-field", ComponentGID: "component-field"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"work", "ready", "--type", "story", "--priority", "P1", "--component", "cards", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"items"`) || !strings.Contains(stdout.String(), `"ref": "STORY:Ready Story"`) {
		t.Fatalf("expected ready work JSON, got %s", stdout.String())
	}
}

func TestWorkGraphReturnsMermaid(t *testing.T) {
	authService := &auth.Service{Store: &testStore{token: "token"}}
	app := &app{
		auth: authService,
		work: &work.Service{
			Auth: authService,
			Asana: &cliWorkAsana{page: &asana.TaskPage{
				Tasks: []asana.Task{{
					GID:          "story1",
					Name:         "Story",
					Dependencies: []asana.TaskSummary{{GID: "bug1", Name: "Bug"}},
					CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
				}},
			}},
			Config: &testConfigStore{cfg: &config.File{
				ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
				TaskTypes:     config.TaskTypes{FieldGID: "field1", Story: "Story"},
			}},
		},
	}
	var stdout, stderr bytes.Buffer

	code := app.run(context.Background(), []string{"work", "graph", "--format", "mermaid"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "flowchart LR") || strings.Contains(stdout.String(), `"ok"`) {
		t.Fatalf("expected raw Mermaid output, got %s", stdout.String())
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
