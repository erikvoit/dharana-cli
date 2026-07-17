package doctor

import (
	"context"
	"errors"
	"net/http"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type ConfigStore interface {
	Load() (*config.File, error)
}

type AsanaClient interface {
	CurrentUser(ctx context.Context, token string) (*asana.User, error)
	Project(ctx context.Context, token string, gid string) (*asana.Project, error)
}

type Service struct {
	Auth   *auth.Service
	Asana  AsanaClient
	Config ConfigStore
}

type Result struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func NewService(authService *auth.Service) *Service {
	return &Service{
		Auth:   authService,
		Asana:  asana.NewClient(""),
		Config: config.NewStore(),
	}
}

func (s *Service) Run(ctx context.Context) (*Result, error) {
	var checks []Check

	resolved, err := s.auth().ResolveToken()
	if err != nil {
		checks = append(checks, Check{Name: "auth", OK: false, Code: "TOKEN_NOT_CONFIGURED", Message: "No Asana token is configured."})
		return finish(checks), nil
	}

	user, err := s.asana().CurrentUser(ctx, resolved.Token)
	if err != nil {
		checks = append(checks, Check{Name: "auth", OK: false, Code: mapAuthCode(err), Message: "Could not validate Asana authentication."})
		return finish(checks), nil
	}
	checks = append(checks, Check{Name: "auth", OK: true, Message: "Authenticated as " + user.Name + "."})

	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}

	if cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		checks = append(checks, Check{Name: "project", OK: false, Code: "PROJECT_NOT_CONFIGURED", Message: "No active project is configured."})
	} else {
		projectValue, err := s.asana().Project(ctx, resolved.Token, cfg.ActiveProject.GID)
		if err != nil {
			checks = append(checks, Check{Name: "project", OK: false, Code: mapProjectCode(err), Message: "Could not access the configured Asana project."})
		} else {
			checks = append(checks, Check{Name: "project", OK: true, Message: "Active project is " + projectValue.Name + "."})
		}
	}

	missing := missingTaskTypes(cfg.TaskTypes)
	if len(missing) > 0 {
		checks = append(checks, Check{Name: "task_types", OK: false, Code: "TASK_TYPES_NOT_CONFIGURED", Message: "Missing task type mappings: " + joinMissing(missing) + "."})
	} else {
		checks = append(checks, Check{Name: "task_types", OK: true, Message: "Required task type mappings are configured."})
	}

	return finish(checks), nil
}

func finish(checks []Check) *Result {
	result := &Result{OK: true, Checks: checks}
	for _, check := range checks {
		if !check.OK {
			result.OK = false
			break
		}
	}
	return result
}

func (s *Service) auth() *auth.Service {
	if s.Auth != nil {
		return s.Auth
	}
	return auth.NewService()
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

func mapAuthCode(err error) string {
	var apiErr *asana.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return "INVALID_AUTH"
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return "INVALID_AUTH"
	}
	return "ASANA_REQUEST_FAILED"
}

func mapProjectCode(err error) string {
	var apiErr *asana.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return "PROJECT_NOT_FOUND"
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return "ASANA_ACCESS_DENIED"
	}
	return "ASANA_REQUEST_FAILED"
}

func missingTaskTypes(types config.TaskTypes) []string {
	var missing []string
	if types.Epic == "" {
		missing = append(missing, "epic")
	}
	if types.Story == "" {
		missing = append(missing, "story")
	}
	if types.Bug == "" {
		missing = append(missing, "bug")
	}
	if types.Spike == "" {
		missing = append(missing, "spike")
	}
	return missing
}

func joinMissing(values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += ", " + value
	}
	return out
}
