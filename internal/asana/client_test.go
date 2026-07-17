package asana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
