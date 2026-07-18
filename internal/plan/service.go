package plan

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/work"
)

func NewService(workService WorkBackend, configStore ConfigStore) *Service {
	return &Service{Work: workService, Config: configStore, Bindings: &BindingStore{}}
}

func (s *Service) Validate(ctx context.Context, manifest *Manifest, remote bool) (*ValidationResult, error) {
	result := ValidateLocal(manifest)
	if !remote || !result.Valid {
		return &result, nil
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration for plan validation.")
	}
	result.RemoteFindings = s.validateTarget(manifest, cfg)
	if !hasErrors(result.RemoteFindings) {
		tree, err := s.work().WorkTree(ctx, work.WorkTreeOptions{})
		if err != nil {
			code, message := planError(err, "PLAN_PROJECT_ACCESS_FAILED", "The selected project could not be inspected remotely.")
			result.RemoteFindings = append(result.RemoteFindings, finding(code, "error", "$.metadata.context", message, "Verify authentication and access to the selected project."))
		} else if cfg.ActiveProject != nil && tree.Project.GID != "" && tree.Project.GID != cfg.ActiveProject.GID {
			result.RemoteFindings = append(result.RemoteFindings, finding("PLAN_PROJECT_MISMATCH", "error", "$.metadata.context", "The remote project snapshot does not match the effective plan project.", "Select the intended context and retry validation."))
		}
	}
	if !hasErrors(result.RemoteFindings) {
		seen := map[string]bool{}
		for _, node := range manifest.Nodes() {
			key := propertyValidationKey(node)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			_, err := s.work().ValidateProperties(ctx, work.ValidatePropertiesOptions{
				Assignee: node.Assignee, DueOn: node.DueOn, Priority: node.Priority, Component: node.Component,
			})
			if err != nil {
				code, message := planError(err, "PLAN_REMOTE_VALUE_INVALID", "A plan value could not be resolved in the selected project.")
				result.RemoteFindings = append(result.RemoteFindings, finding(code, "error", nodePath(manifest, node.ID), message, "Inspect the selected project's users and workflow fields, then update the manifest."))
			}
		}
	}
	sortFindings(result.RemoteFindings)
	result.Valid = result.Valid && !hasErrors(result.RemoteFindings)
	return &result, nil
}

func (s *Service) Snapshot(ctx context.Context) (*Snapshot, error) {
	tree, err := s.work().WorkTree(ctx, work.WorkTreeOptions{})
	if err != nil {
		return nil, err
	}
	snapshot := &Snapshot{Project: tree.Project, Objects: map[string]RemoteObject{}, Issues: tree.Issues}
	var flatten func([]work.TreeNode) error
	flatten = func(nodes []work.TreeNode) error {
		for _, node := range nodes {
			detail, err := s.work().GetWork(ctx, node.Item.GID)
			if err != nil {
				return err
			}
			properties, err := s.work().UpdateWork(ctx, work.UpdateWorkOptions{Ref: node.Item.GID, DryRun: true})
			if err != nil {
				return err
			}
			remote := RemoteObject{
				GID: node.Item.GID, Ref: node.Item.Ref, Type: node.Item.Type, Name: node.Item.Name,
				Completed: node.Item.Status == "completed", Properties: properties.Before,
			}
			if node.Item.Parent != nil {
				remote.ParentGID = node.Item.Parent.GID
			}
			for _, blocker := range detail.Item.Dependencies.Blockers {
				remote.Dependencies = append(remote.Dependencies, blocker.GID)
			}
			sort.Strings(remote.Dependencies)
			snapshot.Objects[remote.GID] = remote
			if err := flatten(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := flatten(tree.Epics); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *Service) config() ConfigStore {
	if s.Config != nil {
		return s.Config
	}
	return config.NewStore()
}

func (s *Service) work() WorkBackend {
	return s.Work
}

func (s *Service) bindingStore() *BindingStore {
	if s.Bindings != nil {
		return s.Bindings
	}
	return &BindingStore{}
}

func (s *Service) validateTarget(manifest *Manifest, cfg *config.File) []Finding {
	var findings []Finding
	if cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return []Finding{finding("PROJECT_NOT_CONFIGURED", "error", "$.metadata.context", "No effective project context is configured.", "Select or create the manifest context before running remote plan operations.")}
	}
	if manifest.Spec.Project != "" && manifest.Spec.Project != cfg.ActiveProject.GID && manifest.Spec.Project != cfg.ActiveProject.Name {
		findings = append(findings, finding("PLAN_PROJECT_MISMATCH", "error", "$.spec.project", "The manifest project does not match the effective project.", "Select the intended context or update spec.project."))
	}
	if manifest.Metadata.Context != "" {
		contextValue, ok := cfg.ContextByName(manifest.Metadata.Context)
		if !ok && cfg.ActiveContext != manifest.Metadata.Context {
			findings = append(findings, finding("PLAN_CONTEXT_NOT_FOUND", "error", "$.metadata.context", "The named plan context is not configured.", "Create the context before running remote plan operations."))
		} else if ok && contextValue.Project.GID != cfg.ActiveProject.GID {
			findings = append(findings, finding("PLAN_CONTEXT_NOT_ACTIVE", "error", "$.metadata.context", "The named context does not match the effective project used by this command.", "Run dharana context use "+manifest.Metadata.Context+" or use an explicit matching project override."))
		}
	}
	missingTypes := []string{}
	if cfg.TaskTypes.Epic == "" {
		missingTypes = append(missingTypes, "epic")
	}
	if cfg.TaskTypes.Story == "" {
		missingTypes = append(missingTypes, "story")
	}
	if cfg.TaskTypes.Bug == "" {
		missingTypes = append(missingTypes, "bug")
	}
	if cfg.TaskTypes.Spike == "" {
		missingTypes = append(missingTypes, "spike")
	}
	if len(missingTypes) > 0 {
		findings = append(findings, finding("TASK_TYPES_NOT_CONFIGURED", "error", "$.spec.work", "Required work type mappings are missing: "+strings.Join(missingTypes, ", ")+".", "Run project adopt, workflow bind, or workflow provision for the selected project."))
	}
	for _, node := range manifest.Nodes() {
		path := nodePath(manifest, node.ID)
		if node.Priority != nil && cfg.Fields.PriorityGID == "" {
			findings = append(findings, finding("PRIORITY_FIELD_NOT_CONFIGURED", "error", path+".priority", "The manifest manages priority but the selected project has no Priority field mapping.", "Configure or provision a Priority field before applying the plan."))
		}
		if node.Component != nil && cfg.Fields.ComponentGID == "" {
			findings = append(findings, finding("COMPONENT_FIELD_NOT_CONFIGURED", "error", path+".component", "The manifest manages component but the selected project has no Component field mapping.", "Configure or provision a Component field before applying the plan."))
		}
	}
	return findings
}

func propertyValidationKey(node Node) string {
	values := []string{pointerValue(node.Assignee), pointerValue(node.DueOn), pointerValue(node.Priority), pointerValue(node.Component)}
	if strings.Join(values, "") == "" {
		return ""
	}
	return strings.Join(values, "\x00")
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func planError(err error, fallbackCode, fallbackMessage string) (string, string) {
	var appErr *output.AppError
	if errors.As(err, &appErr) {
		return appErr.Code, appErr.Message
	}
	return fallbackCode, fallbackMessage
}
