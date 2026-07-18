package work

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
	"github.com/erikvoit/dharana-cli/internal/richtext"
)

type GetWorkResult struct {
	Item         WorkDetail      `json:"item"`
	Authority    Authority       `json:"authority"`
	CachedRef    *refcache.Entry `json:"cached_ref,omitempty"`
	NextCommands []string        `json:"suggested_next_commands,omitempty"`
}

type Authority struct {
	RemoteValidated bool   `json:"remote_validated"`
	Source          string `json:"source"`
	CacheSource     string `json:"cache_source,omitempty"`
}

type WorkDetail struct {
	GID                 string                `json:"gid"`
	Ref                 string                `json:"ref"`
	Name                string                `json:"name"`
	Type                string                `json:"type"`
	Status              string                `json:"status"`
	Parent              *TaskParent           `json:"parent,omitempty"`
	Project             ProjectMembership     `json:"project"`
	Assignee            *UserValue            `json:"assignee,omitempty"`
	DueOn               string                `json:"due_on,omitempty"`
	NotesSummary        string                `json:"notes_summary,omitempty"`
	Description         *richtext.Description `json:"description,omitempty"`
	DescriptionLossless bool                  `json:"description_lossless,omitempty"`
	Fields              []FieldValue          `json:"fields,omitempty"`
	Dependencies        DependencySet         `json:"dependencies"`
	Permalink           string                `json:"permalink_url,omitempty"`
}

type ProjectMembership struct {
	GID    string `json:"gid"`
	Name   string `json:"name,omitempty"`
	Member bool   `json:"member"`
}

type UserValue struct {
	GID   string `json:"gid"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type FieldValue struct {
	GID          string `json:"gid"`
	Name         string `json:"name,omitempty"`
	DisplayValue string `json:"display_value,omitempty"`
	EnumGID      string `json:"enum_gid,omitempty"`
	EnumName     string `json:"enum_name,omitempty"`
}

type DependencySet struct {
	Blockers           []DependencyRef `json:"blockers"`
	DirectDependents   []DependencyRef `json:"direct_dependents,omitempty"`
	UnresolvedBlockers []DependencyRef `json:"unresolved_blockers,omitempty"`
}

type UpdateWorkOptions struct {
	Ref           string
	Name          *string
	Notes         *string
	Description   *richtext.Description
	Assignee      *string
	ClearAssignee bool
	DueOn         *string
	ClearDueOn    bool
	Priority      *string
	Component     *string
	DryRun        bool
}

type UpdateWorkResult struct {
	Target        DependencyRef    `json:"target"`
	Before        WorkProperties   `json:"before"`
	After         WorkProperties   `json:"after"`
	Changes       []PropertyChange `json:"changes"`
	DryRun        bool             `json:"dry_run"`
	Noop          bool             `json:"noop"`
	RefreshedRefs bool             `json:"refreshed_refs,omitempty"`
}

type WorkProperties struct {
	Name      string     `json:"name"`
	Notes     string     `json:"notes,omitempty"`
	HTMLNotes string     `json:"html_notes,omitempty"`
	Assignee  *UserValue `json:"assignee,omitempty"`
	DueOn     string     `json:"due_on,omitempty"`
	Priority  string     `json:"priority,omitempty"`
	Component string     `json:"component,omitempty"`
	Completed bool       `json:"completed"`
	ParentGID string     `json:"parent_gid,omitempty"`
}

type PropertyChange struct {
	Property string `json:"property"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
}

type CompleteWorkOptions struct {
	Ref    string
	DryRun bool
	Reopen bool
}

type CompleteWorkResult struct {
	Target           DependencyRef   `json:"target"`
	BeforeCompleted  bool            `json:"before_completed"`
	AfterCompleted   bool            `json:"after_completed"`
	DryRun           bool            `json:"dry_run"`
	Noop             bool            `json:"noop"`
	DirectDependents []DependencyRef `json:"direct_dependents,omitempty"`
	RefreshedRefs    bool            `json:"refreshed_refs,omitempty"`
}

type MoveWorkOptions struct {
	Ref       string
	ParentRef string
	DryRun    bool
}

type MoveWorkResult struct {
	Target        DependencyRef `json:"target"`
	OldParent     *TaskParent   `json:"old_parent,omitempty"`
	NewParent     TaskParent    `json:"new_parent"`
	DryRun        bool          `json:"dry_run"`
	Noop          bool          `json:"noop"`
	Operations    []string      `json:"operations"`
	RefreshedRefs bool          `json:"refreshed_refs,omitempty"`
}

type CommentWorkOptions struct {
	Ref    string
	Body   string
	DryRun bool
}

type CommentWorkResult struct {
	Target DependencyRef      `json:"target"`
	Body   string             `json:"body"`
	DryRun bool               `json:"dry_run"`
	Story  *CommentStoryValue `json:"story,omitempty"`
}

type CommentStoryValue struct {
	GID       string     `json:"gid"`
	CreatedAt string     `json:"created_at,omitempty"`
	Author    *UserValue `json:"author,omitempty"`
}

type ReconcileOptions struct {
	Ref    string
	DryRun bool
	Apply  bool
}

