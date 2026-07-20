package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
)

type AsanaClient interface {
	auth.AsanaClient
	Projects(ctx context.Context, token string, workspaceGID string) ([]asana.Project, error)
	Project(ctx context.Context, token string, gid string) (*asana.Project, error)
	CreateProject(ctx context.Context, token string, input asana.CreateProjectInput) (*asana.Project, error)
	InstantiateProjectTemplate(ctx context.Context, token string, templateGID string, name string) (*asana.ProjectTemplateJob, error)
	CustomFieldSettingsForProject(ctx context.Context, token string, projectGID string) ([]asana.CustomFieldSetting, error)
	WorkspaceCustomFields(ctx context.Context, token string, workspaceGID string) ([]asana.CustomField, error)
	CreateCustomField(ctx context.Context, token string, input asana.CreateCustomFieldInput) (*asana.CustomField, error)
	CreateEnumOption(ctx context.Context, token string, fieldGID string, name string) (*asana.EnumOption, error)
	AddCustomFieldToProject(ctx context.Context, token string, projectGID string, fieldGID string) error
	ProjectMemberships(ctx context.Context, token string, projectGID string) ([]asana.ProjectMembership, error)
	User(ctx context.Context, token string, userGID string) (*asana.User, error)
	Users(ctx context.Context, token string, workspaceGID string) ([]asana.User, error)
	AddProjectMembers(ctx context.Context, token string, projectGID string, userGIDs []string) error
	RemoveProjectMembers(ctx context.Context, token string, projectGID string, userGIDs []string) error
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
	TeamGID       string `json:"team_gid,omitempty"`
	TeamName      string `json:"team_name,omitempty"`
	Permalink     string `json:"permalink_url,omitempty"`
}

type ListResult struct {
	Projects []ProjectValue `json:"projects"`
}

type SelectResult struct {
	ActiveProject ProjectValue `json:"active_project"`
	ConfigPath    string       `json:"config_path,omitempty"`
}

type InspectResult struct {
	Project       ProjectValue       `json:"project"`
	Fields        []FieldValue       `json:"fields"`
	Mappings      MappingStatus      `json:"mappings"`
	Members       []MemberValue      `json:"members,omitempty"`
	Ready         bool               `json:"ready"`
	Problems      []Problem          `json:"problems,omitempty"`
	Repairability []RepairCapability `json:"repairability,omitempty"`
}

type FieldValue struct {
	GID         string            `json:"gid"`
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`
	EnumOptions []EnumOptionValue `json:"enum_options,omitempty"`
	DetectedAs  []string          `json:"detected_as,omitempty"`
}

type EnumOptionValue struct {
	GID        string `json:"gid"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	DetectedAs string `json:"detected_as,omitempty"`
}

type MappingStatus struct {
	WorkflowMode string          `json:"workflow_mode"`
	TaskTypes    []DetectedValue `json:"task_types"`
	Fields       []DetectedValue `json:"fields"`
}

type DetectedValue struct {
	Name       string `json:"name"`
	Configured string `json:"configured,omitempty"`
	GID        string `json:"gid,omitempty"`
	Source     string `json:"source"`
	Status     string `json:"status"`
}

type Problem struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	NextSteps []string `json:"next_steps,omitempty"`
}

type RepairCapability struct {
	Code        string `json:"code"`
	Automatic   bool   `json:"automatic"`
	Destructive bool   `json:"destructive"`
	Command     string `json:"command,omitempty"`
}

type AdoptOptions struct {
	Ref     string
	Context string
	DryRun  bool
	Apply   bool
}

type AdoptResult struct {
	Project           ProjectValue   `json:"project"`
	ContextName       string         `json:"context_name"`
	ProposedConfig    config.File    `json:"proposed_config"`
	Applied           bool           `json:"applied"`
	Ready             bool           `json:"ready"`
	Diagnostics       *InspectResult `json:"diagnostics,omitempty"`
	SuggestedCommands []string       `json:"suggested_next_commands"`
}

type CreateOptions struct {
	Name         string
	WorkspaceGID string
	TeamGID      string
	Privacy      string
	DryRun       bool
}

type CreateResult struct {
	Project           *ProjectValue `json:"project,omitempty"`
	DryRun            bool          `json:"dry_run"`
	Created           bool          `json:"created"`
	ProposedRemote    []string      `json:"proposed_remote_mutations,omitempty"`
	Reconciliation    []string      `json:"reconciliation_actions,omitempty"`
	SuggestedCommands []string      `json:"suggested_next_commands,omitempty"`
}

