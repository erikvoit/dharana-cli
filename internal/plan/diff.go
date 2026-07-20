package plan

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/richtext"
)

func (s *Service) Diff(ctx context.Context, manifest *Manifest) (*DiffResult, error) {
	validation, err := s.Validate(ctx, manifest, true)
	if err != nil {
		return nil, err
	}
	result := &DiffResult{
		Validation: *validation,
		Operations: []Operation{},
	}
	if manifest != nil {
		result.ManifestID = manifest.Metadata.ID
		result.ManifestDigest = manifest.Digest()
	}
	if !validation.Valid {
		return result, nil
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	result.Project = snapshot.Project
	store := s.bindingStore()
	bindings, err := store.Load(manifest.Metadata.ID, snapshot.Project.GID, snapshot.Project.WorkspaceGID)
	if err != nil {
		return nil, output.NewErrorWithDetails("BINDING_READ_FAILED", "Could not read durable plan bindings.", err.Error())
	}
	bindings.Context = manifest.Metadata.Context
	result.BindingsPath = store.path(manifest.Metadata.ID, snapshot.Project.GID)

	nodes := manifest.Nodes()
	nodeByID := map[string]Node{}
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	resolved := map[string]*RemoteObject{}
	usedRemote := map[string]bool{}
	var operations []Operation

	for _, node := range nodes {
		var remote *RemoteObject
		binding, bound := bindings.Objects[node.ID]
		if bound {
			if value, ok := snapshot.Objects[binding.GID]; ok {
				copyValue := value
				remote = &copyValue
				if value.Type != node.Type {
					operations = append(operations, conflictOperation(node, binding.GID, "Bound object type does not match the manifest type.", map[string]any{"type": value.Type}, map[string]any{"type": node.Type}))
				}
			} else {
				operations = append(operations, conflictOperation(node, binding.GID, "Bound Asana object is missing or inaccessible; automatic rebinding is unsafe.", map[string]any{"binding_gid": binding.GID}, nil))
			}
		} else {
			parentGID := ""
			if node.ParentID != "" && resolved[node.ParentID] != nil {
				parentGID = resolved[node.ParentID].GID
			}
			candidates := exactRemoteCandidates(snapshot.Objects, node, parentGID)
			switch len(candidates) {
			case 0:
				operations = append(operations, createOperation(node))
			case 1:
				copyValue := candidates[0]
				remote = &copyValue
				operations = append(operations, Operation{
					Kind: "bind", LogicalID: node.ID, GID: remote.GID,
					Reason:  "An unambiguous exact-match object can be adopted.",
					Current: remoteMap(*remote), Desired: desiredMap(node),
				})
			default:
				candidateValues := make([]map[string]any, 0, len(candidates))
				for _, candidate := range candidates {
					candidateValues = append(candidateValues, remoteMap(candidate))
				}
				operations = append(operations, conflictOperation(node, "", "Multiple exact-match objects make adoption ambiguous.", map[string]any{"candidates": candidateValues}, desiredMap(node)))
			}
		}
		if remote != nil {
			resolved[node.ID] = remote
			usedRemote[remote.GID] = true
			if remote.Type == node.Type {
				operations = append(operations, diffExistingNode(node, *remote, binding, bound, resolved)...)
			}
		}
	}

	operations = append(operations, diffDependencies(nodes, resolved, bindings)...)
	operations = append(operations, diffRemovedManagedObjects(manifest, bindings, snapshot, nodeByID)...)
	result.Unmanaged = unmanagedObjects(snapshot, usedRemote)
	sortOperations(operations)
	for i := range operations {
		operations[i].ID = fmt.Sprintf("op-%04d-%s-%s", i+1, operations[i].Kind, operations[i].LogicalID)
		if operations[i].Conflict {
			result.Conflicted = true
		}
	}
	result.Operations = operations
	changed := map[string]bool{}
	for _, operation := range operations {
		changed[operation.LogicalID] = true
	}
	for _, node := range nodes {
		if !changed[node.ID] {
			result.NoopLogicalIDs = append(result.NoopLogicalIDs, node.ID)
		}
	}
	sort.Strings(result.NoopLogicalIDs)
	result.Converged = len(operations) == 0
	return result, nil
}

func exactRemoteCandidates(objects map[string]RemoteObject, node Node, parentGID string) []RemoteObject {
	var values []RemoteObject
	for _, value := range objects {
		if value.Type != node.Type || value.Name != node.Name {
			continue
		}
		if node.ParentID == "" {
			if value.ParentGID != "" {
				continue
			}
		} else {
			if parentGID == "" || value.ParentGID != parentGID {
				continue
			}
		}
		values = append(values, value)
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].GID < values[j].GID })
	return values
}

