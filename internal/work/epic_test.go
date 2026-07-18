package work

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
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
	matches           []asana.Task
	page              *asana.TaskPage
	subtasks          map[string]*asana.TaskPage
	task              *asana.Task
	tasks             map[string]*asana.Task
	taskErr           error
	created           *asana.Task
	input             asana.CreateTaskInput
	addedTaskGID      string
	addedProjectGID   string
	dependencyTaskGID string
	dependencyGIDs    []string
	removedTaskGID    string
	removedGIDs       []string
}

func (f *fakeAsana) TasksByName(_ context.Context, _ string, _ string, _ string) ([]asana.Task, error) {
	return f.matches, nil
}

func (f *fakeAsana) ProjectTasks(_ context.Context, _ string, _ string, _ int, _ string) (*asana.TaskPage, error) {
	if f.page == nil {
		return &asana.TaskPage{}, nil
	}
	return f.page, nil
}

func (f *fakeAsana) Subtasks(_ context.Context, _ string, taskGID string, _ int, _ string) (*asana.TaskPage, error) {
	if f.subtasks == nil || f.subtasks[taskGID] == nil {
		return &asana.TaskPage{}, nil
	}
	return f.subtasks[taskGID], nil
}

func (f *fakeAsana) Task(_ context.Context, _ string, gid string) (*asana.Task, error) {
	if f.taskErr != nil {
		return nil, f.taskErr
	}
	if f.tasks != nil && f.tasks[gid] != nil {
		return f.tasks[gid], nil
	}
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

func (f *fakeAsana) AddDependencies(_ context.Context, _ string, taskGID string, dependencyGIDs []string) error {
	f.dependencyTaskGID = taskGID
	f.dependencyGIDs = dependencyGIDs
	return nil
}

func (f *fakeAsana) RemoveDependencies(_ context.Context, _ string, taskGID string, dependencyGIDs []string) error {
	f.removedTaskGID = taskGID
	f.removedGIDs = dependencyGIDs
	return nil
}

type fakeRefStore struct {
	cache *refcache.Cache
	err   error
}

func (s *fakeRefStore) Load() (*refcache.Cache, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.cache == nil {
		return &refcache.Cache{}, nil
	}
	return s.cache, nil
}

func (s *fakeRefStore) Replace(entries []refcache.Entry) (*refcache.Cache, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.cache = &refcache.Cache{UpdatedAt: "now", Items: entries}
	return s.cache, nil
}

func (s *fakeRefStore) Resolve(ref string) (*refcache.Entry, error) {
	cache, err := s.Load()
	if err != nil {
		return nil, err
	}
	for _, entry := range cache.Items {
		if entry.Ref == ref || entry.GID == ref {
			copy := entry
			return &copy, nil
		}
	}
	return nil, refcache.ErrReferenceNotFound
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

func TestCreateStoryFallsBackToNameForNumericEpicWhenGIDNotFound(t *testing.T) {
	client := &fakeAsana{
		taskErr: &asana.APIError{StatusCode: http.StatusNotFound},
		matches: []asana.Task{{
			GID:  "epic-numeric",
			Name: "123",
		}},
	}
	service := newTestService(client)

	result, err := service.CreateStory(context.Background(), CreateStoryOptions{Name: "Recovery story", EpicRef: "123", DryRun: true})
	if err != nil {
		t.Fatalf("CreateStory returned error: %v", err)
	}
	if result.Story.Epic.GID != "epic-numeric" {
		t.Fatalf("expected numeric epic name to resolve by search, got %#v", result.Story.Epic)
	}
}

func TestCreateStoryIgnoresFuzzyAndWrongParentDuplicateMatches(t *testing.T) {
	client := &fakeAsana{
		task: &asana.Task{GID: "123", Name: "Parent Epic"},
		matches: []asana.Task{
			{GID: "fuzzy", Name: "Recovery story part 2", Parent: &asana.TaskParent{GID: "123", Name: "Parent Epic"}},
			{GID: "other-parent", Name: "Recovery story", Parent: &asana.TaskParent{GID: "456", Name: "Other Epic"}},
		},
	}
	service := newTestService(client)

	result, err := service.CreateStory(context.Background(), CreateStoryOptions{Name: "Recovery story", EpicRef: "123", DryRun: true})
	if err != nil {
		t.Fatalf("CreateStory returned error: %v", err)
	}
	if result.Story.Created {
		t.Fatal("dry run should not create")
	}
}

func TestCreateStoryRejectsExactDuplicateUnderSameEpic(t *testing.T) {
	client := &fakeAsana{
		task: &asana.Task{GID: "123", Name: "Parent Epic"},
		matches: []asana.Task{{
			GID:    "story1",
			Name:   "Recovery story",
			Parent: &asana.TaskParent{GID: "123", Name: "Parent Epic"},
		}},
	}
	service := newTestService(client)

	_, err := service.CreateStory(context.Background(), CreateStoryOptions{Name: "Recovery story", EpicRef: "123"})
	if err == nil {
		t.Fatal("expected duplicate story error")
	}
	appErr, ok := err.(*output.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "DUPLICATE_STORY" {
		t.Fatalf("expected DUPLICATE_STORY, got %q", appErr.Code)
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
		matches: []asana.Task{{GID: "existing", Name: "Normalize persistence", Parent: &asana.TaskParent{GID: "other", Name: "Other Parent"}}},
		created: &asana.Task{GID: "task1", Name: "Normalize persistence", Permalink: "https://example.test/task1"},
	}
	service := newTestService(client)

	result, err := service.CreateImplementationTask(context.Background(), CreateTaskOptions{
		Name:      "Normalize persistence",
		ParentRef: "story:456",
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

func TestCreateImplementationTaskIdempotencyKeyReturnsExistingSubtask(t *testing.T) {
	client := &fakeAsana{
		task: &asana.Task{GID: "456", Name: "Parent Bug"},
		subtasks: map[string]*asana.TaskPage{
			"456": &asana.TaskPage{Tasks: []asana.Task{{
				GID:       "existing-task",
				Name:      "Normalize persistence",
				Permalink: "https://example.test/existing-task",
			}}},
		},
	}
	service := newTestService(client)

	result, err := service.CreateImplementationTask(context.Background(), CreateTaskOptions{
		Name:           "Normalize persistence",
		ParentRef:      "456",
		IdempotencyKey: "retry-1",
	})
	if err != nil {
		t.Fatalf("CreateImplementationTask returned error: %v", err)
	}
	if !result.Task.IdempotentExisting || result.Task.GID != "existing-task" {
		t.Fatalf("expected existing task result, got %#v", result.Task)
	}
	if client.input.Name != "" {
		t.Fatalf("idempotency should not create, got input %#v", client.input)
	}
	if result.Task.IdempotencyKey != "retry-1" {
		t.Fatalf("expected idempotency key in result, got %#v", result.Task)
	}
}

func TestListWorkFiltersByTypeStatusAndEpic(t *testing.T) {
	service := newTestService(&fakeAsana{
		task: &asana.Task{GID: "123", Name: "Epic One"},
		page: &asana.TaskPage{
			NextOffset: "next",
			Tasks: []asana.Task{
				{
					GID:       "story1",
					Name:      "Story",
					Completed: false,
					Parent:    &asana.TaskParent{GID: "123", Name: "Epic One"},
					CustomFields: []asana.CustomField{{
						GID:          "field1",
						DisplayValue: "Story",
					}},
				},
				{
					GID:       "bug1",
					Name:      "Bug",
					Completed: true,
					Parent:    &asana.TaskParent{GID: "123", Name: "Epic One"},
					CustomFields: []asana.CustomField{{
						GID:          "field1",
						DisplayValue: "Bug",
					}},
				},
			},
		},
	})

	result, err := service.ListWork(context.Background(), ListWorkOptions{
		Types:   []string{"story"},
		Status:  "incomplete",
		EpicRef: "123",
		Limit:   25,
	})
	if err != nil {
		t.Fatalf("ListWork returned error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected one item, got %#v", result.Items)
	}
	if result.Items[0].Type != "story" || result.Items[0].Status != "incomplete" {
		t.Fatalf("unexpected item: %#v", result.Items[0])
	}
	if result.NextOffset != "next" {
		t.Fatalf("expected next offset, got %q", result.NextOffset)
	}
}

func TestListWorkMatchesEnumCustomFieldByNameWhenGIDIsPresent(t *testing.T) {
	enumValue := &struct {
		GID  string `json:"gid"`
		Name string `json:"name"`
	}{GID: "enum-story-gid", Name: "Story"}
	service := newTestService(&fakeAsana{
		page: &asana.TaskPage{Tasks: []asana.Task{{
			GID:  "story1",
			Name: "Story",
			CustomFields: []asana.CustomField{{
				GID:          "field1",
				DisplayValue: "Story",
				EnumValue:    enumValue,
			}},
		}}},
	})

	result, err := service.ListWork(context.Background(), ListWorkOptions{Types: []string{"story"}})
	if err != nil {
		t.Fatalf("ListWork returned error: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Type != "story" {
		t.Fatalf("expected story item, got %#v", result.Items)
	}
}

func TestWorkTreeBuildsHierarchyAndReportsIssues(t *testing.T) {
	service := newTestService(&fakeAsana{
		page: &asana.TaskPage{Tasks: []asana.Task{
			{
				GID:  "epic1",
				Name: "Epic One",
				CustomFields: []asana.CustomField{{
					GID:          "field1",
					DisplayValue: "Epic",
				}},
			},
			{
				GID:    "story1",
				Name:   "Story One",
				Parent: &asana.TaskParent{GID: "epic1", Name: "Epic One"},
				CustomFields: []asana.CustomField{{
					GID:          "field1",
					DisplayValue: "Story",
				}},
			},
			{
				GID:  "orphan-bug",
				Name: "Orphan Bug",
				CustomFields: []asana.CustomField{{
					GID:          "field1",
					DisplayValue: "Bug",
				}},
			},
		}},
		subtasks: map[string]*asana.TaskPage{
			"epic1": &asana.TaskPage{Tasks: []asana.Task{{
				GID:    "bug1",
				Name:   "Bug One",
				Parent: &asana.TaskParent{GID: "epic1", Name: "Epic One"},
				CustomFields: []asana.CustomField{{
					GID:          "field1",
					DisplayValue: "Bug",
				}},
			}}},
			"story1": &asana.TaskPage{Tasks: []asana.Task{{
				GID:    "task1",
				Name:   "Implementation Task",
				Parent: &asana.TaskParent{GID: "story1", Name: "Story One"},
			}}},
		},
	})

	result, err := service.WorkTree(context.Background(), WorkTreeOptions{})
	if err != nil {
		t.Fatalf("WorkTree returned error: %v", err)
	}
	if len(result.Epics) != 1 {
		t.Fatalf("expected one epic, got %#v", result.Epics)
	}
	if len(result.Epics[0].Children) != 2 {
		t.Fatalf("expected story and bug children, got %#v", result.Epics[0].Children)
	}
	var story *TreeNode
	for i := range result.Epics[0].Children {
		if result.Epics[0].Children[i].Item.GID == "story1" {
			story = &result.Epics[0].Children[i]
		}
	}
	if story == nil || len(story.Children) != 1 || story.Children[0].Item.GID != "task1" {
		t.Fatalf("expected implementation task under story, got %#v", result.Epics[0].Children)
	}
	if len(result.Issues) != 1 || result.Issues[0].Code != "MALFORMED_PARENT" {
		t.Fatalf("expected malformed parent issue, got %#v", result.Issues)
	}
}

func TestRefreshRefsStoresDiscoveredWork(t *testing.T) {
	refs := &fakeRefStore{}
	service := newTestService(&fakeAsana{page: &asana.TaskPage{Tasks: []asana.Task{{
		GID:       "story1",
		Name:      "Story",
		Completed: false,
		CustomFields: []asana.CustomField{{
			GID:          "field1",
			DisplayValue: "Story",
		}},
	}}}})
	service.Refs = refs

	result, err := service.RefreshRefs(context.Background(), RefreshRefsOptions{Limit: 25})
	if err != nil {
		t.Fatalf("RefreshRefs returned error: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("expected one ref, got %#v", result)
	}
	if refs.cache.Items[0].Ref != "STORY:Story" || refs.cache.Items[0].GID != "story1" {
		t.Fatalf("unexpected cache: %#v", refs.cache)
	}
}

func TestResolveRefValidatesCachedGID(t *testing.T) {
	refs := &fakeRefStore{cache: &refcache.Cache{Items: []refcache.Entry{{
		Ref:  "STORY:Story",
		GID:  "story1",
		Name: "Old name",
		Type: "story",
	}}}}
	service := newTestService(&fakeAsana{task: &asana.Task{GID: "story1", Name: "New name", Permalink: "https://example.test/story1"}})
	service.Refs = refs

	result, err := service.ResolveRef(context.Background(), "STORY:Story")
	if err != nil {
		t.Fatalf("ResolveRef returned error: %v", err)
	}
	if result.Entry.GID != "story1" || result.Entry.Name != "New name" {
		t.Fatalf("unexpected resolve result: %#v", result.Entry)
	}
}

func TestAddDependencyResolvesFriendlyRefsAndAddsDependency(t *testing.T) {
	refs := &fakeRefStore{cache: &refcache.Cache{Items: []refcache.Entry{
		{Ref: "STORY:Blocked", GID: "story1", Name: "Blocked", Type: "story"},
		{Ref: "BUG:Blocker", GID: "bug1", Name: "Blocker", Type: "bug"},
	}}}
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"story1": {GID: "story1", Name: "Blocked"},
		"bug1":   {GID: "bug1", Name: "Blocker"},
	}}
	service := newTestService(client)
	service.Refs = refs

	result, err := service.AddDependency(context.Background(), AddDependencyOptions{
		BlockedRef:   "STORY:Blocked",
		BlockedByRef: "BUG:Blocker",
	})
	if err != nil {
		t.Fatalf("AddDependency returned error: %v", err)
	}
	if !result.Added || client.dependencyTaskGID != "story1" || len(client.dependencyGIDs) != 1 || client.dependencyGIDs[0] != "bug1" {
		t.Fatalf("unexpected dependency write: result=%#v task=%q deps=%#v", result, client.dependencyTaskGID, client.dependencyGIDs)
	}
}

func TestAddDependencyRejectsSelfDependency(t *testing.T) {
	service := newTestService(&fakeAsana{task: &asana.Task{GID: "123", Name: "Same"}})

	_, err := service.AddDependency(context.Background(), AddDependencyOptions{BlockedRef: "123", BlockedByRef: "123"})
	if err == nil {
		t.Fatal("expected self dependency error")
	}
	appErr, ok := err.(*output.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "SELF_DEPENDENCY" {
		t.Fatalf("expected SELF_DEPENDENCY, got %q", appErr.Code)
	}
}

func TestAddDependencyReturnsExistingWhenAlreadyBlocked(t *testing.T) {
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"111": {
			GID:          "111",
			Name:         "Blocked",
			Dependencies: []asana.TaskSummary{{GID: "222", Name: "Blocker"}},
		},
		"222": {GID: "222", Name: "Blocker"},
	}}
	service := newTestService(client)

	result, err := service.AddDependency(context.Background(), AddDependencyOptions{BlockedRef: "111", BlockedByRef: "222"})
	if err != nil {
		t.Fatalf("AddDependency returned error: %v", err)
	}
	if !result.IdempotentExisting || result.Added || client.dependencyTaskGID != "" {
		t.Fatalf("expected existing dependency without mutation, got result=%#v task=%q", result, client.dependencyTaskGID)
	}
}

func TestAddDependencyDryRunDoesNotMutate(t *testing.T) {
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"111": {GID: "111", Name: "Blocked"},
		"222": {GID: "222", Name: "Blocker"},
	}}
	service := newTestService(client)

	result, err := service.AddDependency(context.Background(), AddDependencyOptions{BlockedRef: "111", BlockedByRef: "222", DryRun: true})
	if err != nil {
		t.Fatalf("AddDependency returned error: %v", err)
	}
	if !result.DryRun || result.Added || client.dependencyTaskGID != "" {
		t.Fatalf("expected dry-run result without mutation, got result=%#v task=%q", result, client.dependencyTaskGID)
	}
}

func TestAddDependencyGIDResolutionUsesConfiguredTaskTypes(t *testing.T) {
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"111": {
			GID:    "111",
			Name:   "Story",
			Parent: &asana.TaskParent{GID: "epic1", Name: "Epic"},
			CustomFields: []asana.CustomField{{
				GID:          "field1",
				DisplayValue: "Story",
			}},
		},
		"222": {
			GID:  "222",
			Name: "Bug",
			CustomFields: []asana.CustomField{{
				GID:          "field1",
				DisplayValue: "Bug",
			}},
		},
	}}
	service := newTestService(client)

	result, err := service.AddDependency(context.Background(), AddDependencyOptions{BlockedRef: "111", BlockedByRef: "222", DryRun: true})
	if err != nil {
		t.Fatalf("AddDependency returned error: %v", err)
	}
	if result.Blocked.Type != "story" || result.BlockedBy.Type != "bug" {
		t.Fatalf("expected configured types, got blocked=%#v blocker=%#v", result.Blocked, result.BlockedBy)
	}
}

func TestRemoveDependencyRemovesExistingBlocker(t *testing.T) {
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"111": {
			GID:          "111",
			Name:         "Blocked",
			Dependencies: []asana.TaskSummary{{GID: "222", Name: "Blocker"}},
		},
		"222": {GID: "222", Name: "Blocker"},
	}}
	service := newTestService(client)

	result, err := service.RemoveDependency(context.Background(), RemoveDependencyOptions{BlockedRef: "111", BlockedByRef: "222"})
	if err != nil {
		t.Fatalf("RemoveDependency returned error: %v", err)
	}
	if !result.Found || !result.Removed || client.removedTaskGID != "111" || len(client.removedGIDs) != 1 || client.removedGIDs[0] != "222" {
		t.Fatalf("unexpected removal: result=%#v task=%q deps=%#v", result, client.removedTaskGID, client.removedGIDs)
	}
}

