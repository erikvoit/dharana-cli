package work

import (
	"context"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type CreateStoryOptions struct {
	Name       string
	EpicRef    string
	Notes      string
	DryRun     bool
	Idempotent bool
}

type StoryValue struct {
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
	Permalink          string     `json:"permalink_url,omitempty"`
	Created            bool       `json:"created"`
	AddedToProject     bool       `json:"added_to_project"`
	DryRun             bool       `json:"dry_run"`
	IdempotentExisting bool       `json:"idempotent_existing,omitempty"`
}

type EpicParent struct {
	GID       string `json:"gid"`
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Permalink string `json:"permalink_url,omitempty"`
}

type CreateStoryResult struct {
	Story StoryValue `json:"story"`
}

func (s *Service) CreateStory(ctx context.Context, opts CreateStoryOptions) (*CreateStoryResult, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	opts.EpicRef = strings.TrimSpace(opts.EpicRef)
	if opts.Name == "" {
		return nil, output.NewError("STORY_NAME_REQUIRED", "Provide a story name.")
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
	if cfg.TaskTypes.Story == "" {
		return nil, output.NewError("STORY_TYPE_NOT_CONFIGURED", "No Story task type or work-type mapping is configured.")
	}

	epic, err := s.resolveEpic(ctx, resolved.Token, cfg, opts.EpicRef)
	if err != nil {
		return nil, err
	}

	base := StoryValue{
		Ref:           "STORY:" + opts.Name,
		Name:          opts.Name,
		Epic:          toEpicParent(epic),
		ProjectGID:    cfg.ActiveProject.GID,
		ProjectName:   cfg.ActiveProject.Name,
		WorkspaceGID:  cfg.ActiveProject.WorkspaceGID,
		WorkspaceName: cfg.ActiveProject.WorkspaceName,
		TypeMapping:   cfg.TaskTypes.Story,
		TypeFieldGID:  cfg.TaskTypes.FieldGID,
		DryRun:        opts.DryRun,
	}

	matches, err := s.asana().TasksByName(ctx, resolved.Token, cfg.ActiveProject.GID, opts.Name)
	if err != nil {
		return nil, mapAsanaError(err, "Could not check for duplicate stories.")
	}
	if len(matches) > 0 {
		if opts.Idempotent {
			existing := matches[0]
			base.GID = existing.GID
			base.Permalink = existing.Permalink
			base.IdempotentExisting = true
			return &CreateStoryResult{Story: base}, nil
		}
		candidates := make([]StoryValue, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, StoryValue{
				GID:         match.GID,
				Ref:         "STORY:" + match.Name,
				Name:        match.Name,
				Epic:        toEpicParent(epic),
				ProjectGID:  cfg.ActiveProject.GID,
				ProjectName: cfg.ActiveProject.Name,
				Permalink:   match.Permalink,
			})
		}
		return nil, output.NewErrorWithCandidates("DUPLICATE_STORY", "A story with this exact name already exists in the active project.", candidates)
	}

	if opts.DryRun {
		return &CreateStoryResult{Story: base}, nil
	}

	var customFields map[string]string
	if cfg.TaskTypes.FieldGID != "" {
		customFields = map[string]string{cfg.TaskTypes.FieldGID: cfg.TaskTypes.Story}
	}
	task, err := s.asana().CreateTask(ctx, resolved.Token, asana.CreateTaskInput{
		Name:         opts.Name,
		WorkspaceGID: cfg.ActiveProject.WorkspaceGID,
		ParentGID:    epic.GID,
		Notes:        opts.Notes,
		CustomFields: customFields,
	})
	if err != nil {
		return nil, mapAsanaError(err, "Could not create the Asana story.")
	}
	if err := s.asana().AddTaskToProject(ctx, resolved.Token, task.GID, cfg.ActiveProject.GID); err != nil {
		return nil, mapAsanaError(err, "Could not add the story to the active Asana project.")
	}

	base.GID = task.GID
	base.Permalink = task.Permalink
	base.Created = true
	base.AddedToProject = true
	return &CreateStoryResult{Story: base}, nil
}

func (s *Service) resolveEpic(ctx context.Context, token string, cfg *config.File, ref string) (*asana.Task, error) {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "EPIC:")
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, output.NewError("EPIC_REFERENCE_REQUIRED", "Provide an epic reference with --epic.")
	}
	if looksLikeGID(ref) {
		epic, err := s.asana().Task(ctx, token, ref)
		if err != nil {
			return nil, mapAsanaError(err, "Could not read the referenced epic.")
		}
		return epic, nil
	}

	matches, err := s.asana().TasksByName(ctx, token, cfg.ActiveProject.GID, ref)
	if err != nil {
		return nil, mapAsanaError(err, "Could not resolve the referenced epic.")
	}
	if len(matches) == 0 {
		return nil, output.NewError("EPIC_NOT_FOUND", "No epic matched the supplied reference.")
	}
	if len(matches) > 1 {
		candidates := make([]EpicParent, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, toEpicParent(&match))
		}
		return nil, output.NewErrorWithCandidates("AMBIGUOUS_EPIC", "Multiple epics matched the supplied reference.", candidates)
	}
	return &matches[0], nil
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

func toEpicParent(task *asana.Task) EpicParent {
	if task == nil {
		return EpicParent{}
	}
	return EpicParent{
		GID:       task.GID,
		Ref:       "EPIC:" + task.Name,
		Name:      task.Name,
		Permalink: task.Permalink,
	}
}
