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
	"regexp"
	"strings"
	"time"
)

const DefaultBaseURL = "https://app.asana.com/api/1.0"

const taskOptFields = "gid,name,notes,html_notes,completed,due_on,permalink_url,parent.gid,parent.name,assignee.gid,assignee.name,assignee.email,projects.gid,projects.name,dependencies.gid,dependencies.name,custom_fields.gid,custom_fields.name,custom_fields.display_value,custom_fields.enum_value.gid,custom_fields.enum_value.name"

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Sleep      func(context.Context, time.Duration) error
	MaxRetries int
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
	Team      *Team     `json:"team,omitempty"`
	Permalink string    `json:"permalink_url,omitempty"`
}

type Team struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

type Task struct {
	GID          string        `json:"gid"`
	Name         string        `json:"name"`
	Notes        string        `json:"notes,omitempty"`
	HTMLNotes    string        `json:"html_notes,omitempty"`
	Completed    bool          `json:"completed,omitempty"`
	DueOn        string        `json:"due_on,omitempty"`
	Permalink    string        `json:"permalink_url,omitempty"`
	Parent       *TaskParent   `json:"parent,omitempty"`
	Assignee     *User         `json:"assignee,omitempty"`
	Projects     []Project     `json:"projects,omitempty"`
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
	Name         string `json:"name,omitempty"`
	Type         string `json:"type,omitempty"`
	Enabled      bool   `json:"enabled,omitempty"`
	DisplayValue string `json:"display_value,omitempty"`
	EnumValue    *struct {
		GID  string `json:"gid"`
		Name string `json:"name"`
	} `json:"enum_value,omitempty"`
	EnumOptions []EnumOption `json:"enum_options,omitempty"`
}

type EnumOption struct {
	GID     string `json:"gid"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled,omitempty"`
}

type CustomFieldSetting struct {
	GID         string      `json:"gid"`
	CustomField CustomField `json:"custom_field"`
}

type ProjectMembership struct {
	GID     string  `json:"gid"`
	User    User    `json:"user"`
	Project Project `json:"project,omitempty"`
}

type ProjectTemplateJob struct {
	GID        string   `json:"gid"`
	Status     string   `json:"status,omitempty"`
	NewProject *Project `json:"new_project,omitempty"`
}

type CreateProjectInput struct {
	Name         string
	WorkspaceGID string
	TeamGID      string
	Public       *bool
	Notes        string
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
	HTMLNotes    string
	CustomFields map[string]string
}

type UpdateTaskInput struct {
	Name         *string
	Notes        *string
	HTMLNotes    *string
	AssigneeGID  *string
	DueOn        *string
	Completed    *bool
	CustomFields map[string]string
}

