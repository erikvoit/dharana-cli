package work

import (
	"context"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type CreateBugOptions struct {
	Name           string
	EpicRef        string
	Priority       string
	Environment    string
	Notes          string
	DryRun         bool
	Idempotent     bool
	IdempotencyKey string
}

type BugValue struct {
	GID                string     `json:"gid,omitempty"`
	Ref                string     `json:"ref"`
	Name               string     `json:"name"`
	Epic               EpicParent `json:"epic"`
	ProjectGID         string     `json:"project_gid"`
	ProjectName        string     `json:"project_name"`
	WorkspaceGID       string     `json:"workspace_gid"`
	WorkspaceName      string     `json:"workspace_name"`
	TypeMapping        string     `json:"type_mapping"`
	TypeFieldGID       string     `json:"type_field_gid,omitempty"`
	Priority           string     `json:"priority,omitempty"`
	Environment        string     `json:"environment,omitempty"`
	Permalink          string     `json:"permalink_url,omitempty"`
	Created            bool       `json:"created"`
	AddedToProject     bool       `json:"added_to_project"`
	DryRun             bool       `json:"dry_run"`
	IdempotencyKey     string     `json:"idempotency_key,omitempty"`
	IdempotentExisting bool       `json:"idempotent_existing,omitempty"`
}

type CreateBugResult struct {
	Bug BugValue `json:"bug"`
}

func (s *Service) CreateBug(ctx context.Context, opts CreateBugOptions) (*CreateBugResult, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.EpicRef = strings.TrimSpace(opts.EpicRef)
	opts.Priority = strings.TrimSpace(opts.Priority)
	opts.Environment = strings.TrimSpace(opts.Environment)
	opts.IdempotencyKey = strings.TrimSpace(opts.IdempotencyKey)
	if opts.IdempotencyKey != "" {
		opts.Idempotent = true
	}
	if opts.Name == "" {
		return nil, output.NewError("BUG_NAME_REQUIRED", "Provide a bug name.")
	}
	if opts.EpicRef == "" {
		return nil, output.NewError("EPIC_REFERENCE_REQUIRED", "Provide an epic reference with --epic.")
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
	if cfg.TaskTypes.Bug == "" {
		return nil, output.NewError("BUG_TYPE_NOT_CONFIGURED", "No Bug task type or work-type mapping is configured.")
	}

	epic, err := s.resolveEpic(ctx, resolved.Token, cfg, opts.EpicRef)
	if err != nil {
		return nil, err
	}

	base := BugValue{
		Ref:            "BUG:" + opts.Name,
		Name:           opts.Name,
		Epic:           toEpicParent(epic),
		ProjectGID:     cfg.ActiveProject.GID,
		ProjectName:    cfg.ActiveProject.Name,
		WorkspaceGID:   cfg.ActiveProject.WorkspaceGID,
		WorkspaceName:  cfg.ActiveProject.WorkspaceName,
		TypeMapping:    cfg.TaskTypes.Bug,
		TypeFieldGID:   cfg.TaskTypes.FieldGID,
		Priority:       opts.Priority,
		Environment:    opts.Environment,
		DryRun:         opts.DryRun,
		IdempotencyKey: opts.IdempotencyKey,
	}

	matches, err := s.asana().TasksByName(ctx, resolved.Token, cfg.ActiveProject.GID, opts.Name)
	if err != nil {
		return nil, mapAsanaError(err, "Could not check for duplicate bugs.")
	}
	matches = exactParentMatches(matches, opts.Name, epic.GID)
	if len(matches) > 0 {
		if opts.Idempotent {
			existing := matches[0]
			base.GID = existing.GID
			base.Permalink = existing.Permalink
			base.IdempotentExisting = true
			return &CreateBugResult{Bug: base}, nil
		}
		candidates := make([]BugValue, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, BugValue{
				GID:           match.GID,
				Ref:           "BUG:" + match.Name,
				Name:          match.Name,
				Epic:          toEpicParent(epic),
				ProjectGID:    cfg.ActiveProject.GID,
				ProjectName:   cfg.ActiveProject.Name,
				WorkspaceGID:  cfg.ActiveProject.WorkspaceGID,
				WorkspaceName: cfg.ActiveProject.WorkspaceName,
				TypeMapping:   cfg.TaskTypes.Bug,
				TypeFieldGID:  cfg.TaskTypes.FieldGID,
				Permalink:     match.Permalink,
			})
		}
		return nil, output.NewErrorWithCandidates("DUPLICATE_BUG", "A bug with this exact name already exists in the active project.", candidates)
	}

	if opts.DryRun {
		return &CreateBugResult{Bug: base}, nil
	}

	var customFields map[string]string
	if cfg.TaskTypes.FieldGID != "" {
		customFields = map[string]string{cfg.TaskTypes.FieldGID: cfg.TaskTypes.Bug}
	}
	task, err := s.asana().CreateTask(ctx, resolved.Token, asana.CreateTaskInput{
		Name:         opts.Name,
		WorkspaceGID: cfg.ActiveProject.WorkspaceGID,
		ParentGID:    epic.GID,
		Notes:        bugNotes(opts),
		CustomFields: customFields,
	})
	if err != nil {
		return nil, mapAsanaError(err, "Could not create the Asana bug.")
	}
	if err := s.asana().AddTaskToProject(ctx, resolved.Token, task.GID, cfg.ActiveProject.GID); err != nil {
		return nil, mapAsanaError(err, "Could not add the bug to the active Asana project.")
	}

	base.GID = task.GID
	base.Permalink = task.Permalink
	base.Created = true
	base.AddedToProject = true
	return &CreateBugResult{Bug: base}, nil
}

func bugNotes(opts CreateBugOptions) string {
	var lines []string
	if opts.Priority != "" {
		lines = append(lines, "Priority: "+opts.Priority)
	}
	if opts.Environment != "" {
		lines = append(lines, "Environment: "+opts.Environment)
	}
	if trimmedNotes := strings.TrimSpace(opts.Notes); trimmedNotes != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, trimmedNotes)
	}
	return strings.Join(lines, "\n")
}
