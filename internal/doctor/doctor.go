package doctor

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	OK                  bool          `json:"ok"`
	EffectiveAuthSource string        `json:"effective_auth_source,omitempty"`
	EffectiveProfile    string        `json:"effective_profile,omitempty"`
	AuthProvider        auth.Provider `json:"auth_provider,omitempty"`
	ScopesKnown         bool          `json:"scopes_known"`
	GrantedScopes       []string      `json:"granted_scopes,omitempty"`
	EffectiveContext    string        `json:"effective_context,omitempty"`
	CapabilitySchema    string        `json:"capability_schema_version,omitempty"`
	WorkflowMode        string        `json:"workflow_mode,omitempty"`
	CheckedAt           string        `json:"checked_at,omitempty"`
	Checks              []Check       `json:"checks"`
	RepairPlan          []RepairStep  `json:"repair_plan,omitempty"`
}

type Check struct {
	Name      string   `json:"name"`
	OK        bool     `json:"ok"`
	Code      string   `json:"code,omitempty"`
	Message   string   `json:"message"`
	NextSteps []string `json:"next_steps,omitempty"`
}

type RepairStep struct {
	Code        string   `json:"code"`
	Kind        string   `json:"kind"`
	Destructive bool     `json:"destructive"`
	Command     string   `json:"command,omitempty"`
	Details     []string `json:"details,omitempty"`
}

func NewService(authService *auth.Service) *Service {
	return &Service{
		Auth:   authService,
		Asana:  asana.NewClient(""),
		Config: config.NewStore(),
	}
}

func (s *Service) Run(ctx context.Context) (*Result, error) {
	return s.RunWithOptions(ctx, false, false)
}

func (s *Service) RunWithOptions(ctx context.Context, repairPlan bool, repairDryRun bool) (*Result, error) {
	var checks []Check

	resolved, err := s.auth().ResolveToken()
	if err != nil {
		checks = append(checks, Check{Name: "auth", OK: false, Code: "TOKEN_NOT_CONFIGURED", Message: "No Asana token is configured.", NextSteps: []string{"dharana auth configure --token <pat> --validate --json"}})
		return finish(checks, repairPlan, repairDryRun, "", ""), nil
	}

	user, err := s.asana().CurrentUser(ctx, resolved.Token)
	if err != nil {
		checks = append(checks, Check{Name: "auth", OK: false, Code: mapAuthCode(err), Message: "Could not validate Asana authentication.", NextSteps: []string{"dharana auth configure --token <pat> --validate --json"}})
		return finish(checks, repairPlan, repairDryRun, resolved.Source, ""), nil
	}
	checks = append(checks, Check{Name: "auth", OK: true, Message: "Authenticated as " + user.Name + "."})
	if resolved.ScopeKnown {
		if err := s.auth().RequireScopes(ctx, auth.DefaultScopes()); err != nil {
			checks = append(checks, Check{Name: "oauth_scopes", OK: false, Code: "OAUTH_SCOPES_MISSING", Message: "The OAuth profile does not grant all scopes needed by the full Dharana capability set.", NextSteps: []string{"dharana auth login --profile " + resolved.Profile + " --json"}})
		} else {
			checks = append(checks, Check{Name: "oauth_scopes", OK: true, Message: "OAuth scopes cover the full Dharana capability set."})
		}
	} else {
		checks = append(checks, Check{Name: "oauth_scopes", OK: true, Message: "Scope grants are not introspectable for this token provider."})
	}

	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}

	if cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		checks = append(checks, Check{Name: "project", OK: false, Code: "PROJECT_NOT_CONFIGURED", Message: "No active project is configured.", NextSteps: []string{"dharana context list --json", "dharana project adopt <project> --apply --json"}})
	} else {
		projectValue, err := s.asana().Project(ctx, resolved.Token, cfg.ActiveProject.GID)
		if err != nil {
			checks = append(checks, Check{Name: "project", OK: false, Code: mapProjectCode(err), Message: "Could not access the configured Asana project.", NextSteps: []string{"dharana project inspect " + cfg.ActiveProject.GID + " --json"}})
		} else {
			checks = append(checks, Check{Name: "project", OK: true, Message: "Active project is " + projectValue.Name + "."})
		}
	}

	missing := missingTaskTypes(cfg.TaskTypes)
	if len(missing) > 0 {
		checks = append(checks, Check{Name: "task_types", OK: false, Code: "TASK_TYPES_NOT_CONFIGURED", Message: "Missing task type mappings: " + joinMissing(missing) + ".", NextSteps: []string{"dharana project adopt <project> --apply --json", "dharana workflow provision --mode custom-fields --dry-run --json"}})
	} else {
		checks = append(checks, Check{Name: "task_types", OK: true, Message: "Required task type mappings are configured."})
	}
	if cfg.ActiveProject != nil && cfg.ActiveProject.GID != "" {
		checks = append(checks, Check{Name: "reference_cache", OK: true, Message: "Reference cache ownership is validated when refs are read or refreshed."})
	}

	contextName := cfg.ActiveContext
	if contextName == "" && cfg.ActiveProject != nil {
		contextName = "active_project"
	}
	result := finish(checks, repairPlan, repairDryRun, resolved.Source, contextName)
	result.EffectiveProfile = resolved.Profile
	result.AuthProvider = resolved.Provider
	result.ScopesKnown = resolved.ScopeKnown
	result.GrantedScopes = resolved.Scopes
	if cfg.TaskTypes.FieldGID != "" {
		result.WorkflowMode = "custom-fields"
	}
	return result, nil
}

func finish(checks []Check, repairPlan bool, repairDryRun bool, authSource string, contextName string) *Result {
	result := &Result{OK: true, Checks: checks, EffectiveAuthSource: authSource, EffectiveContext: contextName, CapabilitySchema: "mvp-plus-5", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, check := range checks {
		if !check.OK {
			result.OK = false
			if repairPlan || repairDryRun {
				result.RepairPlan = append(result.RepairPlan, repairStepForCheck(check, repairDryRun))
			}
		}
	}
	return result
}

func repairStepForCheck(check Check, dryRun bool) RepairStep {
	step := RepairStep{Code: check.Code, Kind: "human", Details: check.NextSteps}
	switch check.Code {
	case "TOKEN_NOT_CONFIGURED", "INVALID_AUTH":
		step.Command = "dharana auth configure --token <pat> --validate --json"
	case "PROJECT_NOT_CONFIGURED":
		step.Command = "dharana project adopt <project> --apply --json"
	case "TASK_TYPES_NOT_CONFIGURED":
		step.Kind = "operator_approved"
		step.Command = "dharana workflow provision --mode custom-fields --dry-run --json"
		if !dryRun {
			step.Command = "dharana workflow provision --mode custom-fields --apply --json"
		}
	}
	return step
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
