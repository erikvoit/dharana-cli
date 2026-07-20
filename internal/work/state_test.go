package work

import (
	"context"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
)

func configuredStates() config.StateMappings {
	return config.StateMappings{FieldGID: "state-field", Backlog: "s-backlog", Selected: "s-selected", InProgress: "s-progress", Verification: "s-verification", Done: "s-done", Deferred: "s-deferred", Canceled: "s-canceled"}
}

func taskWithState(gid, name, optionGID string) *asana.Task {
	return &asana.Task{GID: gid, Name: name, CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}, {GID: "state-field", EnumValue: &struct {
		GID  string `json:"gid"`
		Name string `json:"name"`
	}{GID: optionGID}}}}
}

func TestTransitionWorkEnforcesGraphAndUpdatesStateWithCompletion(t *testing.T) {
	task := taskWithState("123", "Story", "s-verification")
	client := &fakeAsana{task: task}
	service := newTestService(client)
	service.Config.(*fakeConfigStore).cfg.States = configuredStates()

	result, err := service.TransitionWork(context.Background(), TransitionWorkOptions{Ref: "123", To: "done", Reason: "Acceptance checks passed"})
	if err != nil {
		t.Fatalf("TransitionWork returned error: %v", err)
	}
	if result.BeforeState != "verification" || result.AfterState != "done" || !result.AfterCompleted || !result.ReasonRecorded {
		t.Fatalf("unexpected transition result: %#v", result)
	}
	if client.updateInput.Completed == nil || !*client.updateInput.Completed || client.updateInput.CustomFields["state-field"] != "s-done" {
		t.Fatalf("state and completion were not sent atomically: %#v", client.updateInput)
	}
	if client.storyText == "" {
		t.Fatal("expected transition reason comment")
	}

	task = taskWithState("456", "Story", "s-backlog")
	client = &fakeAsana{task: task}
	service = newTestService(client)
	service.Config.(*fakeConfigStore).cfg.States = configuredStates()
	if _, err := service.TransitionWork(context.Background(), TransitionWorkOptions{Ref: "456", To: "done"}); err == nil {
		t.Fatal("expected backlog to done to be rejected")
	}
	if client.updateInput.Completed != nil {
		t.Fatal("invalid transition must not mutate Asana")
	}
}

func TestReadyWorkRequiresSelectedWhenStatesConfigured(t *testing.T) {
	selected := taskWithState("selected", "Selected", "s-selected")
	backlog := taskWithState("backlog", "Backlog", "s-backlog")
	service := newTestService(&fakeAsana{page: &asana.TaskPage{Tasks: []asana.Task{*backlog, *selected}}})
	service.Config.(*fakeConfigStore).cfg.States = configuredStates()

	result, err := service.ReadyWork(context.Background(), ReadyWorkOptions{})
	if err != nil {
		t.Fatalf("ReadyWork returned error: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].GID != "selected" {
		t.Fatalf("expected only selected work, got %#v", result.Items)
	}
}

func TestCreateStoryStartsInBacklogWhenStatesConfigured(t *testing.T) {
	client := &fakeAsana{task: &asana.Task{GID: "123", Name: "Epic"}}
	service := newTestService(client)
	service.Config.(*fakeConfigStore).cfg.States = configuredStates()

	if _, err := service.CreateStory(context.Background(), CreateStoryOptions{Name: "New story", EpicRef: "123"}); err != nil {
		t.Fatalf("CreateStory returned error: %v", err)
	}
	if client.input.CustomFields["field1"] != "Story" || client.input.CustomFields["state-field"] != "s-backlog" {
		t.Fatalf("expected type and backlog fields, got %#v", client.input.CustomFields)
	}
}
