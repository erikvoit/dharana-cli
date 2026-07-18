package plan

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/work"
)

func (s *Service) Apply(ctx context.Context, manifest *Manifest, opts ApplyOptions) (*ApplyResult, error) {
	diff, err := s.Diff(ctx, manifest)
	if err != nil {
		return nil, err
	}
	result := &ApplyResult{
		ManifestID: diff.ManifestID, ManifestDigest: diff.ManifestDigest,
		DryRun: opts.DryRun, Diff: diff, Results: []OperationResult{}, BindingsPath: diff.BindingsPath,
	}
	if !diff.Validation.Valid {
		return result, applyValidationError(diff.Validation, "Plan validation failed; no operations were applied.")
	}
	if diff.Conflicted {
		return result, output.NewErrorWithDetails("PLAN_CONFLICT", "Plan contains conflicts that require explicit resolution.", diff)
	}
	if opts.DryRun {
		for _, operation := range diff.Operations {
			result.Results = append(result.Results, OperationResult{OperationID: operation.ID, LogicalID: operation.LogicalID, Kind: operation.Kind, Status: "planned", GID: operation.GID})
		}
		result.Converged = diff.Converged
		return result, nil
	}
	store := s.bindingStore()
	release, err := store.Acquire(manifest.Metadata.ID, diff.Project.GID)
	if err != nil {
		return result, output.NewErrorWithDetails("BINDING_LOCK_FAILED", "Could not acquire the project-scoped plan binding lock.", err.Error())
	}
	defer release()
	// Re-read remote state and bindings after acquiring the lock. Another CLI
	// process may have converged this plan while this process was waiting.
	diff, err = s.Diff(ctx, manifest)
	if err != nil {
		return result, err
	}
	result.Diff = diff
	result.BindingsPath = diff.BindingsPath
	if diff.Conflicted {
		return result, output.NewErrorWithDetails("PLAN_CONFLICT", "Plan contains conflicts that require explicit resolution.", diff)
	}
	if diff.Converged {
		result.Converged = true
		return result, nil
	}

	nodes := manifest.Nodes()
	nodeByID := map[string]Node{}
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	bindings, err := store.Load(manifest.Metadata.ID, diff.Project.GID, diff.Project.WorkspaceGID)
	if err != nil {
		return result, output.NewErrorWithDetails("BINDING_READ_FAILED", "Could not read durable plan bindings.", err.Error())
	}
	bindings.Context = manifest.Metadata.Context

	for _, operation := range diff.Operations {
		operationResult := OperationResult{OperationID: operation.ID, LogicalID: operation.LogicalID, Kind: operation.Kind, Status: "pending", GID: operation.GID}
		node, nodeExists := nodeByID[operation.LogicalID]
		bindings.RecordOperation(operation, "inconclusive", operation.GID, "Operation started; final remote outcome has not yet been recorded.")
		if err := store.Save(bindings); err != nil {
			return result, output.NewError("BINDING_WRITE_FAILED", "Could not persist the operation attempt before remote mutation.")
		}
		gid, applyErr := s.applyOperation(ctx, operation, node, nodeExists, bindings, store)
		if gid != "" {
			operationResult.GID = gid
		}
		if applyErr != nil {
			bindings.RecordOperation(operation, "failed", operationResult.GID, applyErr.Error())
			_ = store.Save(bindings)
			operationResult.Status = "failed"
			operationResult.Message = applyErr.Error()
			result.Results = append(result.Results, operationResult)
			result.Partial = hasSuccessfulResults(result.Results)
			for _, pending := range diff.Operations[len(result.Results):] {
				status := "pending"
				message := "Not attempted after an earlier operation failed."
				if operationDependsOn(pending, operation.LogicalID) {
					status = "skipped"
					message = "Skipped because a prerequisite operation failed."
				}
				result.Results = append(result.Results, OperationResult{OperationID: pending.ID, LogicalID: pending.LogicalID, Kind: pending.Kind, Status: status, GID: pending.GID, Message: message})
			}
			return result, output.NewErrorWithDetails("PLAN_PARTIAL_APPLY", "Plan application stopped after a failed operation. Successfully applied operations remain bound and are safe to reconcile.", result)
		}
		bindings.RecordOperation(operation, "succeeded", operationResult.GID, "")
		if err := store.Save(bindings); err != nil {
			result.Partial = true
			return result, output.NewErrorWithDetails("BINDING_WRITE_FAILED", "Remote operation succeeded but its durable outcome record could not be committed.", result)
		}
		operationResult.Status = "succeeded"
		result.Results = append(result.Results, operationResult)
	}

	for _, node := range nodes {
		binding, ok := bindings.Objects[node.ID]
		if !ok || binding.GID == "" {
			continue
		}
		bindings.Bind(node, binding.GID)
	}
	bindings.ManifestDigest = manifest.Digest()
	bindings.LastAppliedAt = time.Now().UTC().Format(time.RFC3339)
	if err := store.Save(bindings); err != nil {
		result.Partial = true
		return result, output.NewErrorWithDetails("BINDING_WRITE_FAILED", "Remote operations succeeded but final plan bindings could not be committed.", result)
	}

	verified, err := s.Diff(ctx, manifest)
	if err != nil {
		result.Partial = true
		return result, output.NewErrorWithDetails("PLAN_VERIFY_FAILED", "Plan operations completed but final remote convergence could not be verified.", err.Error())
	}
	result.Converged = verified.Converged
	if !verified.Converged {
		result.Partial = true
		return result, output.NewErrorWithDetails("PLAN_NOT_CONVERGED", "Plan operations completed but remote state is not converged.", verified)
	}
	return result, nil
}