type ReconcileResult struct {
	Scope       string               `json:"scope"`
	Observed    ReconcileObserved    `json:"observed"`
	Desired     ReconcileDesired     `json:"desired"`
	Operations  []ReconcileOperation `json:"operations"`
	DryRun      bool                 `json:"dry_run"`
	Applied     bool                 `json:"applied"`
	Diagnostics []string             `json:"diagnostics,omitempty"`
}

type ValidatePropertiesOptions struct {
	Assignee  *string
	DueOn     *string
	Priority  *string
	Component *string
}

type ValidatePropertiesResult struct {
	Assignee  *UserValue `json:"assignee,omitempty"`
	DueOn     string     `json:"due_on,omitempty"`
	Priority  string     `json:"priority,omitempty"`
	Component string     `json:"component,omitempty"`
}

// ValidateProperties resolves plan-supplied values against the effective
// workspace and project without mutating work. Declarative planning uses this
// for nodes that do not exist yet and therefore cannot be validated through an
// update dry-run.
func (s *Service) ValidateProperties(ctx context.Context, opts ValidatePropertiesOptions) (*ValidatePropertiesResult, error) {
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	result := &ValidatePropertiesResult{}
	if opts.Assignee != nil && strings.TrimSpace(*opts.Assignee) != "" {
		user, err := s.resolveUser(ctx, resolved.Token, cfg.ActiveProject.WorkspaceGID, *opts.Assignee)
		if err != nil {
			return nil, err
		}
		result.Assignee = userValue(user)
	}
	if opts.DueOn != nil && strings.TrimSpace(*opts.DueOn) != "" {
		value, err := normalizeDueOn(*opts.DueOn)
		if err != nil {
			return nil, err
		}
		result.DueOn = value
	}
	if opts.Priority != nil && strings.TrimSpace(*opts.Priority) != "" {
		value, err := s.resolveEnumFieldValue(ctx, resolved.Token, cfg, cfg.Fields.PriorityGID, *opts.Priority, "PRIORITY_FIELD_NOT_CONFIGURED")
		if err != nil {
			return nil, err
		}
		result.Priority = value.Name
	}
	if opts.Component != nil && strings.TrimSpace(*opts.Component) != "" {
		value, err := s.resolveEnumFieldValue(ctx, resolved.Token, cfg, cfg.Fields.ComponentGID, *opts.Component, "COMPONENT_FIELD_NOT_CONFIGURED")
		if err != nil {
			return nil, err
		}
		result.Component = value.Name
	}
	return result, nil
}

type ReconcileObserved struct {
	ProjectGID                string           `json:"project_gid,omitempty"`
	CacheProjectGID           string           `json:"cache_project_gid,omitempty"`
	MissingCacheEntries       []DependencyRef  `json:"missing_cache_entries,omitempty"`
	StaleCacheEntries         []refcache.Entry `json:"stale_cache_entries,omitempty"`
	MissingProjectMemberships []DependencyRef  `json:"missing_project_memberships,omitempty"`
	TypeMismatches            []TypeMismatch   `json:"type_mismatches,omitempty"`
	UncachedWork              []DependencyRef  `json:"uncached_work,omitempty"`
}

type TypeMismatch struct {
	GID         string `json:"gid"`
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Intended    string `json:"intended"`
	Observed    string `json:"observed"`
	Remediation string `json:"remediation"`
}

type ReconcileDesired struct {
	ProjectGID string `json:"project_gid,omitempty"`
	CacheFresh bool   `json:"cache_fresh"`
}

type ReconcileOperation struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Safe        bool   `json:"safe"`
}

func (s *Service) GetWork(ctx context.Context, ref string) (*GetWorkResult, error) {
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	workRef, cached, err := s.resolveWorkReferenceWithCache(ctx, resolved.Token, strings.TrimSpace(ref), cfg.TaskTypes)
	if err != nil {
		return nil, err
	}
	tasks, err := s.allProjectTasks(ctx, resolved.Token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list project work for dependency context.")
	}
	detail := s.workDetail(*workRef.Task, cfg, workRef.Ref, tasks)
	return &GetWorkResult{
		Item:      detail,
		CachedRef: cached,
		Authority: Authority{
			RemoteValidated: true,
			Source:          "asana",
			CacheSource:     cacheSource(cached),
		},
		NextCommands: nextCommandsForWork(detail),
	}, nil
}

