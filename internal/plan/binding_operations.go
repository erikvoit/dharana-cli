package plan

import (
	"context"

	"github.com/erikvoit/dharana-cli/internal/output"
)

type BindingsResult struct {
	ManifestID       string            `json:"manifest_id"`
	ProjectGID       string            `json:"project_gid"`
	Bindings         []Binding         `json:"bindings"`
	OperationRecords []OperationRecord `json:"operation_records,omitempty"`
	BindingsPath     string            `json:"bindings_path"`
}

type BindingChangeResult struct {
	ManifestID   string   `json:"manifest_id"`
	LogicalID    string   `json:"logical_id"`
	Before       *Binding `json:"before,omitempty"`
	After        *Binding `json:"after,omitempty"`
	DryRun       bool     `json:"dry_run"`
	Applied      bool     `json:"applied"`
	BindingsPath string   `json:"bindings_path"`
}

func (s *Service) InspectBindings(manifest *Manifest) (*BindingsResult, error) {
	validation := ValidateLocal(manifest)
	if !validation.Valid {
		return nil, output.NewErrorWithDetails("PLAN_INVALID", "Plan validation failed; bindings cannot be inspected safely.", validation)
	}
	cfg, err := s.config().Load()
	if err != nil || cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, output.NewError("PROJECT_NOT_CONFIGURED", "No effective project context is configured.")
	}
	store := s.bindingStore()
	state, err := store.Load(manifest.Metadata.ID, cfg.ActiveProject.GID, cfg.ActiveProject.WorkspaceGID)
	if err != nil {
		return nil, output.NewErrorWithDetails("BINDING_READ_FAILED", "Could not read durable plan bindings.", err.Error())
	}
	return &BindingsResult{ManifestID: manifest.Metadata.ID, ProjectGID: cfg.ActiveProject.GID, Bindings: state.SortedBindings(), OperationRecords: state.SortedOperationRecords(), BindingsPath: store.path(manifest.Metadata.ID, cfg.ActiveProject.GID)}, nil
}

func (s *Service) Unbind(manifest *Manifest, logicalID string, apply bool) (*BindingChangeResult, error) {
	result, state, store, err := s.bindingChangeState(manifest, logicalID)
	if err != nil {
		return nil, err
	}
	binding, ok := state.Objects[logicalID]
	if !ok {
		return nil, output.NewError("BINDING_NOT_FOUND", "No binding exists for the requested logical ID.")
	}
	result.Before = &binding
	result.DryRun = !apply
	if apply {
		release, lockErr := store.Acquire(state.ManifestID, state.ProjectGID)
		if lockErr != nil {
			return result, output.NewErrorWithDetails("BINDING_LOCK_FAILED", "Could not acquire the project-scoped plan binding lock.", lockErr.Error())
		}
		defer release()
		result, state, store, err = s.bindingChangeState(manifest, logicalID)
		if err != nil {
			return nil, err
		}
		binding, ok = state.Objects[logicalID]
		if !ok {
			return nil, output.NewError("BINDING_NOT_FOUND", "No binding exists for the requested logical ID.")
		}
		result.Before = &binding
		delete(state.Objects, logicalID)
		state.ManifestDigest = ""
		if err := store.Save(state); err != nil {
			return nil, output.NewError("BINDING_WRITE_FAILED", "Could not remove the durable plan binding.")
		}
		result.Applied = true
	}
	return result, nil
}

func (s *Service) ReplaceBinding(ctx context.Context, manifest *Manifest, logicalID, gid string, apply bool) (*BindingChangeResult, error) {
	result, state, store, err := s.bindingChangeState(manifest, logicalID)
	if err != nil {
		return nil, err
	}
	nodes := map[string]Node{}
	for _, node := range manifest.Nodes() {
		nodes[node.ID] = node
	}
	node, ok := nodes[logicalID]
	if !ok {
		return nil, output.NewError("PLAN_LOGICAL_ID_NOT_FOUND", "The requested logical ID is not present in the manifest.")
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	remote, ok := snapshot.Objects[gid]
	if !ok {
		return nil, output.NewError("BINDING_TARGET_NOT_FOUND", "The requested Asana GID is not present in the selected project graph.")
	}
	if remote.Type != node.Type {
		return nil, output.NewErrorWithDetails("BINDING_TYPE_MISMATCH", "The requested Asana object type does not match the manifest node.", map[string]string{"logical_id": logicalID, "expected_type": node.Type, "observed_type": remote.Type})
	}
	if before, ok := state.Objects[logicalID]; ok {
		copyValue := before
		result.Before = &copyValue
	}
	after := bindingForExisting(node, remote)
	after.LogicalPath = state.logicalPath(node)
	result.After = &after
	result.DryRun = !apply
	if apply {
		release, lockErr := store.Acquire(state.ManifestID, state.ProjectGID)
		if lockErr != nil {
			return result, output.NewErrorWithDetails("BINDING_LOCK_FAILED", "Could not acquire the project-scoped plan binding lock.", lockErr.Error())
		}
		defer release()
		result, state, store, err = s.bindingChangeState(manifest, logicalID)
		if err != nil {
			return nil, err
		}
		if before, exists := state.Objects[logicalID]; exists {
			copyValue := before
			result.Before = &copyValue
		}
		result.After = &after
		state.Objects[logicalID] = after
		state.ManifestDigest = ""
		if err := store.Save(state); err != nil {
			return nil, output.NewError("BINDING_WRITE_FAILED", "Could not replace the durable plan binding.")
		}
		result.Applied = true
	}
	return result, nil
}

func (s *Service) bindingChangeState(manifest *Manifest, logicalID string) (*BindingChangeResult, *BindingState, *BindingStore, error) {
	validation := ValidateLocal(manifest)
	if !validation.Valid {
		return nil, nil, nil, output.NewErrorWithDetails("PLAN_INVALID", "Plan validation failed; bindings cannot be changed safely.", validation)
	}
	cfg, err := s.config().Load()
	if err != nil || cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, nil, nil, output.NewError("PROJECT_NOT_CONFIGURED", "No effective project context is configured.")
	}
	store := s.bindingStore()
	state, err := store.Load(manifest.Metadata.ID, cfg.ActiveProject.GID, cfg.ActiveProject.WorkspaceGID)
	if err != nil {
		return nil, nil, nil, output.NewErrorWithDetails("BINDING_READ_FAILED", "Could not read durable plan bindings.", err.Error())
	}
	result := &BindingChangeResult{ManifestID: manifest.Metadata.ID, LogicalID: logicalID, BindingsPath: store.path(manifest.Metadata.ID, cfg.ActiveProject.GID)}
	return result, state, store, nil
}