type Story struct {
	GID       string `json:"gid"`
	Text      string `json:"text,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	CreatedBy *User  `json:"created_by,omitempty"`
}

type EventResource struct {
	GID          string `json:"gid"`
	ResourceType string `json:"resource_type"`
	Name         string `json:"name,omitempty"`
	Subtype      string `json:"subtype,omitempty"`
}

type EventChange struct {
	Field    string `json:"field,omitempty"`
	Action   string `json:"action,omitempty"`
	NewValue any    `json:"new_value,omitempty"`
}

type Event struct {
	GID       string         `json:"gid,omitempty"`
	Type      string         `json:"type"`
	Action    string         `json:"action"`
	CreatedAt string         `json:"created_at,omitempty"`
	Resource  EventResource  `json:"resource"`
	Parent    *EventResource `json:"parent,omitempty"`
	Change    EventChange    `json:"change,omitempty"`
}

type EventPage struct {
	Events  []Event `json:"data"`
	Sync    string  `json:"sync"`
	HasMore bool    `json:"has_more"`
}

type APIError struct {
	StatusCode int
	Message    string
	RetryAfter string
	RequestID  string
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

	res, err := c.doRead(req, httpClient)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, apiErrorFromResponse(res, body)
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
	if err := c.get(ctx, token, "/projects/"+gid+"?opt_fields=gid,name,workspace.gid,workspace.name,team.gid,team.name,permalink_url", &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

// Events returns one project event page. On HTTP 412 it returns the replacement
// cursor in the page together with the API error so callers can rebuild before
// committing that cursor.
func (c *Client) Events(ctx context.Context, token, resourceGID, syncToken string) (*EventPage, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("asana token is empty")
	}
	if strings.TrimSpace(resourceGID) == "" {
		return nil, errors.New("event resource gid is empty")
	}
	query := url.Values{"resource": {resourceGID}}
	if syncToken != "" {
		query.Set("sync", syncToken)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/events?"+query.Encode(), nil)
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
	res, err := c.doRead(req, httpClient)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var page EventPage
	_ = json.Unmarshal(body, &page)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if res.StatusCode == http.StatusPreconditionFailed && page.Sync == "" {
			page.Sync = syncTokenFromMessage(extractErrorMessage(body))
		}
		return &page, apiErrorFromResponse(res, body)
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (c *Client) CreateProject(ctx context.Context, token string, input CreateProjectInput) (*Project, error) {
	if strings.TrimSpace(input.Name) == "" {
		return nil, errors.New("project name is empty")
	}
	if strings.TrimSpace(input.WorkspaceGID) == "" {
		return nil, errors.New("workspace gid is empty")
	}
	data := map[string]any{"name": input.Name, "workspace": input.WorkspaceGID}
	if input.TeamGID != "" {
		data["team"] = input.TeamGID
	}
	if input.Public != nil {
		data["public"] = *input.Public
	}
	if input.Notes != "" {
		data["notes"] = input.Notes
	}
	var payload struct {
		Data Project `json:"data"`
	}
	if err := c.post(ctx, token, "/projects?opt_fields=gid,name,workspace.gid,workspace.name,team.gid,team.name,permalink_url", map[string]any{"data": data}, &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) InstantiateProjectTemplate(ctx context.Context, token string, templateGID string, name string) (*ProjectTemplateJob, error) {
	if strings.TrimSpace(templateGID) == "" {
		return nil, errors.New("template gid is empty")
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("project name is empty")
	}
	var payload struct {
		Data ProjectTemplateJob `json:"data"`
	}
	body := map[string]any{"data": map[string]any{"name": name}}
	if err := c.post(ctx, token, "/project_templates/"+templateGID+"/instantiateProject?opt_fields=gid,status,new_project.gid,new_project.name,new_project.workspace.gid,new_project.workspace.name", body, &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) CustomFieldSettingsForProject(ctx context.Context, token string, projectGID string) ([]CustomFieldSetting, error) {
	if strings.TrimSpace(projectGID) == "" {
		return nil, errors.New("project gid is empty")
	}
	var all []CustomFieldSetting
	var offset string
	for {
		query := url.Values{}
		query.Set("limit", "100")
		query.Set("opt_fields", "gid,custom_field.gid,custom_field.name,custom_field.type,custom_field.enum_options.gid,custom_field.enum_options.name,custom_field.enum_options.enabled")
		if offset != "" {
			query.Set("offset", offset)
		}
		var payload struct {
			Data     []CustomFieldSetting `json:"data"`
			NextPage *struct {
				Offset string `json:"offset"`
			} `json:"next_page"`
		}
		if err := c.get(ctx, token, "/projects/"+projectGID+"/custom_field_settings?"+query.Encode(), &payload); err != nil {
			return nil, err
		}
		all = append(all, payload.Data...)
		if payload.NextPage == nil || payload.NextPage.Offset == "" {
			return all, nil
		}
		offset = payload.NextPage.Offset
	}
}

func (c *Client) ProjectMemberships(ctx context.Context, token string, projectGID string) ([]ProjectMembership, error) {
	if strings.TrimSpace(projectGID) == "" {
		return nil, errors.New("project gid is empty")
	}
	var payload struct {
		Data []ProjectMembership `json:"data"`
	}
	if err := c.get(ctx, token, "/project_memberships?project="+url.QueryEscape(projectGID)+"&opt_fields=gid,user.gid,user.name,user.email", &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

func (c *Client) User(ctx context.Context, token string, userGID string) (*User, error) {
	if strings.TrimSpace(userGID) == "" {
		return nil, errors.New("user gid is empty")
	}
	var payload struct {
		Data User `json:"data"`
	}
	if err := c.get(ctx, token, "/users/"+userGID+"?opt_fields=gid,name,email", &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) Users(ctx context.Context, token string, workspaceGID string) ([]User, error) {
	if strings.TrimSpace(workspaceGID) == "" {
		return nil, errors.New("workspace gid is empty")
	}
	var all []User
	var offset string
	for {
		query := url.Values{}
		query.Set("workspace", workspaceGID)
		query.Set("limit", "100")
		query.Set("opt_fields", "gid,name,email")
		if offset != "" {
			query.Set("offset", offset)
		}
		var payload struct {
			Data     []User `json:"data"`
			NextPage *struct {
				Offset string `json:"offset"`
			} `json:"next_page"`
		}
		if err := c.get(ctx, token, "/users?"+query.Encode(), &payload); err != nil {
			return nil, err
		}
		all = append(all, payload.Data...)
		if payload.NextPage == nil || payload.NextPage.Offset == "" {
			return all, nil
		}
		offset = payload.NextPage.Offset
	}
}

func (c *Client) AddProjectMembers(ctx context.Context, token string, projectGID string, userGIDs []string) error {
	return c.projectMembersMutation(ctx, token, projectGID, userGIDs, "addMembers")
}

func (c *Client) RemoveProjectMembers(ctx context.Context, token string, projectGID string, userGIDs []string) error {
	return c.projectMembersMutation(ctx, token, projectGID, userGIDs, "removeMembers")
}

func (c *Client) projectMembersMutation(ctx context.Context, token string, projectGID string, userGIDs []string, action string) error {
	if strings.TrimSpace(projectGID) == "" {
		return errors.New("project gid is empty")
	}
	if len(userGIDs) == 0 {
		return errors.New("user gids are required")
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	return c.post(ctx, token, "/projects/"+projectGID+"/"+action, map[string]any{"data": map[string]any{"members": userGIDs}}, &payload)
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
	query.Set("opt_fields", taskOptFields)
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
	query.Set("opt_fields", taskOptFields)
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
	if err := c.get(ctx, token, "/tasks/"+gid+"?opt_fields="+url.QueryEscape(taskOptFields), &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
}

func (c *Client) UpdateTask(ctx context.Context, token string, gid string, input UpdateTaskInput) (*Task, error) {
	if strings.TrimSpace(gid) == "" {
		return nil, errors.New("task gid is empty")
	}
	data := map[string]any{}
	if input.Name != nil {
		data["name"] = *input.Name
	}
	if input.Notes != nil {
		data["notes"] = *input.Notes
	}
	if input.HTMLNotes != nil {
		data["html_notes"] = *input.HTMLNotes
	}
	if input.AssigneeGID != nil {
		if *input.AssigneeGID == "" {
			data["assignee"] = nil
		} else {
			data["assignee"] = *input.AssigneeGID
		}
	}
	if input.DueOn != nil {
		if *input.DueOn == "" {
			data["due_on"] = nil
		} else {
			data["due_on"] = *input.DueOn
		}
	}
	if input.Completed != nil {
		data["completed"] = *input.Completed
	}
	if len(input.CustomFields) > 0 {
		data["custom_fields"] = input.CustomFields
	}
	var payload struct {
		Data Task `json:"data"`
	}
	if err := c.put(ctx, token, "/tasks/"+url.PathEscape(gid)+"?opt_fields="+url.QueryEscape(taskOptFields), map[string]any{"data": data}, &payload); err != nil {
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
	if input.HTMLNotes != "" {
		data["html_notes"] = input.HTMLNotes
	} else if input.Notes != "" {
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

func (c *Client) SetParent(ctx context.Context, token string, taskGID string, parentGID string) error {
	if strings.TrimSpace(taskGID) == "" {
		return errors.New("task gid is empty")
	}
	if strings.TrimSpace(parentGID) == "" {
		return errors.New("parent gid is empty")
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	return c.post(ctx, token, "/tasks/"+url.PathEscape(taskGID)+"/setParent", map[string]any{"data": map[string]any{"parent": parentGID}}, &payload)
}

func (c *Client) AddStory(ctx context.Context, token string, taskGID string, text string) (*Story, error) {
	if strings.TrimSpace(taskGID) == "" {
		return nil, errors.New("task gid is empty")
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("story text is empty")
	}
	var payload struct {
		Data Story `json:"data"`
	}
	body := map[string]any{"data": map[string]any{"text": text}}
	if err := c.post(ctx, token, "/tasks/"+url.PathEscape(taskGID)+"/stories?opt_fields=gid,text,created_at,created_by.gid,created_by.name,created_by.email", body, &payload); err != nil {
		return nil, err
	}
	return &payload.Data, nil
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

func (c *Client) RemoveDependencies(ctx context.Context, token string, taskGID string, dependencyGIDs []string) error {
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
	return c.post(ctx, token, "/tasks/"+taskGID+"/removeDependencies", body, &payload)
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
		query.Set("opt_fields", taskOptFields)
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

	res, err := c.doRead(req, httpClient)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return apiErrorFromResponse(res, body)
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
		return apiErrorFromResponse(res, responseBody)
	}

	return json.Unmarshal(responseBody, dest)
}

func (c *Client) put(ctx context.Context, token string, path string, body any, dest any) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("asana token is empty")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+path, bytes.NewReader(data))
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
		return apiErrorFromResponse(res, responseBody)
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

func apiErrorFromResponse(res *http.Response, body []byte) *APIError {
	if res == nil {
		return &APIError{Message: extractErrorMessage(body)}
	}
	return &APIError{
		StatusCode: res.StatusCode,
		Message:    extractErrorMessage(body),
		RetryAfter: res.Header.Get("Retry-After"),
		RequestID:  firstHeader(res.Header, "X-Request-Id", "X-Asana-Request-Id", "Asana-Request-Id"),
	}
}

func firstHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func (c *Client) doRead(req *http.Request, httpClient *http.Client) (*http.Response, error) {
	maxRetries := c.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 0; ; attempt++ {
		response, err := httpClient.Do(req.Clone(req.Context()))
		if err != nil {
			return nil, err
		}
		if attempt >= maxRetries || !retryableReadStatus(response.StatusCode) {
			return response, nil
		}
		delay := retryDelay(response.Header.Get("Retry-After"), attempt, time.Now())
		if delay > time.Minute {
			return response, nil
		}
		_ = response.Body.Close()
		if err := c.sleep(req.Context(), delay); err != nil {
			return nil, err
		}
	}
}

func retryableReadStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func retryDelay(value string, attempt int, now time.Time) time.Duration {
	var delay time.Duration
	if seconds, err := time.ParseDuration(strings.TrimSpace(value) + "s"); err == nil && seconds > 0 {
		delay = seconds
	} else if parsed, err := http.ParseTime(value); err == nil {
		delay = parsed.Sub(now)
	}
	if delay <= 0 {
		delay = 250 * time.Millisecond * time.Duration(1<<attempt)
	}
	return delay
}

func (c *Client) sleep(ctx context.Context, delay time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var syncTokenPattern = regexp.MustCompile(`(?i)sync:\s*([^\s"']+)`)

func syncTokenFromMessage(message string) string {
	match := syncTokenPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