func (s *Service) UpdateWork(ctx context.Context, opts UpdateWorkOptions) (*UpdateWorkResult, error) {
	if opts.Notes != nil && opts.Description != nil {
		return nil, output.NewError("DESCRIPTION_NOTES_CONFLICT", "Use Markdown description or plain notes, not both.")
	}
	opts.Ref = strings.TrimSpace(opts.Ref)
	if opts.Ref == "" {
		return nil, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID.")
	}
	if opts.ClearAssignee && opts.Assignee != nil {
		return nil, output.NewError("ASSIGNEE_MODE_CONFLICT", "Use --assignee or --clear-assignee, not both.")
	}
	if opts.ClearDueOn && opts.DueOn != nil {
		return nil, output.NewError("DUE_ON_MODE_CONFLICT", "Use --due-on or --clear-due-on, not both.")
	}

	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveWorkReference(ctx, resolved.Token, opts.Ref)
	if err != nil {
		return nil, err
	}
	before := propertiesForTask(*target.Task, cfg)
	update := asana.UpdateTaskInput{}
	after := before
	var changes []PropertyChange

	if opts.Name != nil {
		name := strings.TrimSpace(*opts.Name)
		if name == "" {
			return nil, output.NewError("WORK_NAME_REQUIRED", "Name cannot be empty.")
		}
		addChange(&changes, "name", before.Name, name)
		update.Name = &name
		after.Name = name
	}
	if opts.Notes != nil {
		notes := *opts.Notes
		addChange(&changes, "notes", before.Notes, notes)
		update.Notes = &notes
		after.Notes = notes
	}
	if opts.Description != nil {
		htmlNotes, err := richtext.RenderMarkdown(opts.Description.Content)
		if err != nil || strings.ToLower(strings.TrimSpace(opts.Description.Format)) != "markdown" {
			detail := "description format must be markdown"
			if err != nil {
				detail = err.Error()
			}
			return nil, output.NewErrorWithDetails("INVALID_MARKDOWN_DESCRIPTION", "The Markdown description cannot be rendered safely.", detail)
		}
		addChange(&changes, "description", before.HTMLNotes, htmlNotes)
		update.HTMLNotes = &htmlNotes
		after.HTMLNotes = htmlNotes
		plain, plainErr := richtext.PlainTextFromHTML(htmlNotes)
		if plainErr == nil {
			after.Notes = plain
		}
	}
	if opts.Assignee != nil || opts.ClearAssignee {
		assigneeGID := ""
		var assignee *UserValue
		if opts.Assignee != nil {
			user, err := s.resolveUser(ctx, resolved.Token, cfg.ActiveProject.WorkspaceGID, *opts.Assignee)
			if err != nil {
				return nil, err
			}
			assigneeGID = user.GID
			assignee = userValue(user)
		}
		if userGID(before.Assignee) != userGID(assignee) {
			changes = append(changes, PropertyChange{Property: "assignee", Before: before.Assignee, After: assignee})
		}
		update.AssigneeGID = &assigneeGID
		after.Assignee = assignee
	}
	if opts.DueOn != nil || opts.ClearDueOn {
		dueOn := ""
		if opts.DueOn != nil {
			var err error
			dueOn, err = normalizeDueOn(*opts.DueOn)
			if err != nil {
				return nil, err
			}
		}
		addChange(&changes, "due_on", before.DueOn, dueOn)
		update.DueOn = &dueOn
		after.DueOn = dueOn
	}
	if opts.Priority != nil {
		update.CustomFields = ensureCustomFields(update.CustomFields)
		if strings.TrimSpace(*opts.Priority) == "" {
			if cfg.Fields.PriorityGID == "" {
				return nil, output.NewError("PRIORITY_FIELD_NOT_CONFIGURED", "The selected project does not configure a Priority field.")
			}
			update.CustomFields[cfg.Fields.PriorityGID] = ""
			addChange(&changes, "priority", before.Priority, "")
			after.Priority = ""
		} else {
			value, err := s.resolveEnumFieldValue(ctx, resolved.Token, cfg, cfg.Fields.PriorityGID, *opts.Priority, "PRIORITY_FIELD_NOT_CONFIGURED")
			if err != nil {
				return nil, err
			}
			update.CustomFields[cfg.Fields.PriorityGID] = value.GID
			addChange(&changes, "priority", before.Priority, value.Name)
			after.Priority = value.Name
		}
	}
	if opts.Component != nil {
		update.CustomFields = ensureCustomFields(update.CustomFields)
		if strings.TrimSpace(*opts.Component) == "" {
			if cfg.Fields.ComponentGID == "" {
				return nil, output.NewError("COMPONENT_FIELD_NOT_CONFIGURED", "The selected project does not configure a Component field.")
			}
			update.CustomFields[cfg.Fields.ComponentGID] = ""
			addChange(&changes, "component", before.Component, "")
			after.Component = ""
		} else {
			value, err := s.resolveEnumFieldValue(ctx, resolved.Token, cfg, cfg.Fields.ComponentGID, *opts.Component, "COMPONENT_FIELD_NOT_CONFIGURED")
			if err != nil {
				return nil, err
			}
			update.CustomFields[cfg.Fields.ComponentGID] = value.GID
			addChange(&changes, "component", before.Component, value.Name)
			after.Component = value.Name
		}
	}
	result := &UpdateWorkResult{Target: dependencyRef(target), Before: before, After: after, Changes: changes, DryRun: opts.DryRun, Noop: len(changes) == 0}
	if opts.DryRun || result.Noop {
		return result, nil
	}
	updated, err := s.asana().UpdateTask(ctx, resolved.Token, target.Task.GID, update)
	if err != nil {
		return nil, mapAsanaError(err, "Could not update the Asana work item.")
	}
	result.After = propertiesForTask(*updated, cfg)
	if err := s.refreshRefsBestEffort(ctx); err == nil {
		result.RefreshedRefs = true
	}
	return result, nil
}

