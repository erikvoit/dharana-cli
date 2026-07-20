package work

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
	"github.com/erikvoit/dharana-cli/internal/richtext"
)

type AsanaClient interface {
	TasksByName(ctx context.Context, token string, projectGID string, name string) ([]asana.Task, error)
	ProjectTasks(ctx context.Context, token string, projectGID string, limit int, offset string) (*asana.TaskPage, error)
	Subtasks(ctx context.Context, token string, taskGID string, limit int, offset string) (*asana.TaskPage, error)
	Task(ctx context.Context, token string, gid string) (*asana.Task, error)
	CreateTask(ctx context.Context, token string, input asana.CreateTaskInput) (*asana.Task, error)
	UpdateTask(ctx context.Context, token string, gid string, input asana.UpdateTaskInput) (*asana.Task, error)
	AddTaskToProject(ctx context.Context, token string, taskGID string, projectGID string) error
	SetParent(ctx context.Context, token string, taskGID string, parentGID string) error
	AddStory(ctx context.Context, token string, taskGID string, text string) (*asana.Story, error)
	User(ctx context.Context, token string, userGID string) (*asana.User, error)
	Users(ctx context.Context, token string, workspaceGID string) ([]asana.User, error)
	CustomFieldSettingsForProject(ctx context.Context, token string, projectGID string) ([]asana.CustomFieldSetting, error)
	AddDependencies(ctx context.Context, token string, taskGID string, dependencyGIDs []string) error
	RemoveDependencies(ctx context.Context, token string, taskGID string, dependencyGIDs []string) error
}

type ConfigStore interface {
	Load() (*config.File, error)
}

type Service struct {
	Auth   *auth.Service
	Asana  AsanaClient
	Config ConfigStore
	Refs   RefStore
}

type CreateEpicOptions struct {
	Name           string
	Notes          string
	Description    *richtext.Description
	DryRun         bool
	Idempotent     bool
	IdempotencyKey string
}

type EpicValue struct {
	GID                string                `json:"gid,omitempty"`
	Ref                string                `json:"ref"`
	Name               string                `json:"name"`
	ProjectGID         string                `json:"project_gid"`
	ProjectName        string                `json:"project_name"`
	WorkspaceGID       string                `json:"workspace_gid"`
	WorkspaceName      string                `json:"workspace_name"`
	TypeMapping        string                `json:"type_mapping"`
	TypeFieldGID       string                `json:"type_field_gid,omitempty"`
	Description        *richtext.Description `json:"description,omitempty"`
	Permalink          string                `json:"permalink_url,omitempty"`
	Created            bool                  `json:"created"`
	DryRun             bool                  `json:"dry_run"`
	IdempotencyKey     string                `json:"idempotency_key,omitempty"`
	IdempotentExisting bool                  `json:"idempotent_existing,omitempty"`
}

type CreateEpicResult struct {
	Epic EpicValue `json:"epic"`
}

func NewService(authService *auth.Service) *Service {
	return &Service{
		Auth:   authService,
		Asana:  asana.NewClient(""),
		Config: config.NewStore(),
		Refs:   refcache.NewStore(),
	}
}

func (s *Service) CreateEpic(ctx context.Context, opts CreateEpicOptions) (*CreateEpicResult, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.IdempotencyKey = strings.TrimSpace(opts.IdempotencyKey)
	if opts.IdempotencyKey != "" {
		opts.Idempotent = true
	}
	if opts.Name == "" {
		return nil, output.NewError("EPIC_NAME_REQUIRED", "Provide an epic name.")
	}
	notes, htmlNotes, err := descriptionPayload("", opts.Notes, opts.Description)
	if err != nil {
		return nil, err
	}

	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured. Run project select first.")
	}
	if cfg.TaskTypes.Epic == "" {
		return nil, output.NewError("EPIC_TYPE_NOT_CONFIGURED", "No Epic task type or work-type mapping is configured.")
	}

	base := EpicValue{
		Ref:            "EPIC:" + opts.Name,
		Name:           opts.Name,
		ProjectGID:     cfg.ActiveProject.GID,
		ProjectName:    cfg.ActiveProject.Name,
		WorkspaceGID:   cfg.ActiveProject.WorkspaceGID,
		WorkspaceName:  cfg.ActiveProject.WorkspaceName,
		TypeMapping:    cfg.TaskTypes.Epic,
		TypeFieldGID:   cfg.TaskTypes.FieldGID,
		Description:    opts.Description,
		DryRun:         opts.DryRun,
		IdempotencyKey: opts.IdempotencyKey,
	}

	matches, err := s.asana().TasksByName(ctx, resolved.Token, cfg.ActiveProject.GID, opts.Name)
	if err != nil {
		return nil, mapAsanaError(err, "Could not check for duplicate epics.")
	}
	if len(matches) > 0 {
		if opts.Idempotent {
			existing := matches[0]
			base.GID = existing.GID
			base.Permalink = existing.Permalink
			base.IdempotentExisting = true
			return &CreateEpicResult{Epic: base}, nil
		}
		candidates := make([]EpicValue, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, EpicValue{
				GID:         match.GID,
				Ref:         "EPIC:" + match.Name,
				Name:        match.Name,
				ProjectGID:  cfg.ActiveProject.GID,
				ProjectName: cfg.ActiveProject.Name,
				Permalink:   match.Permalink,
			})
		}
		return nil, output.NewErrorWithCandidates("DUPLICATE_EPIC", "An epic with this exact name already exists in the active project.", candidates)
	}

	if opts.DryRun {
		return &CreateEpicResult{Epic: base}, nil
	}

	task, err := s.asana().CreateTask(ctx, resolved.Token, asana.CreateTaskInput{
		Name:         opts.Name,
		ProjectGID:   cfg.ActiveProject.GID,
		WorkspaceGID: cfg.ActiveProject.WorkspaceGID,
		Notes:        notes,
		HTMLNotes:    htmlNotes,
		CustomFields: creationCustomFields(cfg, cfg.TaskTypes.Epic, false),
	})
	if err != nil {
		return nil, mapAsanaError(err, "Could not create the Asana epic.")
	}

	base.GID = task.GID
	base.Permalink = task.Permalink
	base.Created = true
	return &CreateEpicResult{Epic: base}, nil
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

func mapAsanaError(err error, fallback string) error {
	var apiErr *asana.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return output.NewError("INVALID_AUTH", "Asana rejected the configured token.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return output.NewError("ASANA_ACCESS_DENIED", "The configured token does not have access to this Asana resource.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return output.NewError("ASANA_NOT_FOUND", "The requested Asana resource was not found.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		return output.NewError("ASANA_CONFLICT", "Asana reported a conflicting remote state. Re-read the work item and retry.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
		return output.NewError("ASANA_VALIDATION_FAILED", "Asana rejected the requested mutation as invalid.")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
		if apiErr.RetryAfter != "" {
			return output.NewErrorWithDetails("ASANA_RATE_LIMITED", "Asana rate-limited the request. Retry after the indicated delay.", map[string]string{"retry_after": apiErr.RetryAfter, "request_id": apiErr.RequestID})
		}
		return output.NewErrorWithDetails("ASANA_RATE_LIMITED", "Asana rate-limited the request.", map[string]string{"request_id": apiErr.RequestID})
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 500 {
		return output.NewErrorWithDetails("ASANA_TRANSIENT_FAILURE", "Asana returned a transient service failure. Retry the operation after re-reading current work state.", map[string]string{"request_id": apiErr.RequestID})
	}
	return output.NewError("ASANA_REQUEST_FAILED", fallback)
}