func TestRemoveDependencyReturnsNotFoundResultWithoutMutation(t *testing.T) {
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"111": {GID: "111", Name: "Blocked"},
		"222": {GID: "222", Name: "Blocker"},
	}}
	service := newTestService(client)

	result, err := service.RemoveDependency(context.Background(), RemoveDependencyOptions{BlockedRef: "111", BlockedByRef: "222"})
	if err != nil {
		t.Fatalf("RemoveDependency returned error: %v", err)
	}
	if result.Found || result.Removed || client.removedTaskGID != "" {
		t.Fatalf("expected not-found result without mutation, got result=%#v task=%q", result, client.removedTaskGID)
	}
}

func TestRemoveDependencyDryRunDoesNotMutate(t *testing.T) {
	client := &fakeAsana{tasks: map[string]*asana.Task{
		"111": {
			GID:          "111",
			Name:         "Blocked",
			Dependencies: []asana.TaskSummary{{GID: "222", Name: "Blocker"}},
		},
		"222": {GID: "222", Name: "Blocker"},
	}}
	service := newTestService(client)

	result, err := service.RemoveDependency(context.Background(), RemoveDependencyOptions{BlockedRef: "111", BlockedByRef: "222", DryRun: true})
	if err != nil {
		t.Fatalf("RemoveDependency returned error: %v", err)
	}
	if !result.Found || result.Removed || client.removedTaskGID != "" {
		t.Fatalf("expected dry-run result without mutation, got result=%#v task=%q", result, client.removedTaskGID)
	}
}