func (s *Service) CompleteWork(ctx context.Context, opts CompleteWorkOptions) (*CompleteWorkResult, error) {
	resolved, _, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveWorkReference(ctx, resolved.Token, opts.Ref)
	if err != nil {
		return nil, err
	}
	if target.Type == "epic" {
		return nil, output.NewError("UNSUPPORTED_WORK_TYPE", "Epics are not completed or reopened by MVP+2 lifecycle commands.")
	}
	after := !opts.Reopen
	result := &CompleteWorkResult{
		Target:          dependencyRef(target),
		BeforeCompleted: target.Task.Completed,
		AfterCompleted:  after,
		DryRun:          opts.DryRun,
		Noop:            target.Task.Completed == after,
	}
	result.DirectDependents, _ = s.directDependents(ctx, resolved.Token, target.Task.GID)
	if opts.DryRun || result.Noop {
		return result, nil
	}
	updated, err := s.asana().UpdateTask(ctx, resolved.Token, target.Task.GID, asana.UpdateTaskInput{Completed: &after})
	if err != nil {
		return nil, mapAsanaError(err, "Could not update completion state.")
	}
	result.AfterCompleted = updated.Completed
	if err := s.refreshRefsBestEffort(ctx); err == nil {
		result.RefreshedRefs = true
	}
	return result, nil
}

func (s *Service) MoveWork(ctx context.Context, opts MoveWorkOptions) (*MoveWorkResult, error) {
	opts.Ref = strings.TrimSpace(opts.Ref)
	opts.ParentRef = strings.TrimSpace(opts.ParentRef)
	if opts.ParentRef == "" {
		return nil, output.NewError("PARENT_REFERENCE_REQUIRED", "Provide a parent reference with --parent.")
	}
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveWorkReference(ctx, resolved.Token, opts.Ref)
	if err != nil {
		return nil, err
	}
	parent, err := s.resolveWorkReference(ctx, resolved.Token, opts.ParentRef)
	if err != nil {
		return nil, err
	}
	if target.Task.GID == parent.Task.GID {
		return nil, output.NewError("SELF_PARENT", "Work cannot be moved under itself.")
	}
	if err := validateMove(target, parent); err != nil {
		return nil, err
	}
	if sameParent(target.Task, parent.Task.GID) {
		return &MoveWorkResult{Target: dependencyRef(target), OldParent: taskParentFromSummary(target.Task.Parent), NewParent: toTaskParentValue(parent.Task), DryRun: opts.DryRun, Noop: true}, nil
	}
	if projectGIDForTask(*target.Task) != "" && projectGIDForTask(*target.Task) != cfg.ActiveProject.GID {
		return nil, output.NewError("CROSS_PROJECT_MOVE", "Cross-project moves are not supported.")
	}
	if projectGIDForTask(*parent.Task) != "" && projectGIDForTask(*parent.Task) != cfg.ActiveProject.GID {
		return nil, output.NewError("CROSS_PROJECT_MOVE", "Cross-project moves are not supported.")
	}
	result := &MoveWorkResult{
		Target:     dependencyRef(target),
		OldParent:  taskParentFromSummary(target.Task.Parent),
		NewParent:  toTaskParentValue(parent.Task),
		DryRun:     opts.DryRun,
		Operations: []string{"set_parent", "verify_project_membership", "refresh_refs"},
	}
	if opts.DryRun {
		return result, nil
	}
	if err := s.asana().SetParent(ctx, resolved.Token, target.Task.GID, parent.Task.GID); err != nil {
		return nil, mapAsanaError(err, "Could not move the work item.")
	}
	if target.Type != "task" && !taskInProject(*target.Task, cfg.ActiveProject.GID) {
		if err := s.asana().AddTaskToProject(ctx, resolved.Token, target.Task.GID, cfg.ActiveProject.GID); err != nil {
			return nil, output.NewErrorWithDetails("MOVE_PARTIAL_FAILURE", "Parent was changed but project membership could not be verified. Run work reconcile.", map[string]string{"target_gid": target.Task.GID, "parent_gid": parent.Task.GID, "project_gid": cfg.ActiveProject.GID})
		}
	}
	if err := s.refreshRefsBestEffort(ctx); err == nil {
		result.RefreshedRefs = true
	}
	return result, nil
}

func (s *Service) DependencyList(ctx context.Context, ref string) (*DependencySet, error) {
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveWorkReference(ctx, resolved.Token, ref)
	if err != nil {
		return nil, err
	}
	projectTasks, err := s.allProjectTasks(ctx, resolved.Token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list project work for dependency context.")
	}
	liveByGID := map[string]asana.Task{}
	for _, task := range projectTasks {
		liveByGID[task.GID] = task
	}
	blockers := make([]DependencyRef, 0, len(target.Task.Dependencies))
	var unresolved []DependencyRef
	for _, dep := range target.Task.Dependencies {
		if task, ok := liveByGID[dep.GID]; ok {
			item := toWorkItem(task, cfg.TaskTypes)
			blockers = append(blockers, dependencyRef(&resolvedWorkReference{Task: &task, Ref: refForTask(item, &task), Type: item.Type}))
			continue
		}
		if entry, err := s.refs().Resolve(dep.GID); err == nil {
			blockers = append(blockers, DependencyRef{GID: entry.GID, Ref: entry.Ref, Name: entry.Name, Type: entry.Type, Status: entry.Status, Permalink: entry.Permalink})
			continue
		}
		task, err := s.asana().Task(ctx, resolved.Token, dep.GID)
		if err != nil {
			unresolved = append(unresolved, DependencyRef{GID: dep.GID, Ref: dep.GID, Name: dep.Name, Type: "unknown"})
			continue
		}
		item := toWorkItem(*task, cfg.TaskTypes)
		blockers = append(blockers, dependencyRef(&resolvedWorkReference{Task: task, Ref: refForTask(item, task), Type: item.Type}))
	}
	dependents, err := s.directDependents(ctx, resolved.Token, target.Task.GID)
	if err != nil {
		return nil, err
	}
	sortDependencyRefs(blockers)
	sortDependencyRefs(dependents)
	sortDependencyRefs(unresolved)
	return &DependencySet{Blockers: blockers, DirectDependents: dependents, UnresolvedBlockers: unresolved}, nil
}

