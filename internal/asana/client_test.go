package asana

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCurrentUserSendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"gid":"123","name":"Test User","email":"test@example.com"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	user, err := client.CurrentUser(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("CurrentUser returned error: %v", err)
	}
	if user.GID != "123" || user.Name != "Test User" {
		t.Fatalf("unexpected user: %#v", user)
	}
}

func TestCurrentUserReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Not Authorized"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.CurrentUser(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", apiErr.StatusCode)
	}
}

func TestProjectsFollowsPagination(t *testing.T) {
	var projectCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users/me":
			_, _ = w.Write([]byte(`{"data":{"gid":"u1","name":"User","workspaces":[{"gid":"w1","name":"Workspace"}]}}`))
		case r.URL.Path == "/projects" && r.URL.Query().Get("offset") == "":
			projectCalls++
			if r.URL.Query().Get("workspace") != "w1" {
				t.Fatalf("unexpected workspace: %q", r.URL.Query().Get("workspace"))
			}
			_, _ = w.Write([]byte(`{"data":[{"gid":"p1","name":"One","workspace":{"gid":"w1","name":"Workspace"}}],"next_page":{"offset":"next"}}`))
		case r.URL.Path == "/projects" && r.URL.Query().Get("offset") == "next":
			projectCalls++
			_, _ = w.Write([]byte(`{"data":[{"gid":"p2","name":"Two","workspace":{"gid":"w1","name":"Workspace"}}],"next_page":null}`))
		default:
			t.Fatalf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	projects, err := client.Projects(context.Background(), "token", "")
	if err != nil {
		t.Fatalf("Projects returned error: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected two projects, got %#v", projects)
	}
	if projectCalls != 2 {
		t.Fatalf("expected two project page calls, got %d", projectCalls)
	}
	if !strings.Contains(projects[1].Name, "Two") {
		t.Fatalf("unexpected projects: %#v", projects)
	}
}

