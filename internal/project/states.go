package project

import (
	"context"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/workflowstate"
)

const stateFieldName = "Dharana State"

type StateValue struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	OptionGID     string   `json:"option_gid,omitempty"`
	OptionName    string   `json:"option_name,omitempty"`
	Configured    bool     `json:"configured"`
	AllowedNext   []string `json:"allowed_next"`
	Terminal      bool     `json:"terminal"`
	ReadyEligible bool     `json:"ready_eligible"`
}

type StateInspectResult struct {
	Project    ProjectValue `json:"project"`
	FieldGID   string       `json:"field_gid,omitempty"`
	FieldName  string       `json:"field_name,omitempty"`
	Configured bool         `json:"configured"`
	Attached   bool         `json:"attached"`
	States     []StateValue `json:"states"`
	Problems   []Problem    `json:"problems,omitempty"`
}

type StateProvisionOptions struct {
	DryRun bool
	Apply  bool
}

type StateProvisionResult struct {
	Project        ProjectValue       `json:"project"`
	DryRun         bool               `json:"dry_run"`
	Applied        bool               `json:"applied"`
	CreatedField   bool               `json:"created_field,omitempty"`
	AttachedField  bool               `json:"attached_field,omitempty"`
	CreatedOptions []string           `json:"created_options,omitempty"`
	ProposedRemote []string           `json:"proposed_remote_mutations,omitempty"`
	ProposedLocal  []string           `json:"proposed_local_mutations,omitempty"`
	Inspection     StateInspectResult `json:"inspection"`
}

type StateBindOptions struct {
	FieldGID string
}