func (s *Service) Reconcile(ctx context.Context, manifest *Manifest, opts ApplyOptions) (*ApplyResult, error) {
	return s.Apply(ctx, manifest, opts)
}

func (s *Service) Status(ctx context.Context, manifest *Manifest) (*StatusResult, error) {
	diff, err := s.Diff(ctx, manifest)
	if err != nil {
		code, message := planError(err, "PLAN_INACCESSIBLE", "Plan state could not be inspected.")
		result := &StatusResult{State: "inaccessible", Message: message}
		if manifest != nil {
			result.ManifestID = manifest.Metadata.ID
			result.ManifestDigest = manifest.Digest()
		}
		return result, output.NewErrorWithDetails(code, message, result)
	}
	result := &StatusResult{ManifestID: diff.ManifestID, ManifestDigest: diff.ManifestDigest, Diff: diff}
	if !diff.Validation.Valid {
		result.State = "invalid"
		return result, nil
	}
	bindings, err := s.bindingStore().Load(manifest.Metadata.ID, diff.Project.GID, diff.Project.WorkspaceGID)
	if err == nil {
		result.AppliedDigest = bindings.ManifestDigest
	}
	switch {
	case diff.Conflicted:
		result.State = "conflicted"
	case diff.Converged:
		result.State = "converged"
	case bindings != nil && len(bindings.Objects) > 0 && bindings.ManifestDigest != manifest.Digest():
		result.State = "partially_applied"
	default:
		result.State = "drifted"
	}
	return result, nil
}

func operationDependsOn(operation Operation, logicalID string) bool {
	for _, prerequisite := range operation.Prerequisites {
		if strings.HasSuffix(prerequisite, ":"+logicalID) {
			return true
		}
	}
	return false
}

func applyValidationError(validation ValidationResult, message string) error {
	for _, finding := range validation.RemoteFindings {
		if finding.Severity != "error" {
			continue
		}
		if finding.Code == "INVALID_AUTH" || finding.Code == "TOKEN_NOT_CONFIGURED" || strings.HasPrefix(finding.Code, "ASANA_") {
			return output.NewErrorWithDetails(finding.Code, finding.Message, validation)
		}
	}
	return output.NewErrorWithDetails("PLAN_INVALID", message, validation)
}

