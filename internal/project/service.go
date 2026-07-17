package project

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type AsanaClient interface {
	auth.AsanaClient
	Projects(ctx context.Context, token string, workspaceGID string) ([]asana.Project, error)
	Project(ctx context.Context, token string, gid string) (*asana.Project, error)
}

type ConfigStore interface {
	Load() (*config.File, error)
	Save(cfg *config.File) error
}

type Service struct {
	Auth   *auth.Service
	Asana  AsanaClient
	Config ConfigStore
}

type ListOptions struct {
	WorkspaceGID string
}

type SelectOptions struct {
	GID          string
	Name         string
	WorkspaceGID string
}

type ProjectValue struct {
	GID           string `json:"gid"`
	Name          string `json:"name"`
	WorkspaceGID  string `json:"workspace_gid"`
	WorkspaceName string `json:"workspace_name"`
}

type ListResult struct {
	Projects []ProjectValue `json:"projects"`
}

type SelectResult struct {
	ActiveProject ProjectValue `json:"active_project"`
	ConfigPath    string       `json:"config_path,omitempty"`
}

func NewService(authService *auth.Service) *Service {
	asanaClient := asana.NewClient("")
	return &Service{
		Auth:   authService,
		Asana:  asanaClient,
		Config: config.NewStore(),
	}
}

func (s *Service) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}

	projects, err := s.asana().Projects(ctx, resolved.Token, opts.WorkspaceGID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list Asana projects.")
	}

	values := make([]ProjectValue, 0, len(projects))
	for _, p := range projects {
		values = append(values, toProjectValue(p))
	}
	return &ListResult{Projects: values}, nil
}

func (s *Service) Select(ctx context.Context, opts SelectOptions) (*SelectResult, error) {
	opts.GID = strings.TrimSpace(opts.GID)
	opts.Name = strings.TrimSpace(opts.Name)
	if (opts.GID == "") == (opts.Name == "") {
		return nil, output.NewError("PROJECT_REFERENCE_REQUIRED", "Select a project by exactly one of --gid or --name.")
	}

	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}

	var selected *asana.Project
	if opts.GID != "" {
		selected, err = s.asana().Project(ctx, resolved.Token, opts.GID)
		if err != nil {
			return nil, mapAsanaError(err, "Could not read the selected Asana project.")
		}
	} else {
		projects, err := s.asana().Projects(ctx, resolved.Token, opts.WorkspaceGID)
		if err != nil {
			return nil, mapAsanaError(err, "Could not list Asana projects.")
		}
		matches := exactNameMatches(projects, opts.Name)
		if len(matches) == 0 {
			return nil, output.NewError("PROJECT_NOT_FOUND", "No Asana project matched the supplied exact name.")
		}
		if len(matches) > 1 {
			candidates := make([]ProjectValue, 0, len(matches))
			for _, match := range matches {
				candidates = append(candidates, toProjectValue(match))
			}
			return nil, output.NewErrorWithCandidates("AMBIGUOUS_PROJECT", "Multiple Asana projects matched the supplied exact name.", candidates)
		}
		selected = &matches[0]
	}

	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	value := toProjectValue(*selected)
	cfg.ActiveProject = &config.ProjectConfig{
		GID:           value.GID,
		Name:          value.Name,
		WorkspaceGID:  value.WorkspaceGID,
		WorkspaceName: value.WorkspaceName,
	}
	if err := s.config().Save(cfg); err != nil {
		return nil, output.NewError("CONFIG_WRITE_FAILED", "Could not save local configuration.")
	}

	return &SelectResult{ActiveProject: value}, nil
}

func (s *Service) ShowConfig() (*config.File, error) {
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	return cfg, nil
}

func (s *Service) resolveToken() (*auth.ResolvedToken, error) {
	if s.Auth == nil {
		return nil, output.NewError("AUTH_UNAVAILABLE", "Authentication service is not configured.")
	}
	resolved, err := s.Auth.ResolveToken()
	if err != nil {
		if errors.Is(err, auth.ErrTokenNotFound) {
			return nil, output.NewError("TOKEN_NOT_CONFIGURED", "No Asana token is configured. Set one with auth configure or an environment variable.")
		}
		return nil, output.NewError("TOKEN_READ_FAILED", "Could not read the configured Asana token.")
	}
	return resolved, nil
}

func (s *Service) asana() AsanaClient {
	if s.Asana != nil {
		return s.Asana
	}
	return asana.NewClient("")
}

func (s *Service) config() ConfigStore {
	if s.Config != nil {
		return s.Config
	}
	return config.NewStore()
}

func exactNameMatches(projects []asana.Project, name string) []asana.Project {
	var matches []asana.Project
	for _, p := range projects {
		if p.Name == name {
			matches = append(matches, p)
		}
	}
	return matches
}

func toProjectValue(p asana.Project) ProjectValue {
	return ProjectValue{
		GID:           p.GID,
		Name:          p.Name,
		WorkspaceGID:  p.Workspace.GID,
		WorkspaceName: p.Workspace.Name,
	}
}

func mapAsanaError(err error, fallback string) error {
	var apiErr *asana.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return output.NewError("INVALID_AUTH", "Asana rejected the configured token.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return output.NewError("ASANA_ACCESS_DENIED", "The configured token does not have access to this Asana resource.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return output.NewError("PROJECT_NOT_FOUND", "No Asana project matched the supplied GID.")
	}
	return output.NewError("ASANA_REQUEST_FAILED", fallback)
}