func TestTasksByNameListsProjectTasksAndFiltersExactMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/p1/tasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"gid":"1","name":"Card provisioning"},{"gid":"2","name":"Card provisioning followup"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	tasks, err := client.TasksByName(context.Background(), "token", "p1", "Card provisioning")
	if err != nil {
		t.Fatalf("TasksByName returned error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].GID != "1" {
		t.Fatalf("unexpected exact matches: %#v", tasks)
	}
}

func TestProjectTasksReturnsPageAndNextOffset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/p1/tasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("offset") != "abc" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":[{"gid":"1","name":"Story","completed":false,"dependencies":[{"gid":"2","name":"Bug"}]}],"next_page":{"offset":"next"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	page, err := client.ProjectTasks(context.Background(), "token", "p1", 25, "abc")
	if err != nil {
		t.Fatalf("ProjectTasks returned error: %v", err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].GID != "1" {
		t.Fatalf("unexpected page: %#v", page)
	}
	if len(page.Tasks[0].Dependencies) != 1 || page.Tasks[0].Dependencies[0].GID != "2" {
		t.Fatalf("unexpected dependencies: %#v", page.Tasks[0].Dependencies)
	}
	if page.NextOffset != "next" {
		t.Fatalf("unexpected next offset: %q", page.NextOffset)
	}
}

func TestSubtasksReturnsPageAndNextOffset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks/parent1/subtasks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("offset") != "abc" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":[{"gid":"1","name":"Task","completed":false,"parent":{"gid":"parent1","name":"Parent"}}],"next_page":{"offset":"next"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	page, err := client.Subtasks(context.Background(), "token", "parent1", 25, "abc")
	if err != nil {
		t.Fatalf("Subtasks returned error: %v", err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].GID != "1" || page.Tasks[0].Parent.GID != "parent1" {
		t.Fatalf("unexpected page: %#v", page)
	}
	if page.NextOffset != "next" {
		t.Fatalf("unexpected next offset: %q", page.NextOffset)
	}
}

func TestCreateTaskPostsProjectTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Data struct {
				Name         string            `json:"name"`
				Projects     []string          `json:"projects"`
				Workspace    string            `json:"workspace"`
				Notes        string            `json:"notes"`
				CustomFields map[string]string `json:"custom_fields"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Data.Name != "Card provisioning" || body.Data.Workspace != "w1" || body.Data.Notes != "notes" {
			t.Fatalf("unexpected body: %#v", body.Data)
		}
		if len(body.Data.Projects) != 1 || body.Data.Projects[0] != "p1" {
			t.Fatalf("unexpected projects: %#v", body.Data.Projects)
		}
		if body.Data.CustomFields["field1"] != "enum1" {
			t.Fatalf("unexpected custom fields: %#v", body.Data.CustomFields)
		}
		_, _ = w.Write([]byte(`{"data":{"gid":"123","name":"Card provisioning","permalink_url":"https://example.test/123"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	task, err := client.CreateTask(context.Background(), "token", CreateTaskInput{
		Name:         "Card provisioning",
		ProjectGID:   "p1",
		WorkspaceGID: "w1",
		Notes:        "notes",
		CustomFields: map[string]string{"field1": "enum1"},
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.GID != "123" || task.Permalink == "" {
		t.Fatalf("unexpected task: %#v", task)
	}
}

func TestCreateAndUpdateTaskUseHTMLNotesForRichDescriptions(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Data["html_notes"] != "<body><strong>Safe</strong></body>" {
			t.Fatalf("expected html_notes transport, got %#v", body.Data)
		}
		if _, exists := body.Data["notes"]; exists {
			t.Fatalf("plain notes must not accompany html_notes: %#v", body.Data)
		}
		_, _ = w.Write([]byte(`{"data":{"gid":"123","name":"Rich task","html_notes":"<body><strong>Safe</strong></body>"}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	if _, err := client.CreateTask(context.Background(), "token", CreateTaskInput{Name: "Rich task", ProjectGID: "p1", HTMLNotes: "<body><strong>Safe</strong></body>"}); err != nil {
		t.Fatal(err)
	}
	htmlNotes := "<body><strong>Safe</strong></body>"
	if _, err := client.UpdateTask(context.Background(), "token", "123", UpdateTaskInput{HTMLNotes: &htmlNotes}); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("expected two requests, got %d", requests)
	}
}

func TestCreateTaskPostsParentSubtask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Data struct {
				Name      string   `json:"name"`
				Projects  []string `json:"projects"`
				Workspace string   `json:"workspace"`
				Parent    string   `json:"parent"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Data.Name != "Recovery story" || body.Data.Workspace != "w1" || body.Data.Parent != "epic1" {
			t.Fatalf("unexpected body: %#v", body.Data)
		}
		if len(body.Data.Projects) != 0 {
			t.Fatalf("subtask creation should not include projects: %#v", body.Data.Projects)
		}
		_, _ = w.Write([]byte(`{"data":{"gid":"story1","name":"Recovery story"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	task, err := client.CreateTask(context.Background(), "token", CreateTaskInput{
		Name:         "Recovery story",
		WorkspaceGID: "w1",
		ParentGID:    "epic1",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.GID != "story1" {
		t.Fatalf("unexpected task: %#v", task)
	}
}

func TestAddTaskToProjectPostsProjectAssociation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks/story1/addProject" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Data struct {
				Project string `json:"project"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Data.Project != "p1" {
			t.Fatalf("unexpected project: %#v", body.Data)
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.AddTaskToProject(context.Background(), "token", "story1", "p1"); err != nil {
		t.Fatalf("AddTaskToProject returned error: %v", err)
	}
}

func TestAddDependenciesPostsDependencyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks/blocked/addDependencies" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Data struct {
				Dependencies []string `json:"dependencies"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Data.Dependencies) != 1 || body.Data.Dependencies[0] != "blocker" {
			t.Fatalf("unexpected dependencies: %#v", body.Data.Dependencies)
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.AddDependencies(context.Background(), "token", "blocked", []string{"blocker"}); err != nil {
		t.Fatalf("AddDependencies returned error: %v", err)
	}
}

func TestRemoveDependenciesPostsDependencyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks/blocked/removeDependencies" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Data struct {
				Dependencies []string `json:"dependencies"`
			} `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Data.Dependencies) != 1 || body.Data.Dependencies[0] != "blocker" {
			t.Fatalf("unexpected dependencies: %#v", body.Data.Dependencies)
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.RemoveDependencies(context.Background(), "token", "blocked", []string{"blocker"}); err != nil {
		t.Fatalf("RemoveDependencies returned error: %v", err)
	}
}

func TestEventsReturnsReplacementCursorOnPreconditionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" || r.URL.Query().Get("resource") != "project-1" {
			t.Fatalf("unexpected event request %s", r.URL.String())
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Sync token invalid or too old. Sync: replacement-token"}]}`))
	}))
	defer server.Close()
	page, err := NewClient(server.URL).Events(context.Background(), "token", "project-1", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 API error, got %v", err)
	}
	if page == nil || page.Sync != "replacement-token" {
		t.Fatalf("expected replacement cursor, got %#v", page)
	}
}

func TestCustomFieldProvisioningRequestsUseAsanaContracts(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		var body struct {
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch r.URL.Path {
		case "/custom_fields":
			if body.Data["workspace"] != "w1" || body.Data["resource_subtype"] != "enum" {
				t.Fatalf("unexpected custom field body: %#v", body.Data)
			}
			_, _ = w.Write([]byte(`{"data":{"gid":"field1","name":"Dharana State","resource_subtype":"enum","enum_options":[{"gid":"backlog","name":"Backlog","enabled":true}]}}`))
		case "/custom_fields/field1/enum_options":
			if body.Data["name"] != "Done" {
				t.Fatalf("unexpected enum option body: %#v", body.Data)
			}
			_, _ = w.Write([]byte(`{"data":{"gid":"done","name":"Done","enabled":true}}`))
		case "/projects/project1/addCustomFieldSetting":
			if body.Data["custom_field"] != "field1" || body.Data["is_important"] != true {
				t.Fatalf("unexpected custom field setting body: %#v", body.Data)
			}
			_, _ = w.Write([]byte(`{"data":{"gid":"setting1"}}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	field, err := client.CreateCustomField(context.Background(), "token", CreateCustomFieldInput{Name: "Dharana State", WorkspaceGID: "w1", EnumOptions: []string{"Backlog"}})
	if err != nil || field.ResourceSubtype != "enum" || len(field.EnumOptions) != 1 {
		t.Fatalf("unexpected field response: %#v err=%v", field, err)
	}
	option, err := client.CreateEnumOption(context.Background(), "token", field.GID, "Done")
	if err != nil || option.GID != "done" {
		t.Fatalf("unexpected enum option response: %#v err=%v", option, err)
	}
	if err := client.AddCustomFieldToProject(context.Background(), "token", "project1", field.GID); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /custom_fields", "POST /custom_fields/field1/enum_options", "POST /projects/project1/addCustomFieldSetting"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected requests: %v", calls)
	}
}

func TestReadRequestsRetryRateLimitsWithBoundedDelay(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"gid":"p1","name":"Project","workspace":{"gid":"w1"}}}`))
	}))
	defer server.Close()
	var delays []time.Duration
	client := NewClient(server.URL)
	client.Sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	project, err := client.Project(context.Background(), "token", "p1")
	if err != nil || project.GID != "p1" {
		t.Fatalf("unexpected project %#v err=%v", project, err)
	}
	if calls != 3 || len(delays) != 2 || delays[0] != time.Second {
		t.Fatalf("expected bounded retries, calls=%d delays=%v", calls, delays)
	}
}