func createOperation(node Node) Operation {
	prerequisites := []string{}
	if node.ParentID != "" {
		prerequisites = append(prerequisites, "create-or-bind:"+node.ParentID)
	}
	return Operation{
		Kind: "create", LogicalID: node.ID, Reason: "No bound or exact-match remote object exists.",
		Desired: desiredMap(node), Prerequisites: prerequisites,
	}
}

func conflictOperation(node Node, gid, reason string, current, desired map[string]any) Operation {
	return Operation{Kind: "conflict", LogicalID: node.ID, GID: gid, Reason: reason, Current: current, Desired: desired, Conflict: true, ResolutionOptions: []string{"restore remote state from the manifest", "update the manifest to accept remote state", "explicitly replace or remove the binding"}}
}

func diffExistingNode(node Node, remote RemoteObject, binding Binding, bound bool, resolved map[string]*RemoteObject) []Operation {
	var operations []Operation
	current := map[string]any{}
	desired := map[string]any{}
	conflictFields := []string{}

	compareManagedString("name", node.Name, remote.Name, binding.LastApplied.Name, bound, &current, &desired, &conflictFields)
	if node.Description != nil {
		compareManagedHTML(effectiveHTMLNotes(node), remote.Properties.HTMLNotes, binding.LastApplied.HTMLNotes, bound, &current, &desired, &conflictFields)
	} else {
		compareManagedPointer("notes", effectiveNotes(node), remote.Properties.Notes, binding.LastApplied.Notes, bound, &current, &desired, &conflictFields)
	}
	compareAssignee(node.Assignee, remote, binding.LastApplied.Assignee, bound, &current, &desired, &conflictFields)
	compareManagedPointer("due_on", node.DueOn, remote.Properties.DueOn, binding.LastApplied.DueOn, bound, &current, &desired, &conflictFields)
	compareManagedPointer("priority", node.Priority, remote.Properties.Priority, binding.LastApplied.Priority, bound, &current, &desired, &conflictFields)
	compareManagedPointer("component", node.Component, remote.Properties.Component, binding.LastApplied.Component, bound, &current, &desired, &conflictFields)
	if len(desired) > 0 {
		if len(conflictFields) > 0 {
			operations = append(operations, conflictOperation(node, remote.GID, "Remote values changed outside the last applied plan for: "+strings.Join(conflictFields, ", ")+".", current, desired))
			operations[len(operations)-1].LastApplied = appliedStateMap(binding.LastApplied)
		} else {
			operations = append(operations, Operation{Kind: "update", LogicalID: node.ID, GID: remote.GID, Reason: "Managed properties differ from desired state.", Current: current, Desired: desired})
		}
	}

	if node.State != nil && remote.Properties.State != *node.State {
		if bound && binding.LastApplied.State != nil && remote.Properties.State != *binding.LastApplied.State {
			operations = append(operations, conflictOperation(node, remote.GID, "Remote workflow state changed outside the last applied plan.", map[string]any{"state": remote.Properties.State}, map[string]any{"state": *node.State}))
			operations[len(operations)-1].LastApplied = appliedStateMap(binding.LastApplied)
		} else {
			operations = append(operations, Operation{Kind: "transition", LogicalID: node.ID, GID: remote.GID, Reason: "Workflow state differs from desired state.", Current: map[string]any{"state": remote.Properties.State}, Desired: map[string]any{"state": *node.State}})
		}
	}

	if node.Completed != nil && remote.Completed != *node.Completed {
		kind := "complete"
		if !*node.Completed {
			kind = "reopen"
		}
		if bound && binding.LastApplied.Completed != nil && remote.Completed != *binding.LastApplied.Completed {
			operations = append(operations, conflictOperation(node, remote.GID, "Remote completion state changed outside the last applied plan.", map[string]any{"completed": remote.Completed}, map[string]any{"completed": *node.Completed}))
			operations[len(operations)-1].LastApplied = appliedStateMap(binding.LastApplied)
		} else {
			operations = append(operations, Operation{Kind: kind, LogicalID: node.ID, GID: remote.GID, Reason: "Completion state differs from desired state.", Current: map[string]any{"completed": remote.Completed}, Desired: map[string]any{"completed": *node.Completed}})
		}
	}

	if node.ParentID != "" && resolved[node.ParentID] != nil {
		desiredParentGID := resolved[node.ParentID].GID
		if remote.ParentGID != desiredParentGID {
			if bound && binding.LastApplied.ParentID != "" && binding.LastApplied.ParentID != node.ParentID {
				operations = append(operations, conflictOperation(node, remote.GID, "Manifest and prior applied state disagree about parent ownership.", map[string]any{"parent_gid": remote.ParentGID, "last_parent_id": binding.LastApplied.ParentID}, map[string]any{"parent_id": node.ParentID, "parent_gid": desiredParentGID}))
				operations[len(operations)-1].LastApplied = appliedStateMap(binding.LastApplied)
			} else {
				operations = append(operations, Operation{Kind: "move", LogicalID: node.ID, GID: remote.GID, Reason: "Parent differs from desired hierarchy.", Current: map[string]any{"parent_gid": remote.ParentGID}, Desired: map[string]any{"parent_id": node.ParentID, "parent_gid": desiredParentGID}, Prerequisites: []string{"create-or-bind:" + node.ParentID}})
			}
		}
	}
	return operations
}