func TestBlockedWorkListsBlockedItemsWithBlockers(t *testing.T) {
	refs := &fakeRefStore{cache: &refcache.Cache{Items: []refcache.Entry{{
		Ref:    "BUG:Blocker",
		GID:    "bug1",
		Name:   "Blocker",
		Type:   "bug",
		Status: "incomplete",
	}}}}
	service := newTestService(&fakeAsana{
		task: &asana.Task{GID: "123", Name: "Epic One"},
		page: &asana.TaskPage{Tasks: []asana.Task{
			{
				GID:          "story1",
				Name:         "Blocked Story",
				Parent:       &asana.TaskParent{GID: "123", Name: "Epic One"},
				Dependencies: []asana.TaskSummary{{GID: "bug1", Name: "Blocker"}},
				CustomFields: []asana.CustomField{{
					GID:          "field1",
					DisplayValue: "Story",
				}},
			},
			{
				GID:  "bug1",
				Name: "Blocker",
				CustomFields: []asana.CustomField{{
					GID:          "field1",
					DisplayValue: "Bug",
				}},
			},
		}},
	})
	service.Refs = refs

	result, err := service.BlockedWork(context.Background(), BlockedWorkOptions{Types: []string{"story"}, EpicRef: "123"})
	if err != nil {
		t.Fatalf("BlockedWork returned error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected one blocked item, got %#v", result.Items)
	}
	if result.Items[0].Item.Ref != "STORY:Blocked Story" {
		t.Fatalf("unexpected blocked item: %#v", result.Items[0].Item)
	}
	if len(result.Items[0].Blockers) != 1 || result.Items[0].Blockers[0].Ref != "BUG:Blocker" {
		t.Fatalf("unexpected blockers: %#v", result.Items[0].Blockers)
	}
}

