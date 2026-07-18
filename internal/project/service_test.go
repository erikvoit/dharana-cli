package project

import (
	"context"
	"errors"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type fakeStore struct {
	cfg *config.File
}

func (s *fakeStore) Load() (*config.File, error) {
	if s.cfg == nil {
		return &config.File{}, nil
	}
	return s.cfg, nil
}

func (s *fakeStore) Save(cfg *config.File) error {
	s.cfg = cfg
	return nil
}

type fakeTokenStore struct {
	token string
}

func (s *fakeTokenStore) Save(token string) error {
	s.token = token
	return nil
}

func (s *fakeTokenStore) Load() (string, error) {
	if s.token == "" {
		return "", auth.ErrTokenNotFound
	}
	return s.token, nil
}

func (s *fakeTokenStore) Delete() error {
	s.token = ""
	return nil
}

type fakeAsana struct {
	projects []asana.Project
	project  *asana.Project
	fields   []asana.CustomFieldSetting
}

func (f *fakeAsana) CurrentUser(_ context.Context, _ string) (*asana.User, error) {
	return &asana.User{GID: "u1", Name: "User"}, nil
}

func (f *fakeAsana) Projects(_ context.Context, _ string, _ string) ([]asana.Project, error) {
	return f.projects, nil
}

func (f *fakeAsana) Project(_ context.Context, _ string, _ string) (*asana.Project, error) {
	if f.project == nil {
		return nil, errors.New("not found")
	}
	return f.project, nil
}

func (f *fakeAsana) CreateProject(_ context.Context, _ string, input asana.CreateProjectInput) (*asana.Project, error) {
	return &asana.Project{GID: "created", Name: input.Name, Workspace: asana.Workspace{GID: input.WorkspaceGID}}, nil
}

func (f *fakeAsana) InstantiateProjectTemplate(_ context.Context, _ string, templateGID string, _ string) (*asana.ProjectTemplateJob, error) {
	return &asana.ProjectTemplateJob{GID: templateGID, Status: "running"}, nil
}

func (f *fakeAsana) CustomFieldSettingsForProject(_ context.Context, _ string, _ string) ([]asana.CustomFieldSetting, error) {
	return f.fields, nil
}

func (f *fakeAsana) ProjectMemberships(_ context.Context, _ string, _ string) ([]asana.ProjectMembership, error) {
	return nil, nil
}

func (f *fakeAsana) User(_ context.Context, _ string, userGID string) (*asana.User, error) {
	return &asana.User{GID: userGID, Name: "User"}, nil
}

func (f *fakeAsana) Users(_ context.Context, _ string, _ string) ([]asana.User, error) {
	return nil, nil
}

func (f *fakeAsana) AddProjectMembers(_ context.Context, _ string, _ string, _ []string) error {
	return nil
}

func (f *fakeAsana) RemoveProjectMembers(_ context.Context, _ string, _ string, _ []string) error {
	return nil
}

func TestSelectByNameRejectsAmbiguousProjects(t *testing.T) {
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Config: &fakeStore{},
		Asana: &fakeAsana{projects: []asana.Project{
			{GID: "1", Name: "Delivery", Workspace: asana.Workspace{GID: "w1", Name: "One"}},
			{GID: "2", Name: "Delivery", Workspace: asana.Workspace{GID: "w2", Name: "Two"}},
		}},
	}

	_, err := service.Select(context.Background(), SelectOptions{Name: "Delivery"})
	if err == nil {
		t.Fatal("expected error")
	}
	appErr, ok := err.(*output.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "AMBIGUOUS_PROJECT" {
		t.Fatalf("expected AMBIGUOUS_PROJECT, got %q", appErr.Code)
	}
	candidates, ok := appErr.Candidates.([]ProjectValue)
	if !ok || len(candidates) != 2 {
		t.Fatalf("expected two candidates, got %#v", appErr.Candidates)
	}
}

func TestSelectByGIDSavesActiveProject(t *testing.T) {
	store := &fakeStore{}
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Config: store,
		Asana:  &fakeAsana{project: &asana.Project{GID: "123", Name: "Dharana", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}}},
	}

	result, err := service.Select(context.Background(), SelectOptions{GID: "123"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if result.ActiveProject.GID != "123" {
		t.Fatalf("unexpected selected project: %#v", result.ActiveProject)
	}
	if store.cfg == nil || store.cfg.ActiveProject == nil {
		t.Fatal("active project was not saved")
	}
	if store.cfg.ActiveProject.Name != "Dharana" {
		t.Fatalf("unexpected saved project: %#v", store.cfg.ActiveProject)
	}
}

func TestAdoptDryRunDiscoversMappingsWithoutSaving(t *testing.T) {
	store := &fakeStore{}
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Config: store,
		Asana: &fakeAsana{
			project: &asana.Project{GID: "123", Name: "Payments", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}},
			fields: []asana.CustomFieldSetting{{
				CustomField: asana.CustomField{
					GID:  "field-type",
					Name: "Work Type",
					Type: "enum",
					EnumOptions: []asana.EnumOption{
						{GID: "opt-epic", Name: "Epic", Enabled: true},
						{GID: "opt-story", Name: "Story", Enabled: true},
						{GID: "opt-bug", Name: "Bug", Enabled: true},
						{GID: "opt-spike", Name: "Spike", Enabled: true},
					},
				},
			}},
		},
	}

	result, err := service.Adopt(context.Background(), AdoptOptions{Ref: "123", DryRun: true})
	if err != nil {
		t.Fatalf("Adopt returned error: %v", err)
	}
	if result.Applied {
		t.Fatal("dry-run adoption should not apply")
	}
	if store.cfg != nil {
		t.Fatalf("dry-run adoption saved config: %#v", store.cfg)
	}
	if result.ProposedConfig.TaskTypes.Epic != "opt-epic" {
		t.Fatalf("expected discovered option GID, got %#v", result.ProposedConfig.TaskTypes)
	}
}

func TestInspectReportsMissingMappings(t *testing.T) {
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Config: &fakeStore{cfg: &config.File{}},
		Asana:  &fakeAsana{project: &asana.Project{GID: "123", Name: "Payments", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}}},
	}

	result, err := service.Inspect(context.Background(), "123")
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if result.Ready {
		t.Fatal("inspect should not be ready without mappings")
	}
	if len(result.Problems) == 0 || result.Problems[0].Code != "TASK_TYPES_NOT_CONFIGURED" {
		t.Fatalf("expected task type problem, got %#v", result.Problems)
	}
}