func appliedStateMap(value AppliedState) map[string]any {
	return map[string]any{"name": value.Name, "notes": value.Notes, "html_notes": value.HTMLNotes, "assignee": value.Assignee, "due_on": value.DueOn, "priority": value.Priority, "component": value.Component, "completed": value.Completed, "state": value.State, "parent_id": value.ParentID}
}

func diffDependencies(nodes []Node, resolved map[string]*RemoteObject, bindings *BindingState) []Operation {
	var operations []Operation
	for _, node := range nodes {
		if node.Type == "epic" {
			continue
		}
		remote := resolved[node.ID]
		current := map[string]bool{}
		if remote != nil {
			for _, gid := range remote.Dependencies {
				current[gid] = true
			}
		}
		for _, blockerID := range node.BlockedBy {
			blocker := resolved[blockerID]
			if remote != nil && blocker != nil && current[blocker.GID] {
				continue
			}
			desired := map[string]any{"blocked_id": node.ID, "blocker_id": blockerID}
			prerequisites := []string{"create-or-bind:" + node.ID, "create-or-bind:" + blockerID}
			if remote != nil {
				desired["blocked_gid"] = remote.GID
			}
			if blocker != nil {
				desired["blocker_gid"] = blocker.GID
			}
			operations = append(operations, Operation{Kind: "add_dependency", LogicalID: node.ID, GID: gidOf(remote), Reason: "Declared blocker is not present remotely.", Desired: desired, Prerequisites: prerequisites})
		}
		binding, ok := bindings.Objects[node.ID]
		if !ok || remote == nil {
			continue
		}
		desiredSet := stringSet(node.BlockedBy)
		for _, previousID := range binding.ManagedBlockerIDs {
			if desiredSet[previousID] {
				continue
			}
			previousBinding, ok := bindings.Objects[previousID]
			if !ok || !current[previousBinding.GID] {
				continue
			}
			operations = append(operations, Operation{Kind: "remove_dependency", LogicalID: node.ID, GID: remote.GID, Reason: "A previously plan-managed blocker is no longer declared.", Current: map[string]any{"blocker_id": previousID, "blocker_gid": previousBinding.GID}, Desired: map[string]any{"present": false}})
		}
	}
	return operations
}

func diffRemovedManagedObjects(manifest *Manifest, bindings *BindingState, snapshot *Snapshot, nodeByID map[string]Node) []Operation {
	if manifest.Spec.RemovalPolicy != "complete" {
		return nil
	}
	var operations []Operation
	for id, binding := range bindings.Objects {
		if _, present := nodeByID[id]; present || binding.Type == "epic" {
			continue
		}
		remote, ok := snapshot.Objects[binding.GID]
		if !ok || remote.Completed {
			continue
		}
		operations = append(operations, Operation{Kind: "complete_removed", LogicalID: id, GID: binding.GID, Reason: "Removal policy complete applies to a previously managed object omitted from the manifest.", Current: map[string]any{"completed": false}, Desired: map[string]any{"completed": true}, Destructive: false})
	}
	return operations
}

func unmanagedObjects(snapshot *Snapshot, used map[string]bool) []RemoteObject {
	var values []RemoteObject
	for gid, value := range snapshot.Objects {
		if !used[gid] {
			values = append(values, value)
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Type == values[j].Type {
			if values[i].Name == values[j].Name {
				return values[i].GID < values[j].GID
			}
			return values[i].Name < values[j].Name
		}
		return typeRank(values[i].Type) < typeRank(values[j].Type)
	})
	return values
}

func compareManagedString(field, desired, current, last string, bound bool, currentMap, desiredMap *map[string]any, conflicts *[]string) {
	if desired == current {
		return
	}
	(*currentMap)[field] = current
	(*desiredMap)[field] = desired
	if bound && last != "" && current != last && desired != current {
		*conflicts = append(*conflicts, field)
	}
}

func compareManagedPointer(field string, desired *string, current string, last *string, bound bool, currentMap, desiredMap *map[string]any, conflicts *[]string) {
	if desired == nil || *desired == current {
		return
	}
	(*currentMap)[field] = current
	(*desiredMap)[field] = *desired
	if bound && last != nil && current != *last && *desired != current {
		*conflicts = append(*conflicts, field)
	}
}

