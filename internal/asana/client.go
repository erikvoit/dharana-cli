package asana

import (
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
