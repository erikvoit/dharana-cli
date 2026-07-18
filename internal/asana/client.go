package asana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://app.asana.com/api/1.0"

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type Workspace struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

type User struct {
	GID        string      `json:"gid"`
	Name       string      `json:"name"`
	Email      string      `json:"email,omitempty"`
	Workspaces []Workspace `json:"workspaces,omitempty"`
}

type Project struct {
	GID       string    `json:"gid"`
	Name      string    `json:"name"`
	Workspace Workspace `json:"workspace"`
}

type Task struct {
	GID          string        `json:"gid"`
	Name         string        `json:"name"`
	Completed    bool          `json:"completed,omitempty"`
	Permalink    string        `json:"permalink_url,omitempty"`
	Parent       *TaskParent   `json:"parent,omitempty"`
	Dependencies []TaskSummary `json:"dependencies,omitempty"`
	CustomFields []CustomField `json:"custom_fields,omitempty"`
}

type TaskSummary struct {
	GID  string `json:"gid"`
	Name string `json:"name,omitempty"`
}

type TaskParent struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

type CustomField struct {
	GID          string `json:"gid"`
	DisplayValue string `json:"display_value,omitempty"`
	EnumValue    *struct {
		GID  string `json:"gid"`
		Name string `json:"name"`
	} `json:"enum_value,omitempty"`
}

type TaskPage struct {
	Tasks      []Task
	NextOffset string
}

