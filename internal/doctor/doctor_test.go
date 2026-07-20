package doctor

import (
	"context"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
)

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

type fakeConfigStore struct {
	cfg *config.File
}

func (s *fakeConfigStore) Load() (*config.File, error) {
	if s.cfg == nil {
		return &config.File{}, nil
	}
	return s.cfg, nil
}

type fakeAsana struct{}

func (fakeAsana) CurrentUser(_ context.Context, _ string) (*asana.User, error) {
	return &asana.User{GID: "u1", Name: "Test User"}, nil
}

func (fakeAsana) Project(_ context.Context, _ string, _ string) (*asana.Project, error) {
	return &asana.Project{GID: "p1", Name: "Dharana", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}}, nil
}

func TestDoctorReportsMissingConfiguration(t *testing.T) {
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Asana:  fakeAsana{},
		Config: &fakeConfigStore{cfg: &config.File{}},
	}

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.OK {
		t.Fatal("expected doctor to fail")
	}
	if len(result.Checks) != 4 {
		t.Fatalf("expected four checks, got %#v", result.Checks)
	}
	if result.Checks[2].Code != "PROJECT_NOT_CONFIGURED" {
		t.Fatalf("expected missing project, got %#v", result.Checks[2])
	}
	if result.Checks[3].Code != "TASK_TYPES_NOT_CONFIGURED" {
		t.Fatalf("expected missing task types, got %#v", result.Checks[3])
	}
}

func TestDoctorRepairPlanIncludesActionableSteps(t *testing.T) {
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Asana:  fakeAsana{},
		Config: &fakeConfigStore{cfg: &config.File{}},
	}

	result, err := service.RunWithOptions(context.Background(), true, true)
	if err != nil {
		t.Fatalf("RunWithOptions returned error: %v", err)
	}
	if len(result.RepairPlan) == 0 {
		t.Fatalf("expected repair plan, got %#v", result)
	}
	if result.CapabilitySchema != "mvp-plus-4" {
		t.Fatalf("expected capability schema, got %#v", result)
	}
}

func TestDoctorPassesWhenProjectAndTaskTypesAreConfigured(t *testing.T) {
	service := &Service{
		Auth:  &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Asana: fakeAsana{},
		Config: &fakeConfigStore{cfg: &config.File{
			ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Dharana"},
			TaskTypes: config.TaskTypes{
				Epic:  "Epic",
				Story: "Story",
				Bug:   "Bug",
				Spike: "Spike",
			},
		}},
	}

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected doctor to pass, got %#v", result.Checks)
	}
}