func (s *Service) CommentWork(ctx context.Context, opts CommentWorkOptions) (*CommentWorkResult, error) {
	opts.Body = strings.TrimSpace(opts.Body)
	if opts.Body == "" {
		return nil, output.NewError("COMMENT_BODY_REQUIRED", "Provide a non-empty comment body.")
	}
	resolved, _, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.resolveWorkReference(ctx, resolved.Token, opts.Ref)
	if err != nil {
		return nil, err
	}
	result := &CommentWorkResult{Target: dependencyRef(target), Body: opts.Body, DryRun: opts.DryRun}
	if opts.DryRun {
		return result, nil
	}
	story, err := s.asana().AddStory(ctx, resolved.Token, target.Task.GID, opts.Body)
	if err != nil {
		return nil, mapAsanaError(err, "Could not append an Asana story comment.")
	}
	result.Story = &CommentStoryValue{GID: story.GID, CreatedAt: story.CreatedAt, Author: userValue(story.CreatedBy)}
	return result, nil
}

func (s *Service) ReconcileWork(ctx context.Context, opts ReconcileOptions) (*ReconcileResult, error) {
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Ref) != "" {
		if _, err := s.resolveWorkReference(ctx, resolved.Token, opts.Ref); err != nil {
			return nil, err
		}
	}
	result, err := s.reconcileCache(ctx, resolved.Token, cfg)
	if err != nil {
		return nil, err
	}
	result.Scope = "work"
	result.DryRun = opts.DryRun || !opts.Apply
	if opts.Apply && !opts.DryRun {
		for _, item := range result.Observed.MissingProjectMemberships {
			if err := s.asana().AddTaskToProject(ctx, resolved.Token, item.GID, cfg.ActiveProject.GID); err != nil {
				return nil, output.NewErrorWithDetails("RECONCILE_PARTIAL_FAILURE", "Could not apply all safe reconciliation operations.", item)
			}
		}
		if _, err := s.RefreshRefs(ctx, RefreshRefsOptions{Limit: 100}); err != nil {
			return nil, err
		}
		result.Applied = true
		result.DryRun = false
		result.Diagnostics = append(result.Diagnostics, "reference cache refreshed")
	}
	return result, nil
}

func (s *Service) ReconcileContext(ctx context.Context, opts ReconcileOptions) (*ReconcileResult, error) {
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.reconcileCache(ctx, resolved.Token, cfg)
	if err != nil {
		return nil, err
	}
	result.Scope = "context"
	result.DryRun = opts.DryRun || !opts.Apply
	if opts.Apply && !opts.DryRun {
		if _, err := s.RefreshRefs(ctx, RefreshRefsOptions{Limit: 100}); err != nil {
			return nil, err
		}
		result.Applied = true
		result.DryRun = false
		result.Diagnostics = append(result.Diagnostics, "reference cache refreshed")
	}
	return result, nil
}

func (s *Service) resolveActive(ctx context.Context) (*struct{ Token string }, *config.File, error) {
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, nil, err
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if cfg == nil || cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, nil, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured. Run project select first.")
	}
	return &struct{ Token string }{Token: resolved.Token}, cfg, nil
}

func (s *Service) resolveWorkReferenceWithCache(ctx context.Context, token string, ref string, taskTypes config.TaskTypes) (*resolvedWorkReference, *refcache.Entry, error) {
	if looksLikeGID(ref) {
		workRef, err := s.resolveWorkReference(ctx, token, ref)
		return workRef, nil, err
	}
	entry, err := s.refs().Resolve(ref)
	if err != nil {
		workRef, resolveErr := s.resolveWorkReference(ctx, token, ref)
		return workRef, nil, resolveErr
	}
	task, err := s.asana().Task(ctx, token, entry.GID)
	if err != nil {
		var apiErr *asana.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			cached := entryCopy(entry)
			return nil, &cached, output.NewErrorWithDetails("STALE_REFERENCE", "The cached reference no longer resolves in Asana. Run refs refresh or work reconcile.", entry)
		}
		cached := entryCopy(entry)
		return nil, &cached, mapAsanaError(err, "Could not validate cached reference.")
	}
	item := toWorkItem(*task, taskTypes)
	cached := entryCopy(entry)
	return &resolvedWorkReference{Task: task, Ref: entry.Ref, Type: item.Type}, &cached, nil
}