type CreateTaskInput struct {
	Name         string
	ProjectGID   string
	WorkspaceGID string
	ParentGID    string
	Notes        string
	CustomFields map[string]string
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("asana api returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("asana api returned status %d: %s", e.StatusCode, e.Message)
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) CurrentUser(ctx context.Context, token string) (*User, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("asana token is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/users/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dharana-cli")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &APIError{StatusCode: res.StatusCode, Message: extractErrorMessage(body)}
	}

	var payload struct {
		Data User `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) Projects(ctx context.Context, token string, workspaceGID string) ([]Project, error) {
	if strings.TrimSpace(workspaceGID) == "" {
		user, err := c.CurrentUser(ctx, token)
		if err != nil {
			return nil, err
		}
		var all []Project
		for _, workspace := range user.Workspaces {
			projects, err := c.projectsForWorkspace(ctx, token, workspace.GID)
			if err != nil {
				return nil, err
			}
			all = append(all, projects...)
		}
		return all, nil
	}
	return c.projectsForWorkspace(ctx, token, workspaceGID)
}

func (c *Client) Project(ctx context.Context, token string, gid string) (*Project, error) {
	if strings.TrimSpace(gid) == "" {
		return nil, errors.New("project gid is empty")
	}
	var payload struct {
		Data Project `json:"data"`
	}
	if err := c.get(ctx, token, "/projects/"+gid+"?opt_fields=gid,name,workspace.gid,workspace.name", &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) TasksByName(ctx context.Context, token string, projectGID string, name string) ([]Task, error) {
	if strings.TrimSpace(projectGID) == "" {
		return nil, errors.New("project gid is empty")
	}
	tasks, err := c.tasksForProject(ctx, token, projectGID)
	if err != nil {
		return nil, err
	}
	var exact []Task
	for _, task := range tasks {
		if task.Name == name {
			exact = append(exact, task)
		}
	}
	return exact, nil
}

func (c *Client) ProjectTasks(ctx context.Context, token string, projectGID string, limit int, offset string) (*TaskPage, error) {
	if strings.TrimSpace(projectGID) == "" {
		return nil, errors.New("project gid is empty")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	query.Set("opt_fields", "gid,name,completed,permalink_url,parent.gid,parent.name,custom_fields.gid,custom_fields.display_value,custom_fields.enum_value.gid,custom_fields.enum_value.name")
	if offset != "" {
		query.Set("offset", offset)
	}
	var payload struct {
		Data     []Task `json:"data"`
		NextPage *struct {
			Offset string `json:"offset"`
		} `json:"next_page"`
	}
	if err := c.get(ctx, token, "/projects/"+projectGID+"/tasks?"+query.Encode(), &payload); err != nil {
		return nil, err
	}
	page := &TaskPage{Tasks: payload.Data}
	if payload.NextPage != nil {
		page.NextOffset = payload.NextPage.Offset
	}
	return page, nil
}

func (c *Client) Subtasks(ctx context.Context, token string, taskGID string, limit int, offset string) (*TaskPage, error) {
	if strings.TrimSpace(taskGID) == "" {
		return nil, errors.New("task gid is empty")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", limit))
	query.Set("opt_fields", "gid,name,completed,permalink_url,parent.gid,parent.name,custom_fields.gid,custom_fields.display_value,custom_fields.enum_value.gid,custom_fields.enum_value.name")
	if offset != "" {
		query.Set("offset", offset)
	}
	var payload struct {
		Data     []Task `json:"data"`
		NextPage *struct {
			Offset string `json:"offset"`
		} `json:"next_page"`
	}
	if err := c.get(ctx, token, "/tasks/"+taskGID+"/subtasks?"+query.Encode(), &payload); err != nil {
		return nil, err
	}
	page := &TaskPage{Tasks: payload.Data}
	if payload.NextPage != nil {
		page.NextOffset = payload.NextPage.Offset
	}
	return page, nil
}

func (c *Client) Task(ctx context.Context, token string, gid string) (*Task, error) {
	if strings.TrimSpace(gid) == "" {
		return nil, errors.New("task gid is empty")
	}
	var payload struct {
		Data Task `json:"data"`
	}
	if err := c.get(ctx, token, "/tasks/"+gid+"?opt_fields=gid,name,completed,permalink_url,parent.gid,parent.name,dependencies.gid,dependencies.name,custom_fields.gid,custom_fields.display_value,custom_fields.enum_value.gid,custom_fields.enum_value.name", &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) CreateTask(ctx context.Context, token string, input CreateTaskInput) (*Task, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("task name is empty")
	}
	if strings.TrimSpace(input.ProjectGID) == "" && strings.TrimSpace(input.ParentGID) == "" {
		return nil, errors.New("project gid or parent gid is required")
	}

	body := map[string]any{
		"data": map[string]any{
			"name": input.Name,
		},
	}
	data := body["data"].(map[string]any)
	if input.ProjectGID != "" {
		data["projects"] = []string{input.ProjectGID}
	}
	if input.WorkspaceGID != "" {
		data["workspace"] = input.WorkspaceGID
	}
	if input.ParentGID != "" {
		data["parent"] = input.ParentGID
	}
	if input.Notes != "" {
		data["notes"] = input.Notes
	}
	if len(input.CustomFields) > 0 {
		data["custom_fields"] = input.CustomFields
	}

	var payload struct {
		Data Task `json:"data"`
	}
	if err := c.post(ctx, token, "/tasks?opt_fields=gid,name,permalink_url", body, &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) AddTaskToProject(ctx context.Context, token string, taskGID string, projectGID string) error {
	if strings.TrimSpace(taskGID) == "" {
		return errors.New("task gid is empty")
	}
	if strings.TrimSpace(projectGID) == "" {
		return errors.New("project gid is empty")
	}
	body := map[string]any{
		"data": map[string]any{
			"project": projectGID,
		},
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	return c.post(ctx, token, "/tasks/"+taskGID+"/addProject", body, &payload)
}

func (c *Client) AddDependencies(ctx context.Context, token string, taskGID string, dependencyGIDs []string) error {
	if strings.TrimSpace(taskGID) == "" {
		return errors.New("task gid is empty")
	}
	if len(dependencyGIDs) == 0 {
		return errors.New("dependency gids are required")
	}
	body := map[string]any{
		"data": map[string]any{
			"dependencies": dependencyGIDs,
		},
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	return c.post(ctx, token, "/tasks/"+taskGID+"/addDependencies", body, &payload)
}

func (c *Client) projectsForWorkspace(ctx context.Context, token string, workspaceGID string) ([]Project, error) {
	var all []Project
	var offset string
	for {
		query := url.Values{}
		query.Set("archived", "false")
		query.Set("limit", "100")
		query.Set("workspace", workspaceGID)
		query.Set("opt_fields", "gid,name,workspace.gid,workspace.name")
		if offset != "" {
			query.Set("offset", offset)
		}
		var payload struct {
			Data     []Project `json:"data"`
			NextPage *struct {
				Offset string `json:"offset"`
			} `json:"next_page"`
		}
		if err := c.get(ctx, token, "/projects?"+query.Encode(), &payload); err != nil {
			return nil, err
		}
		all = append(all, payload.Data...)
		if payload.NextPage == nil || payload.NextPage.Offset == "" {
			return all, nil
		}
		offset = payload.NextPage.Offset
	}
}

func (c *Client) tasksForProject(ctx context.Context, token string, projectGID string) ([]Task, error) {
	var all []Task
	var offset string
	for {
		query := url.Values{}
		query.Set("limit", "100")
		query.Set("opt_fields", "gid,name,completed,permalink_url,parent.gid,parent.name,custom_fields.gid,custom_fields.display_value,custom_fields.enum_value.gid,custom_fields.enum_value.name")
		if offset != "" {
			query.Set("offset", offset)
		}
		var payload struct {
			Data     []Task `json:"data"`
			NextPage *struct {
				Offset string `json:"offset"`
			} `json:"next_page"`
		}
		if err := c.get(ctx, token, "/projects/"+projectGID+"/tasks?"+query.Encode(), &payload); err != nil {
			return nil, err
		}
		all = append(all, payload.Data...)
		if payload.NextPage == nil || payload.NextPage.Offset == "" {
			return all, nil
		}
		offset = payload.NextPage.Offset
	}
}

func (c *Client) get(ctx context.Context, token string, path string, dest any) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("asana token is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dharana-cli")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &APIError{StatusCode: res.StatusCode, Message: extractErrorMessage(body)}
	}

	return json.Unmarshal(body, dest)
}

func (c *Client) post(ctx context.Context, token string, path string, body any, dest any) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("asana token is empty")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dharana-cli")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &APIError{StatusCode: res.StatusCode, Message: extractErrorMessage(responseBody)}
	}

	return json.Unmarshal(responseBody, dest)
}

func extractErrorMessage(body []byte) string {
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if len(payload.Errors) == 0 {
		return ""
	}
	return payload.Errors[0].Message
}