func (s *Service) InspectStates(ctx context.Context) (*StateInspectResult, error) {
	resolved, cfg, projectValue, err := s.activeProject(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := s.asana().CustomFieldSettingsForProject(ctx, resolved.Token, projectValue.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not inspect project state fields.")
	}
	result := inspectStateMappings(projectValue, cfg.States, settings)
	return &result, nil
}

func (s *Service) BindStates(ctx context.Context, opts StateBindOptions) (*StateInspectResult, error) {
	resolved, cfg, projectValue, err := s.activeProject(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := s.asana().CustomFieldSettingsForProject(ctx, resolved.Token, projectValue.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not inspect project state fields.")
	}
	field, err := selectStateField(settings, strings.TrimSpace(opts.FieldGID))
	if err != nil {
		return nil, err
	}
	mapping, problems := mappingsForField(field)
	if len(problems) > 0 {
		return nil, output.NewErrorWithDetails("STATE_MAPPING_INCOMPLETE", "The selected state field does not provide every canonical Dharana state exactly once.", problems)
	}
	cfg.States = mapping
	if err := s.config().Save(cfg); err != nil {
		return nil, output.NewError("CONFIG_WRITE_FAILED", "Could not save workflow state mappings.")
	}
	result := inspectStateMappings(projectValue, cfg.States, settings)
	return &result, nil
}

func (s *Service) ProvisionStates(ctx context.Context, opts StateProvisionOptions) (*StateProvisionResult, error) {
	if opts.Apply && opts.DryRun {
		return nil, output.NewError("STATE_PROVISION_MODE_CONFLICT", "Use only one of --dry-run or --apply.")
	}
	if !opts.Apply {
		opts.DryRun = true
	}
	resolved, cfg, projectValue, err := s.activeProject(ctx)
	if err != nil {
		return nil, err
	}
	result := &StateProvisionResult{
		Project: projectValue, DryRun: opts.DryRun,
		ProposedRemote: []string{"create or reuse the workspace enum field " + stateFieldName, "ensure every canonical state option exists", "attach the state field to the selected project"},
		ProposedLocal:  []string{"bind the field and enum option GIDs to the selected Dharana context"},
	}
	settings, err := s.asana().CustomFieldSettingsForProject(ctx, resolved.Token, projectValue.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not inspect project state fields.")
	}
	if !opts.Apply {
		result.Inspection = inspectStateMappings(projectValue, cfg.States, settings)
		return result, nil
	}
	if s.Auth != nil {
		if err := s.Auth.RequireScopes(ctx, []string{"custom_fields:read", "custom_fields:write", "projects:read", "projects:write"}); err != nil {
			return nil, err
		}
	}
	workspaceFields, err := s.asana().WorkspaceCustomFields(ctx, resolved.Token, projectValue.WorkspaceGID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not inspect workspace custom fields.")
	}
	var field *asana.CustomField
	for index := range workspaceFields {
		if strings.EqualFold(strings.TrimSpace(workspaceFields[index].Name), stateFieldName) {
			if field != nil {
				return nil, output.NewError("AMBIGUOUS_STATE_FIELD", "Multiple workspace fields match Dharana State.")
			}
			field = &workspaceFields[index]
		}
	}
	if field == nil {
		names := make([]string, 0, len(workflowstate.Definitions()))
		for _, definition := range workflowstate.Definitions() {
			names = append(names, definition.DisplayName)
		}
		field, err = s.asana().CreateCustomField(ctx, resolved.Token, asana.CreateCustomFieldInput{Name: stateFieldName, Description: "Canonical delivery state managed by Dharana.", WorkspaceGID: projectValue.WorkspaceGID, EnumOptions: names})
		if err != nil {
			return nil, mapAsanaError(err, "Could not create the Dharana state field.")
		}
		result.CreatedField = true
		if len(field.EnumOptions) == 0 {
			refreshed, refreshErr := s.asana().WorkspaceCustomFields(ctx, resolved.Token, projectValue.WorkspaceGID)
			if refreshErr != nil {
				return nil, mapAsanaError(refreshErr, "The Dharana state field was created but its enum options could not be verified.")
			}
			for index := range refreshed {
				if refreshed[index].GID == field.GID {
					field = &refreshed[index]
					break
				}
			}
			if len(field.EnumOptions) == 0 {
				return nil, output.NewErrorWithDetails("STATE_PROVISION_VERIFY_FAILED", "The Dharana state field was created but Asana did not return its enum options.", map[string]string{"field_gid": field.GID, "recovery": "dharana workflow states inspect --json"})
			}
		}
	}
	if !isEnumField(*field) {
		return nil, output.NewError("STATE_FIELD_TYPE_INVALID", "Dharana State must be an enum custom field.")
	}
	for _, definition := range workflowstate.Definitions() {
		if enumOptionForState(*field, definition.Name) != nil {
			continue
		}
		option, createErr := s.asana().CreateEnumOption(ctx, resolved.Token, field.GID, definition.DisplayName)
		if createErr != nil {
			return nil, mapAsanaError(createErr, "Could not complete the Dharana state options.")
		}
		field.EnumOptions = append(field.EnumOptions, *option)
		result.CreatedOptions = append(result.CreatedOptions, definition.Name)
	}
	attached := false
	for _, setting := range settings {
		if setting.CustomField.GID == field.GID {
			attached = true
			break
		}
	}
	if !attached {
		if err := s.asana().AddCustomFieldToProject(ctx, resolved.Token, projectValue.GID, field.GID); err != nil {
			return nil, mapAsanaError(err, "Could not attach the Dharana state field to the project.")
		}
		result.AttachedField = true
		settings = append(settings, asana.CustomFieldSetting{CustomField: *field})
	}
	mapping, problems := mappingsForField(field)
	if len(problems) > 0 {
		return nil, output.NewErrorWithDetails("STATE_MAPPING_INCOMPLETE", "Provisioning did not produce every canonical state.", problems)
	}
	cfg.States = mapping
	if err := s.config().Save(cfg); err != nil {
		return nil, output.NewErrorWithDetails("STATE_PROVISION_PARTIAL", "The remote state field was provisioned but local mappings could not be saved.", map[string]string{"field_gid": field.GID, "recovery": "dharana workflow states bind --field-gid " + field.GID + " --json"})
	}
	result.Applied = true
	result.Inspection = inspectStateMappings(projectValue, cfg.States, settings)
	return result, nil
}

func inspectStateMappings(projectValue ProjectValue, mapping config.StateMappings, settings []asana.CustomFieldSetting) StateInspectResult {
	result := StateInspectResult{Project: projectValue, FieldGID: mapping.FieldGID, Configured: mapping.Complete()}
	var attachedField *asana.CustomField
	for index := range settings {
		if settings[index].CustomField.GID == mapping.FieldGID || (mapping.FieldGID == "" && strings.EqualFold(settings[index].CustomField.Name, stateFieldName)) {
			attachedField = &settings[index].CustomField
			break
		}
	}
	if attachedField != nil {
		result.Attached = true
		result.FieldGID = attachedField.GID
		result.FieldName = attachedField.Name
	}
	for _, definition := range workflowstate.Definitions() {
		value := StateValue{Name: definition.Name, DisplayName: definition.DisplayName, OptionGID: mapping.Option(definition.Name), AllowedNext: workflowstate.AllowedTransitions(definition.Name), Terminal: definition.Terminal, ReadyEligible: workflowstate.IsReady(definition.Name)}
		if attachedField != nil {
			if option := enumOptionForState(*attachedField, definition.Name); option != nil {
				value.OptionName = option.Name
				if value.OptionGID == "" {
					value.OptionGID = option.GID
				}
			}
		}
		value.Configured = value.OptionGID != ""
		result.States = append(result.States, value)
	}
	if mapping.FieldGID == "" {
		result.Problems = append(result.Problems, Problem{Code: "STATE_FIELD_NOT_CONFIGURED", Message: "No authoritative Dharana state field is configured.", NextSteps: []string{"dharana workflow states provision --dry-run --json"}})
	} else if attachedField == nil {
		result.Problems = append(result.Problems, Problem{Code: "STATE_FIELD_NOT_ATTACHED", Message: "The configured state field is not attached to the selected project.", NextSteps: []string{"dharana workflow states provision --apply --json"}})
	}
	if !mapping.Complete() {
		result.Problems = append(result.Problems, Problem{Code: "STATE_MAPPING_INCOMPLETE", Message: "One or more canonical state options are not bound.", NextSteps: []string{"dharana workflow states bind --json"}})
	}
	return result
}

func selectStateField(settings []asana.CustomFieldSetting, gid string) (*asana.CustomField, error) {
	var matches []*asana.CustomField
	for index := range settings {
		field := &settings[index].CustomField
		if (gid != "" && field.GID == gid) || (gid == "" && strings.EqualFold(strings.TrimSpace(field.Name), stateFieldName)) {
			matches = append(matches, field)
		}
	}
	if len(matches) == 0 {
		return nil, output.NewError("STATE_FIELD_NOT_FOUND", "No attached enum field matched the requested Dharana state field.")
	}
	if len(matches) > 1 {
		return nil, output.NewError("AMBIGUOUS_STATE_FIELD", "Multiple attached fields matched Dharana State; provide --field-gid.")
	}
	if !isEnumField(*matches[0]) {
		return nil, output.NewError("STATE_FIELD_TYPE_INVALID", "Dharana State must be an enum custom field.")
	}
	return matches[0], nil
}

func isEnumField(field asana.CustomField) bool {
	fieldType := strings.TrimSpace(field.ResourceSubtype)
	if fieldType == "" {
		fieldType = strings.TrimSpace(field.Type)
	}
	// Older test fixtures and older Asana responses may omit both subtype fields.
	return fieldType == "" || fieldType == "enum"
}

func mappingsForField(field *asana.CustomField) (config.StateMappings, []Problem) {
	mapping := config.StateMappings{FieldGID: field.GID}
	seen := map[string]bool{}
	var problems []Problem
	for _, option := range field.EnumOptions {
		state, ok := workflowstate.Normalize(option.Name)
		if !ok || !option.Enabled {
			continue
		}
		if seen[state] {
			problems = append(problems, Problem{Code: "STATE_OPTION_AMBIGUOUS", Message: "Multiple enabled options map to canonical state " + state + "."})
			continue
		}
		seen[state] = true
		setStateOption(&mapping, state, option.GID)
	}
	for _, state := range workflowstate.Names() {
		if mapping.Option(state) == "" {
			problems = append(problems, Problem{Code: "STATE_OPTION_MISSING", Message: "Missing canonical state option " + state + "."})
		}
	}
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Message < problems[j].Message })
	return mapping, problems
}

func setStateOption(mapping *config.StateMappings, state, gid string) {
	switch state {
	case workflowstate.Backlog:
		mapping.Backlog = gid
	case workflowstate.Selected:
		mapping.Selected = gid
	case workflowstate.InProgress:
		mapping.InProgress = gid
	case workflowstate.Verification:
		mapping.Verification = gid
	case workflowstate.Done:
		mapping.Done = gid
	case workflowstate.Deferred:
		mapping.Deferred = gid
	case workflowstate.Canceled:
		mapping.Canceled = gid
	}
}

func enumOptionForState(field asana.CustomField, state string) *asana.EnumOption {
	for index := range field.EnumOptions {
		candidate, ok := workflowstate.Normalize(field.EnumOptions[index].Name)
		if ok && candidate == state && field.EnumOptions[index].Enabled {
			return &field.EnumOptions[index]
		}
	}
	return nil
}