func (s *Service) Adopt(ctx context.Context, manifest *Manifest, opts AdoptOptions) (*AdoptResult, error) {
	diff, err := s.Diff(ctx, manifest)
	if err != nil {
		return nil, err
	}
	result := &AdoptResult{ManifestID: diff.ManifestID, DryRun: opts.DryRun || !opts.Apply, BindingsPath: diff.BindingsPath}
	if !diff.Validation.Valid {
		return result, applyValidationError(diff.Validation, "Plan validation failed; no bindings were adopted.")
	}
	var release func()
	if opts.Apply && !opts.DryRun {
		store := s.bindingStore()
		release, err = store.Acquire(manifest.Metadata.ID, diff.Project.GID)
		if err != nil {
			return result, output.NewErrorWithDetails("BINDING_LOCK_FAILED", "Could not acquire the project-scoped plan binding lock.", err.Error())
		}
		defer release()
		diff, err = s.Diff(ctx, manifest)
		if err != nil {
			return result, err
		}
	}
	for _, operation := range diff.Operations {
		if operation.Conflict {
			result.Conflicts = append(result.Conflicts, operation)
		}
	}
	if len(result.Conflicts) > 0 {
		return result, output.NewErrorWithDetails("PLAN_ADOPTION_CONFLICT", "Plan adoption contains ambiguous or stale bindings.", result)
	}
	store := s.bindingStore()
	bindings, err := store.Load(manifest.Metadata.ID, diff.Project.GID, diff.Project.WorkspaceGID)
	if err != nil {
		return result, output.NewErrorWithDetails("BINDING_READ_FAILED", "Could not read durable plan bindings.", err.Error())
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	nodeByID := map[string]Node{}
	for _, node := range manifest.Nodes() {
		nodeByID[node.ID] = node
	}
	for _, operation := range diff.Operations {
		if operation.Kind != "bind" {
			if operation.Kind == "create" {
				result.Candidates = append(result.Candidates, fuzzyAdoptionCandidates(nodeByID[operation.LogicalID], snapshot, bindings, diff.Operations)...)
			}
			continue
		}
		node := nodeByID[operation.LogicalID]
		remote := snapshot.Objects[operation.GID]
		binding := bindingForExisting(node, remote)
		binding.LogicalPath = bindings.logicalPath(node)
		result.Bindings = append(result.Bindings, binding)
		if opts.Apply && !opts.DryRun {
			bindings.Objects[node.ID] = binding
		}
	}
	if opts.Apply && !opts.DryRun {
		if err := store.Save(bindings); err != nil {
			return result, output.NewError("BINDING_WRITE_FAILED", "Could not save adopted plan bindings.")
		}
		result.Applied = true
		result.DryRun = false
	}
	return result, nil
}

func fuzzyAdoptionCandidates(node Node, snapshot *Snapshot, bindings *BindingState, operations []Operation) []Operation {
	parentGID := ""
	if binding, ok := bindings.Objects[node.ParentID]; ok {
		parentGID = binding.GID
	}
	for _, operation := range operations {
		if operation.LogicalID == node.ParentID && operation.Kind == "bind" {
			parentGID = operation.GID
		}
	}
	desiredName := strings.ToLower(strings.TrimSpace(node.Name))
	var candidates []Operation
	for _, remote := range snapshot.Objects {
		if remote.Type != node.Type || remote.ParentGID != parentGID {
			continue
		}
		remoteName := strings.ToLower(strings.TrimSpace(remote.Name))
		if remoteName == desiredName || !similarName(desiredName, remoteName) {
			continue
		}
		candidates = append(candidates, Operation{Kind: "candidate", LogicalID: node.ID, GID: remote.GID, Reason: "A similar name is reported for review but is never adopted automatically.", Current: remoteMap(remote), Desired: desiredMap(node)})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].GID < candidates[j].GID })
	return candidates
}

func similarName(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a) || levenshtein(a, b) <= 3
}

