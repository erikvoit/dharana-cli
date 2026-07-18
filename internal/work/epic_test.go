package work

import (
	"context"
	"strings"
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
	matches         []asana.Task
	task            *asana.Task
	created         *asana.Task
	input           asana.CreateTaskInput
	addedTaskGID    string
	addedProjectGID string
}

func (f *fakeAsana) TasksByName(_ context.Context, _ string, _ string, _ string) ([]asana.Task, error) {
	return f.matches, nil
}

func (f *fakeAsana) Task(_ context.Context, _ string, _ string) (*asana.Task, error) {
	if f.task == nil {
		return &asana.Task{GID: "epic1", Name: "Epic"}, nil
	}
	return f.task, nil
}

func (f *fakeAsana) CreateTask(_ context.Context, _ string, input asana.CreateTaskInput) (*asana.Task, error) {
	f.input = input
	if f.created == nil {
		return &asana.Task{GID: "new", Name: input.Name}, nil
	}
	return f.created, nil
}

func (f *fakeAsana) AddTaskToProject(_ context.Context, _ string, taskGID string, projectGID string) error {
	f.addedTaskGID = taskGID
	f.addedProjectGID = projectGID
	return nil
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
			TaskTypes: config.TaskTypes{FieldGID: "field1", Epic: "Epic", Story: "Story", Bug: "Bug", Spike: "Spike"},
		}},
	}
}

func TestCreateStoryDryRunResolvesEpic(t *testing.T) {
	client := &fakeAsana{task: &asana.Task{GID: "123", Name: "Parent Epic"}}
	service := newTestService(client)

	result, err := service.CreateStory(context.Background(), CreateStoryOptions{Name: "Recovery story", EpicRef: "123", DryRun: true})
	if err != nil {
		t.Fatalf("CreateStory returned error: %v", err)
	}
	if result.Story.Created {
		t.Fatal("dry run should not create")
	}
	if result.Story.Epic.GID != "123" {
		t.Fatalf("unexpected epic: %#v", result.Story.Epic)
	}
	if result.Story.TypeMapping != "Story" {
		t.Fatalf("unexpected type mapping: %#v", result.Story)
	}
}

func TestCreateStoryCreatesSubtaskAndAddsToProject(t *testing.T) {
	client := &fakeAsana{
		task:    &asana.Task{GID: "123", Name: "Parent Epic"},
		created: &asana.Task{GID: "story1", Name: "Recovery story", Permalink: "https://example.test/story1"},
	}
	service := newTestService(client)

	result, err := service.CreateStory(context.Background(), CreateStoryOptions{Name: "Recovery story", EpicRef: "123", Notes: "notes"})
	if err != nil {
		t.Fatalf("CreateStory returned error: %v", err)
	}
	if !result.Story.Created || !result.Story.AddedToProject {
		t.Fatalf("expected created and added story, got %#v", result.Story)
	}
	if client.input.ParentGID != "123" {
		t.Fatalf("expected parent epic in create input, got %#v", client.input)
	}
	if client.input.ProjectGID != "" {
		t.Fatalf("story subtask should be added to project by addProject, got create input %#v", client.input)
	}
	if client.addedTaskGID != "story1" || client.addedProjectGID != "p1" {
		t.Fatalf("expected addProject call, got task=%q project=%q", client.addedTaskGID, client.addedProjectGID)
	}
	if client.input.CustomFields["field1"] != "Story" {
		t.Fatalf("unexpected custom fields: %#v", client.input.CustomFields)
	}
}