func (s *Service) workDetail(task asana.Task, cfg *config.File, ref string, projectTasks []asana.Task) WorkDetail {
	item := toWorkItem(task, cfg.TaskTypes)
	if ref != "" {
		item.Ref = ref
	}
	project := ProjectMembership{GID: cfg.ActiveProject.GID, Name: cfg.ActiveProject.Name, Member: taskInProject(task, cfg.ActiveProject.GID)}
	detail := WorkDetail{
		GID:          task.GID,
		Ref:          item.Ref,
		Name:         task.Name,
		Type:         item.Type,
		Status:       item.Status,
		Parent:       item.Parent,
		Project:      project,
		Assignee:     userValue(task.Assignee),
		DueOn:        task.DueOn,
		NotesSummary: notesSummary(task.Notes),
		Fields:       fieldValues(task.CustomFields),
		Dependencies: s.dependencySetForTask(task, cfg, projectTasks),
		Permalink:    task.Permalink,
	}
	if task.HTMLNotes != "" {
		if markdown, lossless, err := richtext.MarkdownFromHTML(task.HTMLNotes); err == nil {
			detail.Description = &richtext.Description{Format: "markdown", Content: markdown}
			detail.DescriptionLossless = lossless
		}
	}
	return detail
}

func (s *Service) dependencySetForTask(task asana.Task, cfg *config.File, projectTasks []asana.Task) DependencySet {
	byGID := map[string]asana.Task{}
	for _, candidate := range projectTasks {
		byGID[candidate.GID] = candidate
	}
	refs := refIndex(s.refs())
	var blockers []DependencyRef
	var unresolved []DependencyRef
	for _, dep := range task.Dependencies {
		if blocker, ok := byGID[dep.GID]; ok {
			item := toWorkItem(blocker, cfg.TaskTypes)
			blockers = append(blockers, DependencyRef{GID: blocker.GID, Ref: item.Ref, Name: blocker.Name, Type: item.Type, Status: item.Status, Permalink: blocker.Permalink})
			continue
		}
		ref := dependencyRefFromSummary(dep.GID, dep.Name, refs)
		unresolved = append(unresolved, ref)
	}
	var dependents []DependencyRef
	for _, candidate := range projectTasks {
		if hasDependency(&candidate, task.GID) {
			item := toWorkItem(candidate, cfg.TaskTypes)
			dependents = append(dependents, DependencyRef{GID: candidate.GID, Ref: item.Ref, Name: candidate.Name, Type: item.Type, Status: item.Status, Permalink: candidate.Permalink})
		}
	}
	sortDependencyRefs(blockers)
	sortDependencyRefs(dependents)
	sortDependencyRefs(unresolved)
	return DependencySet{Blockers: blockers, DirectDependents: dependents, UnresolvedBlockers: unresolved}
}

func (s *Service) directDependents(ctx context.Context, token string, gid string) ([]DependencyRef, error) {
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	tasks, err := s.allProjectTasks(ctx, token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list direct dependents.")
	}
	var out []DependencyRef
	for _, task := range tasks {
		if !hasDependency(&task, gid) {
			continue
		}
		item := toWorkItem(task, cfg.TaskTypes)
		out = append(out, DependencyRef{GID: task.GID, Ref: item.Ref, Name: task.Name, Type: item.Type, Status: item.Status, Permalink: task.Permalink})
	}
	sortDependencyRefs(out)
	return out, nil
}

func (s *Service) resolveUser(ctx context.Context, token, workspaceGID, ref string) (*asana.User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, output.NewError("USER_REQUIRED", "Provide an Asana user GID or exact email.")
	}
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
		return nil, output.NewErrorWithCandidates("AMBIGUOUS_USER", "Multiple accessible Asana users matched the supplied email.", matches)
	}
	return &matches[0], nil
}

func (s *Service) resolveEnumFieldValue(ctx context.Context, token string, cfg *config.File, fieldGID string, value string, missingCode string) (asana.EnumOption, error) {
	value = strings.TrimSpace(value)
	if fieldGID == "" {
		return asana.EnumOption{}, output.NewError(missingCode, "The selected project does not configure that field mapping.")
	}
	if value == "" {
		return asana.EnumOption{}, output.NewError("FIELD_VALUE_REQUIRED", "Field value cannot be empty.")
	}
	settings, err := s.asana().CustomFieldSettingsForProject(ctx, token, cfg.ActiveProject.GID)
	if err != nil {
		return asana.EnumOption{}, mapAsanaError(err, "Could not inspect project field options.")
	}
	for _, setting := range settings {
		field := setting.CustomField
		if field.GID != fieldGID {
			continue
		}
		var matches []asana.EnumOption
		for _, option := range field.EnumOptions {
			if !option.Enabled {
				continue
			}
			if option.GID == value || strings.EqualFold(option.Name, value) {
				matches = append(matches, option)
			}
		}
		if len(matches) == 0 {
			return asana.EnumOption{}, output.NewError("INVALID_FIELD_VALUE", "No enabled enum option matched the supplied value.")
		}
		if len(matches) > 1 {
			return asana.EnumOption{}, output.NewErrorWithCandidates("AMBIGUOUS_FIELD_VALUE", "Multiple enabled enum options matched the supplied value.", matches)
		}
		return matches[0], nil
	}
	return asana.EnumOption{}, output.NewError("FIELD_NOT_ATTACHED", "The configured field is not attached to the selected project.")
}