func levenshtein(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func (s *Service) applyOperation(ctx context.Context, operation Operation, node Node, nodeExists bool, bindings *BindingState, store *BindingStore) (string, error) {
	switch operation.Kind {
	case "bind":
		if !nodeExists {
			return "", fmt.Errorf("manifest node %s is missing", operation.LogicalID)
		}
		detail, err := s.work().UpdateWork(ctx, work.UpdateWorkOptions{Ref: operation.GID, DryRun: true})
		if err != nil {
			return operation.GID, err
		}
		bindings.Objects[node.ID] = Binding{
			LogicalID: node.ID, GID: operation.GID, Type: node.Type, ParentID: node.ParentID,
			LastKnownName: detail.Before.Name, LastVerifiedAt: time.Now().UTC().Format(time.RFC3339),
			LastApplied: AppliedState{Name: detail.Before.Name, Notes: stringPointer(detail.Before.Notes), DueOn: optionalStringPointer(detail.Before.DueOn), Priority: optionalStringPointer(detail.Before.Priority), Component: optionalStringPointer(detail.Before.Component), Completed: boolPointer(detail.Before.Completed), ParentID: node.ParentID},
		}
		if detail.Before.Assignee != nil {
			identity := detail.Before.Assignee.GID
			bindingsValue := bindings.Objects[node.ID]
			bindingsValue.LastApplied.Assignee = &identity
			bindings.Objects[node.ID] = bindingsValue
		}
		return operation.GID, store.Save(bindings)
	case "create":
		gid, err := s.createNode(ctx, node, bindings)
		if err != nil {
			return gid, err
		}
		baseline, err := s.work().UpdateWork(ctx, work.UpdateWorkOptions{Ref: gid, DryRun: true})
		if err != nil {
			return gid, err
		}
		remote := RemoteObject{GID: gid, Type: node.Type, Name: baseline.Before.Name, ParentGID: baseline.Before.ParentGID, Completed: baseline.Before.Completed, Properties: baseline.Before}
		bindings.Objects[node.ID] = bindingForExisting(node, remote)
		if err := store.Save(bindings); err != nil {
			return gid, err
		}
		if err := s.applyCreatedNodeProperties(ctx, node, gid); err != nil {
			return gid, err
		}
		bindings.Bind(node, gid)
		return gid, store.Save(bindings)
	case "update":
		if !nodeExists {
			return operation.GID, fmt.Errorf("manifest node %s is missing", operation.LogicalID)
		}
		_, err := s.work().UpdateWork(ctx, updateOptionsForNode(node, operation.GID))
		if err == nil {
			bindings.Bind(node, operation.GID)
			err = store.Save(bindings)
		}
		return operation.GID, err
	case "move":
		parentID, _ := operation.Desired["parent_id"].(string)
		parent, ok := bindings.Objects[parentID]
		if !ok {
			return operation.GID, fmt.Errorf("parent binding %s is not available", parentID)
		}
		_, err := s.work().MoveWork(ctx, work.MoveWorkOptions{Ref: operation.GID, ParentRef: parent.GID})
		return operation.GID, err
	case "complete", "reopen", "complete_removed":
		_, err := s.work().CompleteWork(ctx, work.CompleteWorkOptions{Ref: operation.GID, Reopen: operation.Kind == "reopen"})
		return operation.GID, err
	case "add_dependency":
		blockedID, _ := operation.Desired["blocked_id"].(string)
		blockerID, _ := operation.Desired["blocker_id"].(string)
		blocked, blockedOK := bindings.Objects[blockedID]
		blocker, blockerOK := bindings.Objects[blockerID]
		if !blockedOK || !blockerOK {
			return operation.GID, fmt.Errorf("dependency endpoint bindings are not available")
		}
		_, err := s.work().AddDependency(ctx, work.AddDependencyOptions{BlockedRef: blocked.GID, BlockedByRef: blocker.GID})
		return blocked.GID, err
	case "remove_dependency":
		blockerGID, _ := operation.Current["blocker_gid"].(string)
		_, err := s.work().RemoveDependency(ctx, work.RemoveDependencyOptions{BlockedRef: operation.GID, BlockedByRef: blockerGID})
		return operation.GID, err
	default:
		return operation.GID, fmt.Errorf("unsupported plan operation %s", operation.Kind)
	}
}

func (s *Service) createNode(ctx context.Context, node Node, bindings *BindingState) (string, error) {
	idempotencyKey := bindings.ManifestID + ":" + node.ID
	parent := Binding{}
	if node.ParentID != "" {
		var ok bool
		parent, ok = bindings.Objects[node.ParentID]
		if !ok || parent.GID == "" {
			return "", fmt.Errorf("parent binding %s is not available", node.ParentID)
		}
	}
	notes := ""
	if node.Notes != nil {
		notes = *node.Notes
	}
	switch node.Type {
	case "epic":
		result, err := s.work().CreateEpic(ctx, work.CreateEpicOptions{Name: node.Name, Notes: notes, IdempotencyKey: idempotencyKey})
		if err != nil {
			return "", err
		}
		return result.Epic.GID, nil
	case "story":
		result, err := s.work().CreateStory(ctx, work.CreateStoryOptions{Name: node.Name, EpicRef: parent.GID, Notes: notes, IdempotencyKey: idempotencyKey})
		if err != nil {
			return "", err
		}
		return result.Story.GID, nil
	case "bug":
		result, err := s.work().CreateBug(ctx, work.CreateBugOptions{Name: node.Name, EpicRef: parent.GID, Notes: notes, IdempotencyKey: idempotencyKey})
		if err != nil {
			return "", err
		}
		return result.Bug.GID, nil
	case "spike":
		timebox := ""
		if node.Timebox != nil {
			timebox = *node.Timebox
		}
		result, err := s.work().CreateSpike(ctx, work.CreateSpikeOptions{Name: node.Name, EpicRef: parent.GID, Timebox: timebox, Notes: notes, IdempotencyKey: idempotencyKey})
		if err != nil {
			return "", err
		}
		return result.Spike.GID, nil
	case "task":
		estimate := ""
		if node.Estimate != nil {
			estimate = *node.Estimate
		}
		result, err := s.work().CreateImplementationTask(ctx, work.CreateTaskOptions{Name: node.Name, ParentRef: parent.GID, Estimate: estimate, Notes: notes, IdempotencyKey: idempotencyKey})
		if err != nil {
			return "", err
		}
		return result.Task.GID, nil
	default:
		return "", fmt.Errorf("unsupported plan work type %s", node.Type)
	}
}

func (s *Service) applyCreatedNodeProperties(ctx context.Context, node Node, gid string) error {
	if node.Assignee != nil || node.DueOn != nil || node.Component != nil || node.Priority != nil {
		opts := work.UpdateWorkOptions{Ref: gid}
		opts.Assignee = node.Assignee
		opts.DueOn = node.DueOn
		opts.Component = node.Component
		opts.Priority = node.Priority
		if node.Assignee != nil && strings.TrimSpace(*node.Assignee) == "" {
			opts.Assignee = nil
			opts.ClearAssignee = true
		}
		if node.DueOn != nil && strings.TrimSpace(*node.DueOn) == "" {
			opts.DueOn = nil
			opts.ClearDueOn = true
		}
		if _, err := s.work().UpdateWork(ctx, opts); err != nil {
			return err
		}
	}
	if node.Completed != nil && *node.Completed && node.Type != "epic" {
		_, err := s.work().CompleteWork(ctx, work.CompleteWorkOptions{Ref: gid})
		return err
	}
	return nil
}

func updateOptionsForNode(node Node, gid string) work.UpdateWorkOptions {
	name := node.Name
	opts := work.UpdateWorkOptions{
		Ref: gid, Name: &name, Notes: effectiveNotes(node), Assignee: node.Assignee,
		DueOn: node.DueOn, Priority: node.Priority, Component: node.Component,
	}
	if node.Assignee != nil && strings.TrimSpace(*node.Assignee) == "" {
		opts.Assignee = nil
		opts.ClearAssignee = true
	}
	if node.DueOn != nil && strings.TrimSpace(*node.DueOn) == "" {
		opts.DueOn = nil
		opts.ClearDueOn = true
	}
	return opts
}

func bindingForExisting(node Node, remote RemoteObject) Binding {
	binding := Binding{
		LogicalID: node.ID, GID: remote.GID, Type: remote.Type, ParentID: node.ParentID,
		LogicalPath:   node.ParentID + "/" + node.ID,
		LastKnownName: remote.Name, LastVerifiedAt: time.Now().UTC().Format(time.RFC3339),
		LastApplied: AppliedState{Name: remote.Name, Notes: stringPointer(remote.Properties.Notes), DueOn: optionalStringPointer(remote.Properties.DueOn), Priority: optionalStringPointer(remote.Properties.Priority), Component: optionalStringPointer(remote.Properties.Component), Completed: boolPointer(remote.Completed), ParentID: node.ParentID},
	}
	if remote.Properties.Assignee != nil {
		identity := remote.Properties.Assignee.GID
		binding.LastApplied.Assignee = &identity
	}
	return binding
}

func stringPointer(value string) *string { return &value }

func optionalStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func boolPointer(value bool) *bool { return &value }

func hasSuccessfulResults(values []OperationResult) bool {
	for _, value := range values {
		if value.Status == "succeeded" {
			return true
		}
	}
	return false
}