type TemplateOptions struct {
	TemplateGID string
	Name        string
	DryRun      bool
}

type TemplateResult struct {
	Job               *asana.ProjectTemplateJob `json:"job,omitempty"`
	DryRun            bool                      `json:"dry_run"`
	ProposedRemote    []string                  `json:"proposed_remote_mutations,omitempty"`
	SuggestedCommands []string                  `json:"suggested_next_commands,omitempty"`
}

type ProvisionOptions struct {
	Mode   string
	DryRun bool
	Apply  bool
}

type ProvisionResult struct {
	Mode           string                `json:"mode"`
	DryRun         bool                  `json:"dry_run"`
	Applied        bool                  `json:"applied"`
	Partial        bool                  `json:"partial,omitempty"`
	Supported      bool                  `json:"supported"`
	StateProvision *StateProvisionResult `json:"state_provision,omitempty"`
	ProposedRemote []string              `json:"proposed_remote_mutations,omitempty"`
	ProposedLocal  []string              `json:"proposed_local_mutations,omitempty"`
	Problems       []Problem             `json:"problems,omitempty"`
	Remediation    []string              `json:"remediation,omitempty"`
}

type MemberValue struct {
	GID   string `json:"gid"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type MemberOptions struct {
	User   string
	DryRun bool
}

type MemberListResult struct {
	Project ProjectValue  `json:"project"`
	Members []MemberValue `json:"members"`
}

type MemberMutationResult struct {
	Project            ProjectValue `json:"project"`
	User               MemberValue  `json:"user"`
	DryRun             bool         `json:"dry_run"`
	Added              bool         `json:"added,omitempty"`
	Removed            bool         `json:"removed,omitempty"`
	IdempotentExisting bool         `json:"idempotent_existing,omitempty"`
	Found              bool         `json:"found"`
	SideEffects        []string     `json:"side_effects,omitempty"`
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

func (s *Service) Inspect(ctx context.Context, ref string) (*InspectResult, error) {
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	projectValue, err := s.resolveProject(ctx, resolved.Token, ref, "")
	if err != nil {
		return nil, err
	}
	fields, err := s.asana().CustomFieldSettingsForProject(ctx, resolved.Token, projectValue.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not inspect project custom fields.")
	}
	members, _ := s.asana().ProjectMemberships(ctx, resolved.Token, projectValue.GID)
	result := buildInspectResult(toProjectValue(*projectValue), cfg, fields, members)
	return result, nil
}

func (s *Service) InspectActive(ctx context.Context) (*InspectResult, error) {
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured.")
	}
	return s.Inspect(ctx, cfg.ActiveProject.GID)
}

func (s *Service) Adopt(ctx context.Context, opts AdoptOptions) (*AdoptResult, error) {
	opts.Ref = strings.TrimSpace(opts.Ref)
	opts.Context = strings.TrimSpace(opts.Context)
	if opts.Ref == "" {
		return nil, output.NewError("PROJECT_REFERENCE_REQUIRED", "Provide a project GID or exact name to adopt.")
	}
	if opts.Apply && opts.DryRun {
		return nil, output.NewError("ADOPT_MODE_CONFLICT", "Use only one of --dry-run or --apply.")
	}
	if !opts.Apply {
		opts.DryRun = true
	}
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	projectValue, err := s.resolveProject(ctx, resolved.Token, opts.Ref, "")
	if err != nil {
		return nil, err
	}
	base, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if base == nil {
		base = &config.File{}
	}
	proposed := *base
	value := toProjectValue(*projectValue)
	proposed.ActiveProject = &config.ProjectConfig{GID: value.GID, Name: value.Name, WorkspaceGID: value.WorkspaceGID, WorkspaceName: value.WorkspaceName}
	if opts.Context == "" {
		opts.Context = safeContextName(value.Name)
	}
	proposed.ActiveContext = opts.Context
	proposed.UpsertContext(opts.Context, *proposed.ActiveProject)
	userGID := ""
	if resolved.User != nil {
		userGID = resolved.User.GID
	}
	proposed.BindContextIdentity(opts.Context, resolved.Profile, userGID)
	discoverDefaultMappings(&proposed, s.inspectFieldsBestEffort(ctx, resolved.Token, value.GID))

	result := &AdoptResult{
		Project:        value,
		ContextName:    opts.Context,
		ProposedConfig: proposed,
		SuggestedCommands: []string{
			"dharana workflow provision --mode custom-fields --dry-run --json",
			"dharana doctor --json",
			"dharana refs refresh --json",
			"dharana epic create \"Payment recovery\" --dry-run --json",
		},
	}
	if !opts.Apply {
		return result, nil
	}
	if err := s.config().Save(&proposed); err != nil {
		return nil, output.NewError("CONFIG_WRITE_FAILED", "Could not save adopted project configuration.")
	}
	_ = (&refcache.Store{Project: proposed.ActiveProject}).Save(&refcache.Cache{})
	result.Applied = true
	diagnostics, err := s.Inspect(ctx, value.GID)
	if err == nil {
		result.Diagnostics = diagnostics
		result.Ready = diagnostics.Ready
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, opts CreateOptions) (*CreateResult, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.WorkspaceGID = strings.TrimSpace(opts.WorkspaceGID)
	opts.TeamGID = strings.TrimSpace(opts.TeamGID)
	opts.Privacy = strings.TrimSpace(opts.Privacy)
	if opts.Name == "" {
		return nil, output.NewError("PROJECT_NAME_REQUIRED", "Provide a project name.")
	}
	if opts.WorkspaceGID == "" {
		return nil, output.NewError("WORKSPACE_REQUIRED", "Provide --workspace for project creation.")
	}
	if opts.Privacy != "" && opts.Privacy != "private" && opts.Privacy != "team" {
		return nil, output.NewError("INVALID_PROJECT_PRIVACY", "Project privacy must be private or team.")
	}
	result := &CreateResult{
		DryRun: opts.DryRun,
		ProposedRemote: []string{
			fmt.Sprintf("create Asana project %q in workspace %s", opts.Name, opts.WorkspaceGID),
			"inspect and adopt the created project",
			"run workflow provisioning or return remediation if unsupported",
		},
	}
	if opts.DryRun {
		return result, nil
	}
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	var public *bool
	if opts.Privacy != "" {
		value := opts.Privacy == "team"
		public = &value
	}
	projectValue, err := s.asana().CreateProject(ctx, resolved.Token, asana.CreateProjectInput{Name: opts.Name, WorkspaceGID: opts.WorkspaceGID, TeamGID: opts.TeamGID, Public: public})
	if err != nil {
		result.Reconciliation = []string{"If Asana created the project, run dharana project adopt <project-gid> --apply --json."}
		return result, mapAsanaError(err, "Could not create Asana project.")
	}
	value := toProjectValue(*projectValue)
	result.Project = &value
	result.Created = true
	result.SuggestedCommands = []string{"dharana project adopt " + value.GID + " --apply --json", "dharana workflow provision --mode custom-fields --dry-run --json"}
	return result, nil
}

func (s *Service) CreateFromTemplate(ctx context.Context, opts TemplateOptions) (*TemplateResult, error) {
	opts.TemplateGID = strings.TrimSpace(opts.TemplateGID)
	opts.Name = strings.TrimSpace(opts.Name)
	if opts.TemplateGID == "" {
		return nil, output.NewError("TEMPLATE_REQUIRED", "Provide a project template GID.")
	}
	if opts.Name == "" {
		return nil, output.NewError("PROJECT_NAME_REQUIRED", "Provide --name for the created project.")
	}
	result := &TemplateResult{DryRun: opts.DryRun, ProposedRemote: []string{"instantiate Asana project template " + opts.TemplateGID + " as " + opts.Name, "poll the returned job with bounded waiting", "inspect and adopt the resulting project"}}
	if opts.DryRun {
		return result, nil
	}
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	job, err := s.asana().InstantiateProjectTemplate(ctx, resolved.Token, opts.TemplateGID, opts.Name)
	if err != nil {
		return result, mapAsanaError(err, "Could not instantiate Asana project template.")
	}
	result.Job = job
	result.SuggestedCommands = []string{"dharana project adopt <new-project-gid> --apply --json", "dharana doctor --json"}
	return result, nil
}

func (s *Service) Provision(ctx context.Context, opts ProvisionOptions) (*ProvisionResult, error) {
	opts.Mode = strings.TrimSpace(opts.Mode)
	if opts.Mode == "" {
		return nil, output.NewError("WORKFLOW_MODE_REQUIRED", "Provide --mode custom-fields or --mode native-types.")
	}
	if opts.Mode != "custom-fields" && opts.Mode != "native-types" {
		return nil, output.NewError("INVALID_WORKFLOW_MODE", "Workflow mode must be custom-fields or native-types.")
	}
	if opts.Apply && opts.DryRun {
		return nil, output.NewError("PROVISION_MODE_CONFLICT", "Use only one of --dry-run or --apply.")
	}
	result := &ProvisionResult{Mode: opts.Mode, DryRun: !opts.Apply, Supported: opts.Mode == "custom-fields"}
	result.ProposedRemote = []string{"create or reuse the Dharana State enum field", "ensure every canonical workflow state exists", "attach the state field to the selected project"}
	result.ProposedLocal = []string{"record the state field and enum option GIDs in local configuration"}
	stateResult, err := s.ProvisionStates(ctx, StateProvisionOptions{DryRun: !opts.Apply, Apply: opts.Apply})
	if err != nil {
		return result, err
	}
	result.StateProvision = stateResult
	result.Applied = stateResult.Applied
	if opts.Mode == "native-types" {
		result.Problems = []Problem{{Code: "UNSUPPORTED_PROVISIONING", Message: "Native Asana custom-type creation or association is not safely provisioned by Dharana.", NextSteps: []string{"Associate compatible native types in Asana, then run dharana workflow bind --mode native-types --json."}}}
		result.Remediation = result.Problems[0].NextSteps
		result.Partial = result.Applied
		return result, nil
	}
	result.ProposedRemote = append(result.ProposedRemote, "create or reuse Dharana Work Type enum field", "ensure Epic, Story, Bug, and Spike options are enabled", "create or reuse optional Priority and Component enum fields", "attach created fields to the selected project")
	result.ProposedLocal = append(result.ProposedLocal, "record returned work-type and optional field GIDs in local configuration", "run deep diagnostics after provisioning")
	if !opts.Apply {
		return result, nil
	}
	result.Problems = []Problem{{Code: "UNSUPPORTED_PROVISIONING", Message: "Automatic custom-field provisioning is not enabled until the Asana account/API capabilities are verified.", NextSteps: []string{"Create compatible fields in Asana or run project adopt against a prepared project.", "Use config set-task-types and config set-fields with returned GIDs."}}}
	result.Remediation = result.Problems[0].NextSteps
	result.Partial = result.Applied
	return result, nil
}

func (s *Service) ListMembers(ctx context.Context) (*MemberListResult, error) {
	resolved, cfg, value, err := s.activeProject(ctx)
	if err != nil {
		return nil, err
	}
	members, err := s.asana().ProjectMemberships(ctx, resolved.Token, value.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list project members.")
	}
	_ = cfg
	return &MemberListResult{Project: value, Members: memberValues(members)}, nil
}

func (s *Service) AddMember(ctx context.Context, opts MemberOptions) (*MemberMutationResult, error) {
	return s.memberMutation(ctx, opts, true)
}

func (s *Service) RemoveMember(ctx context.Context, opts MemberOptions) (*MemberMutationResult, error) {
	return s.memberMutation(ctx, opts, false)
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

func (s *Service) resolveProject(ctx context.Context, token string, ref string, workspaceGID string) (*asana.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		cfg, err := s.config().Load()
		if err != nil {
			return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
		}
		if cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
			return nil, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured.")
		}
		ref = cfg.ActiveProject.GID
	}
	if looksLikeGID(ref) {
		projectValue, err := s.asana().Project(ctx, token, ref)
		if err == nil {
			return projectValue, nil
		}
		var apiErr *asana.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return nil, mapAsanaError(err, "Could not read Asana project.")
		}
	}
	projects, err := s.asana().Projects(ctx, token, workspaceGID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list Asana projects.")
	}
	matches := exactNameMatches(projects, ref)
	if len(matches) == 0 {
		return nil, output.NewError("PROJECT_NOT_FOUND", "No Asana project matched the supplied reference.")
	}
	if len(matches) > 1 {
		candidates := make([]ProjectValue, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, toProjectValue(match))
		}
		return nil, output.NewErrorWithCandidates("AMBIGUOUS_PROJECT", "Multiple Asana projects matched the supplied exact name.", candidates)
	}
	return &matches[0], nil
}

func (s *Service) inspectFieldsBestEffort(ctx context.Context, token string, projectGID string) []asana.CustomFieldSetting {
	fields, err := s.asana().CustomFieldSettingsForProject(ctx, token, projectGID)
	if err != nil {
		return nil
	}
	return fields
}

func buildInspectResult(projectValue ProjectValue, cfg *config.File, settings []asana.CustomFieldSetting, memberships []asana.ProjectMembership) *InspectResult {
	fields := fieldValues(settings, cfg)
	mappings := mappingStatus(cfg, settings)
	result := &InspectResult{
		Project:       projectValue,
		Fields:        fields,
		Mappings:      mappings,
		Members:       memberValues(memberships),
		Ready:         true,
		Repairability: []RepairCapability{{Code: "MISSING_TASK_TYPES", Automatic: true, Command: "dharana project adopt " + projectValue.GID + " --apply --json"}},
	}
	if cfg == nil || cfg.TaskTypes.Epic == "" || cfg.TaskTypes.Story == "" || cfg.TaskTypes.Bug == "" || cfg.TaskTypes.Spike == "" {
		result.Ready = false
		result.Problems = append(result.Problems, Problem{Code: "TASK_TYPES_NOT_CONFIGURED", Message: "Epic, Story, Bug, and Spike mappings are not fully configured.", NextSteps: []string{"dharana project adopt " + projectValue.GID + " --apply --json", "dharana workflow provision --mode custom-fields --dry-run --json"}})
	}
	if cfg == nil || !cfg.States.Complete() {
		result.Ready = false
		result.Problems = append(result.Problems, Problem{Code: "WORK_STATES_NOT_CONFIGURED", Message: "Canonical Backlog, Selected, In Progress, Verification, Done, Deferred, and Canceled mappings are not fully configured.", NextSteps: []string{"dharana workflow states provision --dry-run --json", "dharana workflow states bind --json"}})
	}
	if cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID != projectValue.GID {
		result.Ready = false
		result.Problems = append(result.Problems, Problem{Code: "PROJECT_CONTEXT_NOT_SELECTED", Message: "This project is not the selected effective project context.", NextSteps: []string{"dharana project adopt " + projectValue.GID + " --apply --json"}})
	}
	sort.SliceStable(result.Fields, func(i, j int) bool { return result.Fields[i].Name < result.Fields[j].Name })
	sort.SliceStable(result.Members, func(i, j int) bool { return result.Members[i].Name < result.Members[j].Name })
	return result
}

func fieldValues(settings []asana.CustomFieldSetting, cfg *config.File) []FieldValue {
	values := make([]FieldValue, 0, len(settings))
	for _, setting := range settings {
		field := setting.CustomField
		value := FieldValue{GID: field.GID, Name: field.Name, Type: field.Type, DetectedAs: detectedFieldNames(field, cfg)}
		for _, option := range field.EnumOptions {
			value.EnumOptions = append(value.EnumOptions, EnumOptionValue{GID: option.GID, Name: option.Name, Enabled: option.Enabled, DetectedAs: detectedOptionName(option, cfg)})
		}
		sort.SliceStable(value.EnumOptions, func(i, j int) bool { return value.EnumOptions[i].Name < value.EnumOptions[j].Name })
		values = append(values, value)
	}
	return values
}

func mappingStatus(cfg *config.File, settings []asana.CustomFieldSetting) MappingStatus {
	status := MappingStatus{WorkflowMode: "custom-fields"}
	if cfg == nil {
		cfg = &config.File{}
	}
	status.TaskTypes = []DetectedValue{
		detected("epic", cfg.TaskTypes.Epic, cfg.TaskTypes.FieldGID, optionSource(cfg.TaskTypes.Epic, settings)),
		detected("story", cfg.TaskTypes.Story, cfg.TaskTypes.FieldGID, optionSource(cfg.TaskTypes.Story, settings)),
		detected("bug", cfg.TaskTypes.Bug, cfg.TaskTypes.FieldGID, optionSource(cfg.TaskTypes.Bug, settings)),
		detected("spike", cfg.TaskTypes.Spike, cfg.TaskTypes.FieldGID, optionSource(cfg.TaskTypes.Spike, settings)),
	}
	status.Fields = []DetectedValue{
		detected("work_type", cfg.TaskTypes.FieldGID, cfg.TaskTypes.FieldGID, fieldSource(cfg.TaskTypes.FieldGID, settings)),
		detected("priority", cfg.Fields.PriorityGID, cfg.Fields.PriorityGID, fieldSource(cfg.Fields.PriorityGID, settings)),
		detected("component", cfg.Fields.ComponentGID, cfg.Fields.ComponentGID, fieldSource(cfg.Fields.ComponentGID, settings)),
		detected("state", cfg.States.FieldGID, cfg.States.FieldGID, fieldSource(cfg.States.FieldGID, settings)),
	}
	return status
}

func detected(name, configured, gid, source string) DetectedValue {
	status := "missing"
	if configured != "" {
		status = "configured"
	}
	if source != "" {
		status = "verified"
	}
	return DetectedValue{Name: name, Configured: configured, GID: gid, Source: source, Status: status}
}

func detectedFieldNames(field asana.CustomField, cfg *config.File) []string {
	if cfg == nil {
		return nil
	}
	var out []string
	if field.GID == cfg.TaskTypes.FieldGID {
		out = append(out, "work_type")
	}
	if field.GID == cfg.Fields.PriorityGID {
		out = append(out, "priority")
	}
	if field.GID == cfg.Fields.ComponentGID {
		out = append(out, "component")
	}
	if field.GID == cfg.States.FieldGID {
		out = append(out, "state")
	}
	return out
}

func detectedOptionName(option asana.EnumOption, cfg *config.File) string {
	if cfg == nil {
		return ""
	}
	switch {
	case option.GID == cfg.States.Backlog:
		return "state.backlog"
	case option.GID == cfg.States.Selected:
		return "state.selected"
	case option.GID == cfg.States.InProgress:
		return "state.in_progress"
	case option.GID == cfg.States.Verification:
		return "state.verification"
	case option.GID == cfg.States.Done:
		return "state.done"
	case option.GID == cfg.States.Deferred:
		return "state.deferred"
	case option.GID == cfg.States.Canceled:
		return "state.canceled"
	case option.GID == cfg.TaskTypes.Epic || option.Name == cfg.TaskTypes.Epic:
		return "epic"
	case option.GID == cfg.TaskTypes.Story || option.Name == cfg.TaskTypes.Story:
		return "story"
	case option.GID == cfg.TaskTypes.Bug || option.Name == cfg.TaskTypes.Bug:
		return "bug"
	case option.GID == cfg.TaskTypes.Spike || option.Name == cfg.TaskTypes.Spike:
		return "spike"
	default:
		return ""
	}
}

func fieldSource(gid string, settings []asana.CustomFieldSetting) string {
	if gid == "" {
		return ""
	}
	for _, setting := range settings {
		if setting.CustomField.GID == gid {
			return "attached_custom_field"
		}
	}
	return ""
}

func optionSource(value string, settings []asana.CustomFieldSetting) string {
	if value == "" {
		return ""
	}
	for _, setting := range settings {
		for _, option := range setting.CustomField.EnumOptions {
			if option.GID == value || option.Name == value {
				return "enum_option"
			}
		}
	}
	return ""
}

func discoverDefaultMappings(cfg *config.File, settings []asana.CustomFieldSetting) {
	if cfg == nil {
		return
	}
	for _, setting := range settings {
		field := setting.CustomField
		if strings.EqualFold(field.Name, "Work Type") || strings.EqualFold(field.Name, "Dharana Work Type") || strings.EqualFold(field.Name, "Type") {
			cfg.TaskTypes.FieldGID = field.GID
			for _, option := range field.EnumOptions {
				switch strings.ToLower(option.Name) {
				case "epic":
					cfg.TaskTypes.Epic = option.GID
				case "story":
					cfg.TaskTypes.Story = option.GID
				case "bug":
					cfg.TaskTypes.Bug = option.GID
				case "spike":
					cfg.TaskTypes.Spike = option.GID
				}
			}
		}
		if strings.EqualFold(field.Name, "Priority") {
			cfg.Fields.PriorityGID = field.GID
		}
		if strings.EqualFold(field.Name, "Component") {
			cfg.Fields.ComponentGID = field.GID
		}
	}
	if !cfg.States.Complete() {
		for index := range settings {
			if strings.EqualFold(strings.TrimSpace(settings[index].CustomField.Name), stateFieldName) {
				if mapping, problems := mappingsForField(&settings[index].CustomField); len(problems) == 0 {
					cfg.States = mapping
				}
				break
			}
		}
	}
	if cfg.TaskTypes.Epic == "" {
		cfg.TaskTypes.Epic = "Epic"
	}
	if cfg.TaskTypes.Story == "" {
		cfg.TaskTypes.Story = "Story"
	}
	if cfg.TaskTypes.Bug == "" {
		cfg.TaskTypes.Bug = "Bug"
	}
	if cfg.TaskTypes.Spike == "" {
		cfg.TaskTypes.Spike = "Spike"
	}
}

func (s *Service) activeProject(ctx context.Context) (*auth.ResolvedToken, *config.File, ProjectValue, error) {
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, nil, ProjectValue{}, err
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, nil, ProjectValue{}, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, nil, ProjectValue{}, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured.")
	}
	projectValue, err := s.asana().Project(ctx, resolved.Token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, nil, ProjectValue{}, mapAsanaError(err, "Could not read active project.")
	}
	return resolved, cfg, toProjectValue(*projectValue), nil
}

func (s *Service) memberMutation(ctx context.Context, opts MemberOptions, add bool) (*MemberMutationResult, error) {
	opts.User = strings.TrimSpace(opts.User)
	if opts.User == "" {
		return nil, output.NewError("USER_REQUIRED", "Provide --user with an Asana user GID or exact email.")
	}
	resolved, _, projectValue, err := s.activeProject(ctx)
	if err != nil {
		return nil, err
	}
	members, err := s.asana().ProjectMemberships(ctx, resolved.Token, projectValue.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list project members.")
	}
	user, err := s.resolveUser(ctx, resolved.Token, projectValue.WorkspaceGID, opts.User)
	if err != nil {
		return nil, err
	}
	isMember := memberContains(members, user.GID)
	result := &MemberMutationResult{Project: projectValue, User: toMemberValue(*user), DryRun: opts.DryRun, Found: isMember, SideEffects: []string{"Asana may notify project members or update project access metadata."}}
	if add {
		if isMember {
			result.IdempotentExisting = true
			return result, nil
		}
		if opts.DryRun {
			return result, nil
		}
		if err := s.asana().AddProjectMembers(ctx, resolved.Token, projectValue.GID, []string{user.GID}); err != nil {
			return nil, mapAsanaError(err, "Could not add project member.")
		}
		result.Added = true
		result.Found = true
		return result, nil
	}
	if !isMember {
		return result, nil
	}
	if opts.DryRun {
		return result, nil
	}
	if err := s.asana().RemoveProjectMembers(ctx, resolved.Token, projectValue.GID, []string{user.GID}); err != nil {
		return nil, mapAsanaError(err, "Could not remove project member.")
	}
	result.Removed = true
	return result, nil
}

func (s *Service) resolveUser(ctx context.Context, token, workspaceGID, ref string) (*asana.User, error) {
	if looksLikeGID(ref) {
		user, err := s.asana().User(ctx, token, ref)
		if err != nil {
			return nil, mapAsanaError(err, "Could not read Asana user.")
		}
		return user, nil
	}
	users, err := s.asana().Users(ctx, token, workspaceGID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list workspace users.")
	}
	var matches []asana.User
	for _, user := range users {
		if strings.EqualFold(user.Email, ref) {
			matches = append(matches, user)
		}
	}
	if len(matches) == 0 {
		return nil, output.NewError("USER_NOT_FOUND", "No accessible Asana user matched the supplied email.")
	}
	if len(matches) > 1 {
		candidates := make([]MemberValue, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, toMemberValue(match))
		}
		return nil, output.NewErrorWithCandidates("AMBIGUOUS_USER", "Multiple Asana users matched the supplied identity.", candidates)
	}
	return &matches[0], nil
}

func memberValues(members []asana.ProjectMembership) []MemberValue {
	values := make([]MemberValue, 0, len(members))
	for _, member := range members {
		values = append(values, toMemberValue(member.User))
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}

func memberContains(members []asana.ProjectMembership, userGID string) bool {
	for _, member := range members {
		if member.User.GID == userGID {
			return true
		}
	}
	return false
}

func toMemberValue(user asana.User) MemberValue {
	return MemberValue{GID: user.GID, Name: user.Name, Email: user.Email}
}

func safeContextName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, " ", "-")
	if name == "" {
		return "default"
	}
	return name
}

func looksLikeGID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
	value := ProjectValue{
		GID:           p.GID,
		Name:          p.Name,
		WorkspaceGID:  p.Workspace.GID,
		WorkspaceName: p.Workspace.Name,
		Permalink:     p.Permalink,
	}
	if p.Team != nil {
		value.TeamGID = p.Team.GID
		value.TeamName = p.Team.Name
	}
	return value
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