func (s *Service) reconcileCache(ctx context.Context, token string, cfg *config.File) (*ReconcileResult, error) {
	cache, err := s.refs().Load()
	if errors.Is(err, refcache.ErrProjectMismatch) {
		return &ReconcileResult{
			Observed: ReconcileObserved{ProjectGID: cfg.ActiveProject.GID, CacheProjectGID: "different"},
			Desired:  ReconcileDesired{ProjectGID: cfg.ActiveProject.GID, CacheFresh: true},
			Operations: []ReconcileOperation{{
				Kind:        "refresh_refs",
				Description: "Replace the cross-project reference cache with entries from the selected project.",
				Safe:        true,
			}},
		}, nil
	}
	if err != nil {
		return nil, output.NewError("REF_CACHE_READ_FAILED", "Could not read local reference cache.")
	}
	if cache == nil {
		cache = &refcache.Cache{}
	}
	tasks, err := s.allProjectTasks(ctx, token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not inspect live project work.")
	}
	cacheByGID := map[string]refcache.Entry{}
	for _, entry := range cache.Items {
		cacheByGID[entry.GID] = entry
	}
	liveByGID := map[string]asana.Task{}
	for _, task := range tasks {
		liveByGID[task.GID] = task
	}
	var missing []DependencyRef
	var typeMismatches []TypeMismatch
	for _, task := range tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		entry, ok := cacheByGID[task.GID]
		if !ok {
			missing = append(missing, DependencyRef{GID: task.GID, Ref: item.Ref, Name: task.Name, Type: item.Type, Status: item.Status, Permalink: task.Permalink})
			continue
		}
		if entry.Type != "" && item.Type != "unknown" && entry.Type != item.Type {
			typeMismatches = append(typeMismatches, TypeMismatch{
				GID:         task.GID,
				Ref:         entry.Ref,
				Name:        task.Name,
				Intended:    entry.Type,
				Observed:    item.Type,
				Remediation: "Inspect the configured work-type field and update it manually or with an explicit work update.",
			})
		}
	}
	var stale []refcache.Entry
	for _, entry := range cache.Items {
		if _, ok := liveByGID[entry.GID]; !ok {
			stale = append(stale, entry)
		}
	}
	missingMemberships, err := s.firstLevelChildrenMissingProject(ctx, token, cfg, tasks)
	if err != nil {
		return nil, err
	}
	sortDependencyRefs(missing)
	sortDependencyRefs(missingMemberships)
	sort.SliceStable(stale, func(i, j int) bool { return stale[i].Ref < stale[j].Ref })
	sort.SliceStable(typeMismatches, func(i, j int) bool { return typeMismatches[i].Ref < typeMismatches[j].Ref })
	result := &ReconcileResult{
		Observed: ReconcileObserved{
			ProjectGID:                cfg.ActiveProject.GID,
			CacheProjectGID:           cache.ProjectGID,
			MissingCacheEntries:       missing,
			StaleCacheEntries:         stale,
			MissingProjectMemberships: missingMemberships,
			TypeMismatches:            typeMismatches,
		},
		Desired: ReconcileDesired{ProjectGID: cfg.ActiveProject.GID, CacheFresh: len(missing) == 0 && len(stale) == 0 && len(typeMismatches) == 0},
	}
	if len(missing) > 0 || len(stale) > 0 || cache.ProjectGID != "" && cache.ProjectGID != cfg.ActiveProject.GID {
		result.Operations = append(result.Operations, ReconcileOperation{Kind: "refresh_refs", Description: "Refresh the project-scoped reference cache from live Asana data.", Safe: true})
	}
	if len(missingMemberships) > 0 {
		result.Operations = append(result.Operations, ReconcileOperation{Kind: "add_project_membership", Description: "Add first-level epic children back to the selected project.", Safe: true})
	}
	if len(typeMismatches) > 0 {
		result.Operations = append(result.Operations, ReconcileOperation{Kind: "manual_type_repair", Description: "Type mismatches are reported but require explicit operator direction.", Safe: false})
	}
	return result, nil
}

func (s *Service) firstLevelChildrenMissingProject(ctx context.Context, token string, cfg *config.File, tasks []asana.Task) ([]DependencyRef, error) {
	var missing []DependencyRef
	seen := map[string]bool{}
	for _, task := range tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		if item.Type != "epic" {
			continue
		}
		children, err := s.allSubtasks(ctx, token, task.GID)
		if err != nil {
			return nil, mapAsanaError(err, "Could not inspect first-level epic children.")
		}
		for _, child := range children {
			if seen[child.GID] || taskInProject(child, cfg.ActiveProject.GID) {
				continue
			}
			seen[child.GID] = true
			childItem := toWorkItem(child, cfg.TaskTypes)
			missing = append(missing, DependencyRef{GID: child.GID, Ref: childItem.Ref, Name: child.Name, Type: childItem.Type, Status: childItem.Status, Permalink: child.Permalink})
		}
	}
	return missing, nil
}