func TestCreateBugDryRunIncludesPriorityAndEnvironment(t *testing.T) {
	client := &fakeAsana{task: &asana.Task{GID: "123", Name: "Parent Epic"}}
	service := newTestService(client)

	result, err := service.CreateBug(context.Background(), CreateBugOptions{
		Name:        "Provisioning regression",
		EpicRef:     "123",
		Priority:    "P1",
		Environment: "1841",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("CreateBug returned error: %v", err)
	}
	if result.Bug.TypeMapping != "Bug" {
		t.Fatalf("unexpected type mapping: %#v", result.Bug)
	}
	if result.Bug.Priority != "P1" || result.Bug.Environment != "1841" {
		t.Fatalf("expected priority/environment, got %#v", result.Bug)
	}
}

func TestCreateBugCreatesSubtaskAndAddsToProject(t *testing.T) {
	client := &fakeAsana{
		task:    &asana.Task{GID: "123", Name: "Parent Epic"},
		created: &asana.Task{GID: "bug1", Name: "Provisioning regression", Permalink: "https://example.test/bug1"},
	}
	service := newTestService(client)

	result, err := service.CreateBug(context.Background(), CreateBugOptions{
		Name:        "Provisioning regression",
		EpicRef:     "123",
		Priority:    "P1",
		Environment: "1841",
		Notes:       "extra context",
	})
	if err != nil {
		t.Fatalf("CreateBug returned error: %v", err)
	}
	if !result.Bug.Created || !result.Bug.AddedToProject {
		t.Fatalf("expected created and added bug, got %#v", result.Bug)
	}
	if client.input.ParentGID != "123" {
		t.Fatalf("expected parent epic in create input, got %#v", client.input)
	}
	if client.addedTaskGID != "bug1" || client.addedProjectGID != "p1" {
		t.Fatalf("expected addProject call, got task=%q project=%q", client.addedTaskGID, client.addedProjectGID)
	}
	if client.input.CustomFields["field1"] != "Bug" {
		t.Fatalf("unexpected custom fields: %#v", client.input.CustomFields)
	}
	if !strings.Contains(client.input.Notes, "Priority: P1") || !strings.Contains(client.input.Notes, "Environment: 1841") || !strings.Contains(client.input.Notes, "extra context") {
		t.Fatalf("unexpected notes: %q", client.input.Notes)
	}
}

func TestCreateSpikeDryRunIncludesTimeboxAndOutcomes(t *testing.T) {
	client := &fakeAsana{task: &asana.Task{GID: "123", Name: "Parent Epic"}}
	service := newTestService(client)

	result, err := service.CreateSpike(context.Background(), CreateSpikeOptions{
		Name:    "Investigate provisioning",
		EpicRef: "123",
		Timebox: "4h",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("CreateSpike returned error: %v", err)
	}
	if result.Spike.TypeMapping != "Spike" {
		t.Fatalf("unexpected type mapping: %#v", result.Spike)
	}
	if result.Spike.Timebox != "4h" {
		t.Fatalf("unexpected timebox: %#v", result.Spike)
	}
	if len(result.Spike.ExpectedOutcomes) == 0 {
		t.Fatalf("expected default outcomes: %#v", result.Spike)
	}
}

func TestCreateSpikeCreatesSubtaskAndAddsToProject(t *testing.T) {
	client := &fakeAsana{
		task:    &asana.Task{GID: "123", Name: "Parent Epic"},
		created: &asana.Task{GID: "spike1", Name: "Investigate provisioning", Permalink: "https://example.test/spike1"},
	}
	service := newTestService(client)

	result, err := service.CreateSpike(context.Background(), CreateSpikeOptions{
		Name:    "Investigate provisioning",
		EpicRef: "123",
		Timebox: "4h",
		Notes:   "extra context",
	})
	if err != nil {
		t.Fatalf("CreateSpike returned error: %v", err)
	}
	if !result.Spike.Created || !result.Spike.AddedToProject {
		t.Fatalf("expected created and added spike, got %#v", result.Spike)
	}
	if client.input.ParentGID != "123" {
		t.Fatalf("expected parent epic in create input, got %#v", client.input)
	}
	if client.addedTaskGID != "spike1" || client.addedProjectGID != "p1" {
		t.Fatalf("expected addProject call, got task=%q project=%q", client.addedTaskGID, client.addedProjectGID)
	}
	if client.input.CustomFields["field1"] != "Spike" {
		t.Fatalf("unexpected custom fields: %#v", client.input.CustomFields)
	}
	if !strings.Contains(client.input.Notes, "Timebox: 4h") || !strings.Contains(client.input.Notes, "Expected outcomes:") || !strings.Contains(client.input.Notes, "extra context") {
		t.Fatalf("unexpected notes: %q", client.input.Notes)
	}
}

func TestCreateImplementationTaskDryRunIncludesParentRelationship(t *testing.T) {
	client := &fakeAsana{task: &asana.Task{GID: "456", Name: "Parent Bug"}}
	service := newTestService(client)

	result, err := service.CreateImplementationTask(context.Background(), CreateTaskOptions{
		Name:      "Normalize persistence",
		ParentRef: "456",
		Assignee:  "dev@example.com",
		DueOn:     "2026-07-18",
		Estimate:  "2h",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("CreateImplementationTask returned error: %v", err)
	}
	if result.Task.Parent.GID != "456" {
		t.Fatalf("unexpected parent: %#v", result.Task.Parent)
	}
	if result.Task.Assignee != "dev@example.com" || result.Task.DueOn != "2026-07-18" || result.Task.Estimate != "2h" {
		t.Fatalf("expected metadata, got %#v", result.Task)
	}
}

func TestCreateImplementationTaskCreatesSubtask(t *testing.T) {
	client := &fakeAsana{
		task:    &asana.Task{GID: "456", Name: "Parent Bug"},
		created: &asana.Task{GID: "task1", Name: "Normalize persistence", Permalink: "https://example.test/task1"},
	}
	service := newTestService(client)

	result, err := service.CreateImplementationTask(context.Background(), CreateTaskOptions{
		Name:      "Normalize persistence",
		ParentRef: "456",
		Assignee:  "dev@example.com",
		DueOn:     "2026-07-18",
		Estimate:  "2h",
		Notes:     "extra context",
	})
	if err != nil {
		t.Fatalf("CreateImplementationTask returned error: %v", err)
	}
	if !result.Task.Created {
		t.Fatalf("expected created task, got %#v", result.Task)
	}
	if client.input.ParentGID != "456" {
		t.Fatalf("expected parent in create input, got %#v", client.input)
	}
	if !strings.Contains(client.input.Notes, "Assignee: dev@example.com") || !strings.Contains(client.input.Notes, "Due: 2026-07-18") || !strings.Contains(client.input.Notes, "Estimate: 2h") || !strings.Contains(client.input.Notes, "extra context") {
		t.Fatalf("unexpected notes: %q", client.input.Notes)
	}
}
