package work

import (
	"context"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
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

type fakeAsana struct {
	matches []asana.Task
	created *asana.Task
	input   asana.CreateTaskInput
}

func (f *fakeAsana) TasksByName(_ context.Context, _ string, _ string, _ string, _ string) ([]asana.Task, error) {
	return f.matches, nil
}

func (f *fakeAsana) CreateTask(_ context.Context, _ string, input asana.CreateTaskInput) (*asana.Task, error) {
	f.input = input
	if f.created == nil {
		return &asana.Task{GID: "new", Name: input.Name}, nil
	}
	return f.created, nil
}

func TestCreateEpicDryRunDoesNotCreateTask(t *testing.T) {
	client := &fakeAsana{}
	service := newTestService(client)

	result, err := service.CreateEpic(context.Background(), CreateEpicOptions{Name: "Card provisioning", DryRun: true})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	if result.Epic.Created {
		t.Fatal("dry run should not create")
	}
	if client.input.Name != "" {
		t.Fatalf("dry run called CreateTask: %#v", client.input)
	}
	if result.Epic.TypeMapping != "Epic" {
		t.Fatalf("unexpected type mapping: %#v", result.Epic)
	}
}

func TestCreateEpicRejectsDuplicateExactName(t *testing.T) {
	service := newTestService(&fakeAsana{matches: []asana.Task{{GID: "1", Name: "Card provisioning"}}})

	_, err := service.CreateEpic(context.Background(), CreateEpicOptions{Name: "Card provisioning"})
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	appErr, ok := err.(*output.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "DUPLICATE_EPIC" {
		t.Fatalf("expected DUPLICATE_EPIC, got %q", appErr.Code)
	}
}

func TestCreateEpicIdempotentReturnsExisting(t *testing.T) {
	service := newTestService(&fakeAsana{matches: []asana.Task{{GID: "1", Name: "Card provisioning", Permalink: "https://example.test/1"}}})

	result, err := service.CreateEpic(context.Background(), CreateEpicOptions{Name: "Card provisioning", Idempotent: true})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	if !result.Epic.IdempotentExisting {
		t.Fatal("expected existing idempotent result")
	}
	if result.Epic.GID != "1" {
		t.Fatalf("unexpected epic: %#v", result.Epic)
	}
}

func TestCreateEpicCreatesTopLevelProjectTask(t *testing.T) {
	client := &fakeAsana{created: &asana.Task{GID: "123", Name: "Card provisioning", Permalink: "https://example.test/123"}}
	service := newTestService(client)

	result, err := service.CreateEpic(context.Background(), CreateEpicOptions{Name: "Card provisioning", Notes: "notes"})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	if !result.Epic.Created {
		t.Fatal("expected created result")
	}
	if client.input.ProjectGID != "p1" || client.input.WorkspaceGID != "w1" {
		t.Fatalf("unexpected create input: %#v", client.input)
	}
	if client.input.Notes != "notes" {
		t.Fatalf("unexpected notes: %#v", client.input)
	}
	if client.input.CustomFields["field1"] != "Epic" {
		t.Fatalf("unexpected custom fields: %#v", client.input.CustomFields)
	}
}

func newTestService(client *fakeAsana) *Service {
	return &Service{
		Auth:  &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Asana: client,
		Config: &fakeConfigStore{cfg: &config.File{
			ActiveProject: &config.ProjectConfig{
				GID:           "p1",
				Name:          "Project",
				WorkspaceGID:  "w1",
				WorkspaceName: "Workspace",
			},
			TaskTypes: config.TaskTypes{FieldGID: "field1", Epic: "Epic"},
		}},
	}
}