func propertiesForTask(task asana.Task, cfg *config.File) WorkProperties {
	return WorkProperties{
		Name:      task.Name,
		Notes:     task.Notes,
		HTMLNotes: task.HTMLNotes,
		Assignee:  userValue(task.Assignee),
		DueOn:     task.DueOn,
		Priority:  fieldDisplayValue(task.CustomFields, cfg.Fields.PriorityGID),
		Component: fieldDisplayValue(task.CustomFields, cfg.Fields.ComponentGID),
		Completed: task.Completed,
		ParentGID: parentGID(taskParentFromSummary(task.Parent)),
	}
}

func addChange(changes *[]PropertyChange, property string, before, after any) {
	if before == after {
		return
	}
	*changes = append(*changes, PropertyChange{Property: property, Before: before, After: after})
}

func ensureCustomFields(fields map[string]string) map[string]string {
	if fields == nil {
		return map[string]string{}
	}
	return fields
}

func normalizeDueOn(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", output.NewError("DUE_ON_REQUIRED", "Due date cannot be empty.")
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", output.NewError("INVALID_DUE_ON", "Due date must use YYYY-MM-DD format.")
	}
	return value, nil
}

func validateMove(target, parent *resolvedWorkReference) error {
	switch target.Type {
	case "story", "bug", "spike":
		if parent.Type != "epic" {
			return output.NewError("INVALID_PARENT_TYPE", "Stories, bugs, and spikes can only move under epics.")
		}
	case "task":
		if parent.Type != "story" && parent.Type != "bug" && parent.Type != "spike" {
			return output.NewError("INVALID_PARENT_TYPE", "Implementation tasks can only move under stories, bugs, or spikes.")
		}
	default:
		return output.NewError("UNSUPPORTED_WORK_TYPE", "Only story, bug, spike, and implementation-task work can be moved.")
	}
	return nil
}

func sameParent(task *asana.Task, parentGID string) bool {
	return task != nil && task.Parent != nil && task.Parent.GID == parentGID
}

func taskParentFromSummary(parent *asana.TaskParent) *TaskParent {
	if parent == nil {
		return nil
	}
	return &TaskParent{GID: parent.GID, Ref: "TASK:" + parent.Name, Name: parent.Name}
}

func toTaskParentValue(task *asana.Task) TaskParent {
	if task == nil {
		return TaskParent{}
	}
	return TaskParent{GID: task.GID, Ref: "TASK:" + task.Name, Name: task.Name, Permalink: task.Permalink}
}

func projectGIDForTask(task asana.Task) string {
	if len(task.Projects) == 0 {
		return ""
	}
	return task.Projects[0].GID
}

func taskInProject(task asana.Task, projectGID string) bool {
	if projectGID == "" {
		return false
	}
	for _, project := range task.Projects {
		if project.GID == projectGID {
			return true
		}
	}
	return false
}

func userValue(user *asana.User) *UserValue {
	if user == nil {
		return nil
	}
	return &UserValue{GID: user.GID, Name: user.Name, Email: user.Email}
}

func userGID(user *UserValue) string {
	if user == nil {
		return ""
	}
	return user.GID
}

func fieldValues(fields []asana.CustomField) []FieldValue {
	out := make([]FieldValue, 0, len(fields))
	for _, field := range fields {
		value := FieldValue{GID: field.GID, Name: field.Name, DisplayValue: field.DisplayValue}
		if field.EnumValue != nil {
			value.EnumGID = field.EnumValue.GID
			value.EnumName = field.EnumValue.Name
		}
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func fieldDisplayValue(fields []asana.CustomField, gid string) string {
	if gid == "" {
		return ""
	}
	for _, field := range fields {
		if field.GID == gid {
			return field.DisplayValue
		}
	}
	return ""
}

func notesSummary(notes string) string {
	notes = strings.TrimSpace(notes)
	runes := []rune(notes)
	if len(runes) <= 240 {
		return notes
	}
	return strings.TrimSpace(string(runes[:240]))
}

func cacheSource(entry *refcache.Entry) string {
	if entry == nil {
		return "none"
	}
	return "validated"
}

func nextCommandsForWork(detail WorkDetail) []string {
	if detail.Status == "completed" {
		return []string{"dharana work reopen " + detail.Ref + " --dry-run --json"}
	}
	if len(detail.Dependencies.Blockers) > 0 || len(detail.Dependencies.UnresolvedBlockers) > 0 {
		return []string{"dharana dependency list " + detail.Ref + " --json"}
	}
	return []string{"dharana work update " + detail.Ref + " --dry-run --json", "dharana work complete " + detail.Ref + " --dry-run --json"}
}

func sortDependencyRefs(values []DependencyRef) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Type == values[j].Type {
			if values[i].Name == values[j].Name {
				return values[i].GID < values[j].GID
			}
			return values[i].Name < values[j].Name
		}
		return typeOrder(values[i].Type) < typeOrder(values[j].Type)
	})
}

func (s *Service) refreshRefsBestEffort(ctx context.Context) error {
	_, err := s.RefreshRefs(ctx, RefreshRefsOptions{Limit: 100})
	return err
}

func entryCopy(entry *refcache.Entry) refcache.Entry {
	if entry == nil {
		return refcache.Entry{}
	}
	return *entry
}
