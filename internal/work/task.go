package work

import (
	"context"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type CreateTaskOptions struct {
	Name           string
	ParentRef      string
	Assignee       string
	DueOn          string
	Estimate       string
	Notes          string
	DryRun         bool
	Idempotent     bool
	IdempotencyKey string
}

type ImplementationTaskValue struct {
	GID                string     `json:"gid,omitempty"`
	Ref                string     `json:"ref"`
	Name               string     `json:"name"`
	Parent             TaskParent `json:"parent"`
	ProjectGID         string     `json:"project_gid"`
	ProjectName        string     `json:"project_name"`
	WorkspaceGID       string     `json:"workspace_gid"`
	WorkspaceName      string     `json:"workspace_name"`
	Assignee           string     `json:"assignee,omitempty"`
	DueOn              string     `json:"due_on,omitempty"`
	Estimate           string     `json:"estimate,omitempty"`
	Permalink          string     `json:"permalink_url,omitempty"`
	Created            bool       `json:"created"`
	DryRun             bool       `json:"dry_run"`
	IdempotencyKey     string     `json:"idempotency_key,omitempty"`
	IdempotentExisting bool       `json:"idempotent_existing,omitempty"`
}

type TaskParent struct {
	GID       string `json:"gid"`
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Permalink string `json:"permalink_url,omitempty"`
}

type CreateTaskResult struct {
	Task ImplementationTaskValue `json:"task"`
}

func (s *Service) CreateImplementationTask(ctx context.Context, opts CreateTaskOptions) (*CreateTaskResult, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.ParentRef = strings.TrimSpace(opts.ParentRef)
	opts.Assignee = strings.TrimSpace(opts.Assignee)
	opts.DueOn = strings.TrimSpace(opts.DueOn)
	opts.Estimate = strings.TrimSpace(opts.Estimate)
	opts.IdempotencyKey = strings.TrimSpace(opts.IdempotencyKey)
	if opts.IdempotencyKey != "" {
		opts.Idempotent = true
	}
	if opts.Name == "" {
		return nil, output.NewError("TASK_NAME_REQUIRED", "Provide an implementation task name.")
	}
	if opts.ParentRef == "" {
		return nil, output.NewError("PARENT_REFERENCE_REQUIRED", "Provide a parent reference with --parent.")
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

	parent, err := s.resolveParent(ctx, resolved.Token, cfg, opts.ParentRef)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, output.NewError("PARENT_NOT_FOUND", "Parent task not found.")
	}

	base := ImplementationTaskValue{
		Ref:            "TASK:" + opts.Name,
		Name:           opts.Name,
		Parent:         toTaskParent(parent),
		ProjectGID:     cfg.ActiveProject.GID,
		ProjectName:    cfg.ActiveProject.Name,
		WorkspaceGID:   cfg.ActiveProject.WorkspaceGID,
		WorkspaceName:  cfg.ActiveProject.WorkspaceName,
		Assignee:       opts.Assignee,
		DueOn:          opts.DueOn,
		Estimate:       opts.Estimate,
		DryRun:         opts.DryRun,
		IdempotencyKey: opts.IdempotencyKey,
	}

	if opts.Idempotent {
		matches, err := s.allSubtasks(ctx, resolved.Token, parent.GID)
		if err != nil {
			return nil, mapAsanaError(err, "Could not check for duplicate implementation tasks.")
		}
		for _, match := range matches {
			if match.Name != opts.Name {
				continue
			}
			base.GID = match.GID
			base.Permalink = match.Permalink
			base.IdempotentExisting = true
			return &CreateTaskResult{Task: base}, nil
		}
	}

	if opts.DryRun {
		return &CreateTaskResult{Task: base}, nil
	}

	task, err := s.asana().CreateTask(ctx, resolved.Token, asana.CreateTaskInput{
		Name:         opts.Name,
		WorkspaceGID: cfg.ActiveProject.WorkspaceGID,
		ParentGID:    parent.GID,
		Notes:        implementationTaskNotes(opts),
	})
	if err != nil {
		return nil, mapAsanaError(err, "Could not create the Asana implementation task.")
	}

	base.GID = task.GID
	base.Permalink = task.Permalink
	base.Created = true
	return &CreateTaskResult{Task: base}, nil
}

func (s *Service) resolveParent(ctx context.Context, token string, cfg *config.File, ref string) (*asana.Task, error) {
	ref = strings.TrimSpace(ref)
	ref = trimKnownPrefix(ref, "STORY:", "BUG:", "SPIKE:", "TASK:")
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, output.NewError("PARENT_REFERENCE_REQUIRED", "Provide a parent reference with --parent.")
	}
	if looksLikeGID(ref) {
		parent, err := s.asana().Task(ctx, token, ref)
		if err != nil {
			return nil, mapAsanaError(err, "Could not read the referenced parent.")
		}
		return parent, nil
	}

	matches, err := s.asana().TasksByName(ctx, token, cfg.ActiveProject.GID, ref)
	if err != nil {
		return nil, mapAsanaError(err, "Could not resolve the referenced parent.")
	}
	if len(matches) == 0 {
		return nil, output.NewError("PARENT_NOT_FOUND", "No parent matched the supplied reference.")
	}
	if len(matches) > 1 {
		candidates := make([]TaskParent, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, toTaskParent(&match))
		}
		return nil, output.NewErrorWithCandidates("AMBIGUOUS_PARENT", "Multiple parents matched the supplied reference.", candidates)
	}
	return &matches[0], nil
}

func implementationTaskNotes(opts CreateTaskOptions) string {
	var lines []string
	if opts.Assignee != "" {
		lines = append(lines, "Assignee: "+opts.Assignee)
	}
	if opts.DueOn != "" {
		lines = append(lines, "Due: "+opts.DueOn)
	}
	if opts.Estimate != "" {
		lines = append(lines, "Estimate: "+opts.Estimate)
	}
	if trimmedNotes := strings.TrimSpace(opts.Notes); trimmedNotes != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, trimmedNotes)
	}
	return strings.Join(lines, "\n")
}

func toTaskParent(task *asana.Task) TaskParent {
	if task == nil {
		return TaskParent{}
	}
	return TaskParent{
		GID:       task.GID,
		Ref:       "TASK:" + task.Name,
		Name:      task.Name,
		Permalink: task.Permalink,
	}
}