func compareManagedHTML(desired *string, current string, last *string, bound bool, currentMap, desiredMap *map[string]any, conflicts *[]string) {
	if desired == nil {
		return
	}
	desiredValue := normalizedHTML(*desired)
	currentValue := normalizedHTML(current)
	if desiredValue == currentValue {
		return
	}
	(*currentMap)["description"] = current
	(*desiredMap)["description"] = *desired
	if bound && last != nil && normalizedHTML(*last) != currentValue {
		*conflicts = append(*conflicts, "description")
	}
}

func normalizedHTML(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<body></body>"
	}
	normalized, err := richtext.NormalizeHTML(value)
	if err != nil {
		return strings.TrimSpace(value)
	}
	return normalized
}

func compareAssignee(desired *string, remote RemoteObject, last *string, bound bool, currentMap, desiredMap *map[string]any, conflicts *[]string) {
	if desired == nil {
		return
	}
	current := ""
	if remote.Properties.Assignee != nil {
		current = remote.Properties.Assignee.GID
		if strings.Contains(*desired, "@") && remote.Properties.Assignee.Email != "" {
			current = remote.Properties.Assignee.Email
		} else if strings.EqualFold(*desired, remote.Properties.Assignee.Name) {
			current = remote.Properties.Assignee.Name
		}
	}
	if strings.EqualFold(strings.TrimSpace(*desired), strings.TrimSpace(current)) {
		return
	}
	(*currentMap)["assignee"] = current
	(*desiredMap)["assignee"] = *desired
	if bound && last != nil && !strings.EqualFold(current, *last) && !strings.EqualFold(*desired, current) {
		*conflicts = append(*conflicts, "assignee")
	}
}

func desiredMap(node Node) map[string]any {
	value := map[string]any{"id": node.ID, "type": node.Type, "name": node.Name}
	if node.ParentID != "" {
		value["parent_id"] = node.ParentID
	}
	if notes := effectiveNotes(node); notes != nil {
		value["notes"] = *notes
	}
	if node.Description != nil {
		value["description"] = node.Description
		delete(value, "notes")
	}
	if node.Assignee != nil {
		value["assignee"] = *node.Assignee
	}
	if node.DueOn != nil {
		value["due_on"] = *node.DueOn
	}
	if node.Priority != nil {
		value["priority"] = *node.Priority
	}
	if node.Component != nil {
		value["component"] = *node.Component
	}
	if node.Timebox != nil {
		value["timebox"] = *node.Timebox
	}
	if node.Estimate != nil {
		value["estimate"] = *node.Estimate
	}
	if node.Completed != nil {
		value["completed"] = *node.Completed
	}
	if node.State != nil {
		value["state"] = *node.State
	}
	if len(node.BlockedBy) > 0 {
		value["blocked_by"] = node.BlockedBy
	}
	return value
}

func remoteMap(remote RemoteObject) map[string]any {
	return map[string]any{"gid": remote.GID, "type": remote.Type, "name": remote.Name, "parent_gid": remote.ParentGID, "completed": remote.Completed, "state": remote.Properties.State, "html_notes": remote.Properties.HTMLNotes}
}

func sortOperations(values []Operation) {
	sort.SliceStable(values, func(i, j int) bool {
		ri, rj := operationRank(values[i].Kind), operationRank(values[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if values[i].Kind == "create" || values[i].Kind == "bind" {
			ti, _ := values[i].Desired["type"].(string)
			tj, _ := values[j].Desired["type"].(string)
			if typeRank(ti) != typeRank(tj) {
				return typeRank(ti) < typeRank(tj)
			}
		}
		if values[i].LogicalID != values[j].LogicalID {
			return values[i].LogicalID < values[j].LogicalID
		}
		return fmt.Sprint(values[i].Desired) < fmt.Sprint(values[j].Desired)
	})
}

func operationRank(kind string) int {
	switch kind {
	case "conflict":
		return 0
	case "bind":
		return 10
	case "create":
		return 20
	case "move":
		return 30
	case "update":
		return 40
	case "complete", "reopen", "complete_removed", "transition":
		return 50
	case "remove_dependency":
		return 60
	case "add_dependency":
		return 70
	default:
		return 100
	}
}

func typeRank(kind string) int {
	switch kind {
	case "epic":
		return 0
	case "story":
		return 1
	case "bug":
		return 2
	case "spike":
		return 3
	case "task":
		return 4
	default:
		return 5
	}
}

func gidOf(remote *RemoteObject) string {
	if remote == nil {
		return ""
	}
	return remote.GID
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}