func TestBlockedWorkSkipsCompletedItemsAndResolvedBlockers(t *testing.T) {
	refs := &fakeRefStore{cache: &refcache.Cache{Items: []refcache.Entry{
		{Ref: "BUG:Resolved", GID: "resolved", Name: "Resolved", Type: "bug", Status: "completed"},
		{Ref: "BUG:Open", GID: "open", Name: "Open", Type: "bug", Status: "incomplete"},
	}}}
	service := newTestService(&fakeAsana{
		page: &asana.TaskPage{Tasks: []asana.Task{
			{
				GID:          "only-resolved",
				Name:         "Only Resolved",
				Dependencies: []asana.TaskSummary{{GID: "resolved", Name: "Resolved"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
			{
				GID:          "completed-blocked",
				Name:         "Completed Blocked",
				Completed:    true,
				Dependencies: []asana.TaskSummary{{GID: "open", Name: "Open"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
			{
				GID:          "active-blocked",
				Name:         "Active Blocked",
				Dependencies: []asana.TaskSummary{{GID: "resolved", Name: "Resolved"}, {GID: "open", Name: "Open"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
		}},
	})
	service.Refs = refs

	result, err := service.BlockedWork(context.Background(), BlockedWorkOptions{Types: []string{"story"}})
	if err != nil {
		t.Fatalf("BlockedWork returned error: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Item.GID != "active-blocked" {
		t.Fatalf("expected only active blocked work, got %#v", result.Items)
	}
	if len(result.Items[0].Blockers) != 1 || result.Items[0].Blockers[0].GID != "open" {
		t.Fatalf("expected only unresolved blocker, got %#v", result.Items[0].Blockers)
	}
}

func TestReadyWorkFiltersActionableItems(t *testing.T) {
	service := newTestService(&fakeAsana{
		task: &asana.Task{GID: "123", Name: "Epic One"},
		page: &asana.TaskPage{Tasks: []asana.Task{
			{
				GID:    "ready-story",
				Name:   "Ready Story",
				Parent: &asana.TaskParent{GID: "123", Name: "Epic One"},
				CustomFields: []asana.CustomField{
					{GID: "field1", DisplayValue: "Story"},
					{GID: "priority-field", DisplayValue: "P1"},
					{GID: "component-field", DisplayValue: "Cards"},
				},
			},
			{
				GID:          "blocked-story",
				Name:         "Blocked Story",
				Parent:       &asana.TaskParent{GID: "123", Name: "Epic One"},
				Dependencies: []asana.TaskSummary{{GID: "bug1", Name: "Blocker"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
			{
				GID:       "completed-story",
				Name:      "Completed Story",
				Completed: true,
				Parent:    &asana.TaskParent{GID: "123", Name: "Epic One"},
				CustomFields: []asana.CustomField{
					{GID: "field1", DisplayValue: "Story"},
					{GID: "priority-field", DisplayValue: "P1"},
					{GID: "component-field", DisplayValue: "Cards"},
				},
			},
		}},
	})
	service.Config = &fakeConfigStore{cfg: &config.File{
		ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"},
		TaskTypes:     config.TaskTypes{FieldGID: "field1", Epic: "Epic", Story: "Story", Bug: "Bug", Spike: "Spike"},
		Fields:        config.FieldMappings{PriorityGID: "priority-field", ComponentGID: "component-field"},
	}}

	result, err := service.ReadyWork(context.Background(), ReadyWorkOptions{
		Types:      []string{"story"},
		EpicRef:    "123",
		Priorities: []string{"P1"},
		Components: []string{"cards"},
	})
	if err != nil {
		t.Fatalf("ReadyWork returned error: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].GID != "ready-story" {
		t.Fatalf("expected only ready story, got %#v", result.Items)
	}
}

func TestReadyWorkIncludesItemsWithCompletedDependencies(t *testing.T) {
	service := newTestService(&fakeAsana{
		page: &asana.TaskPage{Tasks: []asana.Task{
			{
				GID:          "ready-after-blocker",
				Name:         "Ready After Blocker",
				Dependencies: []asana.TaskSummary{{GID: "done", Name: "Done"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
			{
				GID:       "done",
				Name:      "Done",
				Completed: true,
				CustomFields: []asana.CustomField{
					{GID: "field1", DisplayValue: "Bug"},
				},
			},
			{
				GID:          "still-blocked",
				Name:         "Still Blocked",
				Dependencies: []asana.TaskSummary{{GID: "missing", Name: "Missing"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
		}},
	})

	result, err := service.ReadyWork(context.Background(), ReadyWorkOptions{Types: []string{"story"}})
	if err != nil {
		t.Fatalf("ReadyWork returned error: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].GID != "ready-after-blocker" {
		t.Fatalf("expected dependency-resolved story, got %#v", result.Items)
	}
}

func TestReadyWorkRequiresConfiguredPriorityFieldWhenFilteringPriority(t *testing.T) {
	service := newTestService(&fakeAsana{})

	_, err := service.ReadyWork(context.Background(), ReadyWorkOptions{Priorities: []string{"P1"}})
	if err == nil {
		t.Fatal("expected priority field configuration error")
	}
	appErr, ok := err.(*output.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "PRIORITY_FIELD_NOT_CONFIGURED" {
		t.Fatalf("expected PRIORITY_FIELD_NOT_CONFIGURED, got %q", appErr.Code)
	}
}

func TestWorkGraphBuildsEdgesAndReportsCycles(t *testing.T) {
	service := newTestService(&fakeAsana{
		page: &asana.TaskPage{Tasks: []asana.Task{
			{
				GID:          "story1",
				Name:         "Story",
				Dependencies: []asana.TaskSummary{{GID: "bug1", Name: "Bug"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Story"}},
			},
			{
				GID:          "bug1",
				Name:         "Bug",
				Dependencies: []asana.TaskSummary{{GID: "story1", Name: "Story"}},
				CustomFields: []asana.CustomField{{GID: "field1", DisplayValue: "Bug"}},
			},
		}},
	})

	result, err := service.WorkGraph(context.Background(), WorkGraphOptions{})
	if err != nil {
		t.Fatalf("WorkGraph returned error: %v", err)
	}
	if len(result.Nodes) != 2 || len(result.Edges) != 2 {
		t.Fatalf("expected two nodes and edges, got nodes=%#v edges=%#v", result.Nodes, result.Edges)
	}
	if len(result.Cycles) != 1 {
		t.Fatalf("expected one cycle, got %#v", result.Cycles)
	}
	if !strings.Contains(result.Mermaid, "flowchart LR") || !strings.Contains(result.Mermaid, "Cycle detected") {
		t.Fatalf("expected Mermaid graph with cycle comment, got %s", result.Mermaid)
	}
	if !strings.Contains(result.Mermaid, `BUG: Bug\nBUG:Bug`) {
		t.Fatalf("expected Mermaid label to contain escaped newline, got %s", result.Mermaid)
	}
}

func TestCycleSignaturePreservesDirection(t *testing.T) {
	first := cycleSignature([]string{"b", "a", "c", "b"})
	second := cycleSignature([]string{"b", "c", "a", "b"})

	if first == second {
		t.Fatalf("expected directionally different cycles to have different signatures: %q", first)
	}
	if first != "a->c->b->a" {
		t.Fatalf("unexpected rotated signature: %q", first)
	}
}
