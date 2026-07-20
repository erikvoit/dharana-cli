package work

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
)

type AddDependencyOptions struct {
	BlockedRef   string
	BlockedByRef string
	DryRun       bool
}

type DependencyRef struct {
	GID       string `json:"gid"`
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Permalink string `json:"permalink_url,omitempty"`
}

type AddDependencyResult struct {
	Blocked            DependencyRef `json:"blocked"`
	BlockedBy          DependencyRef `json:"blocked_by"`
	Added              bool          `json:"added"`
	DryRun             bool          `json:"dry_run"`
	IdempotentExisting bool          `json:"idempotent_existing,omitempty"`
}

type RemoveDependencyOptions struct {
	BlockedRef   string
	BlockedByRef string
	DryRun       bool
}

type RemoveDependencyResult struct {
	Blocked   DependencyRef `json:"blocked"`
	BlockedBy DependencyRef `json:"blocked_by"`
	Found     bool          `json:"found"`
	Removed   bool          `json:"removed"`
	DryRun    bool          `json:"dry_run"`
}

func (s *Service) AddDependency(ctx context.Context, opts AddDependencyOptions) (*AddDependencyResult, error) {
	opts.BlockedRef = strings.TrimSpace(opts.BlockedRef)
	opts.BlockedByRef = strings.TrimSpace(opts.BlockedByRef)
	if opts.BlockedRef == "" {
		return nil, output.NewError("BLOCKED_REFERENCE_REQUIRED", "Provide the blocked work reference.")
	}
	if opts.BlockedByRef == "" {
		return nil, output.NewError("BLOCKER_REFERENCE_REQUIRED", "Provide the blocking work reference with --blocked-by.")
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

	blocked, err := s.resolveWorkReference(ctx, resolved.Token, opts.BlockedRef)
	if err != nil {
		return nil, err
	}
	blocker, err := s.resolveWorkReference(ctx, resolved.Token, opts.BlockedByRef)
	if err != nil {
		return nil, err
	}
	if blocked.Task.GID == blocker.Task.GID {
		return nil, output.NewError("SELF_DEPENDENCY", "Work cannot be blocked by itself.")
	}

	result := &AddDependencyResult{
		Blocked:   dependencyRef(blocked),
		BlockedBy: dependencyRef(blocker),
		DryRun:    opts.DryRun,
	}
	if hasDependency(blocked.Task, blocker.Task.GID) {
		result.IdempotentExisting = true
		return result, nil
	}
	if opts.DryRun {
		return result, nil
	}
	if err := s.asana().AddDependencies(ctx, resolved.Token, blocked.Task.GID, []string{blocker.Task.GID}); err != nil {
		return nil, mapAsanaError(err, "Could not add the Asana dependency.")
	}
	result.Added = true
	return result, nil
}

func (s *Service) RemoveDependency(ctx context.Context, opts RemoveDependencyOptions) (*RemoveDependencyResult, error) {
	opts.BlockedRef = strings.TrimSpace(opts.BlockedRef)
	opts.BlockedByRef = strings.TrimSpace(opts.BlockedByRef)
	if opts.BlockedRef == "" {
		return nil, output.NewError("BLOCKED_REFERENCE_REQUIRED", "Provide the blocked work reference.")
	}
	if opts.BlockedByRef == "" {
		return nil, output.NewError("BLOCKER_REFERENCE_REQUIRED", "Provide the blocking work reference with --blocked-by.")
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

	blocked, err := s.resolveWorkReference(ctx, resolved.Token, opts.BlockedRef)
	if err != nil {
		return nil, err
	}
	blocker, err := s.resolveWorkReference(ctx, resolved.Token, opts.BlockedByRef)
	if err != nil {
		return nil, err
	}
	if blocked.Task.GID == blocker.Task.GID {
		return nil, output.NewError("SELF_DEPENDENCY", "Work cannot be blocked by itself.")
	}

	result := &RemoveDependencyResult{
		Blocked:   dependencyRef(blocked),
		BlockedBy: dependencyRef(blocker),
		Found:     hasDependency(blocked.Task, blocker.Task.GID),
		DryRun:    opts.DryRun,
	}
	if !result.Found || opts.DryRun {
		return result, nil
	}
	if err := s.asana().RemoveDependencies(ctx, resolved.Token, blocked.Task.GID, []string{blocker.Task.GID}); err != nil {
		return nil, mapAsanaError(err, "Could not remove the Asana dependency.")
	}
	result.Removed = true
	return result, nil
}

type resolvedWorkReference struct {
	Task *asana.Task
	Ref  string
	Type string
}

func (s *Service) resolveWorkReference(ctx context.Context, token string, ref string) (*resolvedWorkReference, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID.")
	}
	if looksLikeGID(ref) {
		task, err := s.asana().Task(ctx, token, ref)
		if err == nil {
			if task == nil {
				return nil, output.NewError("WORK_NOT_FOUND", "The referenced work was not found.")
			}
			var taskTypes config.TaskTypes
			if cfg, err := s.config().Load(); err == nil && cfg != nil {
				taskTypes = cfg.TaskTypes
			}
			item := toWorkItem(*task, taskTypes, config.StateMappings{})
			if item.Type == "unknown" && task.Parent != nil {
				item.Type = "task"
			}
			return &resolvedWorkReference{Task: task, Ref: refForTask(item, task), Type: item.Type}, nil
		}
		var apiErr *asana.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return nil, mapAsanaError(err, "Could not read the referenced work.")
		}
	}

	entry, err := s.refs().Resolve(ref)
	if err != nil {
		if errors.Is(err, refcache.ErrReferenceRequired) {
			return nil, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID.")
		}
		if errors.Is(err, refcache.ErrReferenceNotFound) {
			return nil, output.NewError("REFERENCE_NOT_FOUND", "No cached reference matched the supplied value. Run refs refresh.")
		}
		return nil, output.NewError("REF_CACHE_READ_FAILED", "Could not read local reference cache.")
	}
	task, err := s.asana().Task(ctx, token, entry.GID)
	if err != nil {
		return nil, output.NewErrorWithDetails("STALE_REFERENCE", "The cached reference no longer resolves in Asana. Run refs refresh.", entry)
	}
	if task == nil {
		return nil, output.NewErrorWithDetails("STALE_REFERENCE", "The cached reference resolved to empty work in Asana. Run refs refresh.", entry)
	}
	return &resolvedWorkReference{Task: task, Ref: entry.Ref, Type: entry.Type}, nil
}

func refForTask(item WorkItem, task *asana.Task) string {
	if item.Ref != "" && !strings.HasPrefix(item.Ref, "UNKNOWN:") {
		return item.Ref
	}
	return task.GID
}

func dependencyRef(value *resolvedWorkReference) DependencyRef {
	itemType := value.Type
	if itemType == "" {
		itemType = "unknown"
	}
	if itemType == "unknown" && value.Task.Parent != nil {
		itemType = "task"
	}
	ref := value.Ref
	if ref == "" {
		ref = value.Task.GID
	}
	return DependencyRef{
		GID:       value.Task.GID,
		Ref:       ref,
		Name:      value.Task.Name,
		Type:      itemType,
		Status:    statusForTask(*value.Task),
		Permalink: value.Task.Permalink,
	}
}

func hasDependency(task *asana.Task, dependencyGID string) bool {
	for _, dependency := range task.Dependencies {
		if dependency.GID == dependencyGID {
			return true
		}
	}
	return false
}
