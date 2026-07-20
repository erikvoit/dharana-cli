package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type AuthResolver interface {
	Resolve(context.Context) (*auth.ResolvedToken, error)
}

type IdentityValidator interface {
	Validate(context.Context) (*auth.ValidateResult, error)
}

type ConfigStore interface{ Load() (*config.File, error) }

type EventClient interface {
	Events(context.Context, string, string, string) (*asana.EventPage, error)
}

type Projection interface {
	RefreshRefs(context.Context, work.RefreshRefsOptions) (*work.RefreshRefsResult, error)
	RefreshChangedRefs(context.Context, []string) (*work.RefreshChangedRefsResult, error)
}

type Service struct {
	Auth       AuthResolver
	Config     ConfigStore
	Events     EventClient
	Projection Projection
	Store      *Store
	Now        func() time.Time
}

func (s *Service) Status(ctx context.Context) (*StatusResult, error) {
	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.store().Load(scope)
	if err != nil {
		if errors.Is(err, ErrUnsupportedSchema) {
			return nil, output.NewError("SYNC_STATE_SCHEMA_UNSUPPORTED", "The synchronization state was written by an incompatible Dharana version.")
		}
		return nil, output.NewError("SYNC_STATE_READ_FAILED", "Could not read incremental synchronization state.")
	}
	return &StatusResult{
		Scope: scope, Configured: state.Cursor != "", CursorState: state.CursorState,
		LastAttemptAt: state.LastAttemptAt, LastSuccessAt: state.LastSuccessAt,
		LastObservedAt: state.LastObservedAt, LastErrorCode: state.LastErrorCode,
		EventsObserved: state.EventsObserved, Rebuilds: state.Rebuilds,
		LagSeconds: lagSeconds(state.LastObservedAt, s.now()),
	}, nil
}

func (s *Service) Reset(ctx context.Context, dryRun bool) (*ResetResult, error) {
	scope, _, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if dryRun {
		state, loadErr := s.store().Load(scope)
		if loadErr != nil {
			return nil, output.NewError("SYNC_STATE_READ_FAILED", "Could not inspect incremental synchronization state.")
		}
		return &ResetResult{Scope: scope, DryRun: true, Reset: state.Cursor != ""}, nil
	}
	release, err := s.store().Acquire(scope)
	if err != nil {
		return nil, output.NewError("SYNC_SCOPE_LOCKED", "Another process is synchronizing this identity and context.")
	}
	defer release()
	reset, err := s.store().Reset(scope)
	if err != nil {
		return nil, output.NewError("SYNC_STATE_WRITE_FAILED", "Could not reset incremental synchronization state.")
	}
	return &ResetResult{Scope: scope, Reset: reset}, nil
}

func (s *Service) Pull(ctx context.Context) (*PullResult, error) {
	scope, token, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	release, err := s.store().Acquire(scope)
	if err != nil {
		return nil, output.NewError("SYNC_SCOPE_LOCKED", "Another process is synchronizing this identity and context.")
	}
	defer release()
	state, err := s.store().Load(scope)
	if err != nil {
		if errors.Is(err, ErrUnsupportedSchema) {
			return nil, output.NewError("SYNC_STATE_SCHEMA_UNSUPPORTED", "The synchronization state was written by an incompatible Dharana version.")
		}
		return nil, output.NewError("SYNC_STATE_READ_FAILED", "Could not read incremental synchronization state.")
	}
	state.LastAttemptAt = s.now().UTC().Format(time.RFC3339)
	result := &PullResult{Scope: scope, CursorState: state.CursorState}
	for pages := 0; pages < 100; pages++ {
		page, eventErr := s.Events.Events(ctx, token, scope.ProjectGID, state.Cursor)
		var apiErr *asana.APIError
		if eventErr != nil && errors.As(eventErr, &apiErr) && apiErr.StatusCode == http.StatusPreconditionFailed {
			if page == nil || page.Sync == "" {
				return nil, s.fail(state, "SYNC_CURSOR_REPLACEMENT_MISSING", "Asana invalidated the synchronization cursor without returning a replacement.")
			}
			rebuilt, rebuildErr := s.Projection.RefreshRefs(ctx, work.RefreshRefsOptions{Limit: 100})
			if rebuildErr != nil {
				return nil, s.failWithCause(state, syncFailureCode(rebuildErr, "SYNC_REBUILD_FAILED"), "The synchronization cursor expired and the bounded projection rebuild failed.", rebuildErr)
			}
			previous := state.Cursor
			state.Cursor = page.Sync
			state.CursorState = "ready"
			state.LastSuccessAt = s.now().UTC().Format(time.RFC3339)
			state.LastErrorCode = ""
			state.Rebuilds++
			if err := s.store().Save(state); err != nil {
				return nil, output.NewError("SYNC_STATE_WRITE_FAILED", "The projection rebuilt, but its replacement cursor could not be committed.")
			}
			result.CursorAdvanced = previous != page.Sync
			result.CursorState = state.CursorState
			result.Rebuilt = true
			result.Warnings = append(result.Warnings, "The event cursor was initialized or expired; Dharana rebuilt the project projection before committing the replacement cursor.")
			for _, item := range rebuilt.Items {
				result.ResourcesRefreshed = append(result.ResourcesRefreshed, item.GID)
			}
			return result, nil
		}
		if eventErr != nil {
			return nil, s.failWithCause(state, syncFailureCode(eventErr, "SYNC_PULL_FAILED"), "Could not retrieve incremental project events from Asana.", eventErr)
		}
		if page == nil || page.Sync == "" {
			return nil, s.fail(state, "SYNC_RESPONSE_INVALID", "Asana returned an unusable incremental event response.")
		}
		records, taskGIDs := normalizeEvents(scope.Context, page.Events, s.now())
		checkpoint := cursorCheckpoint(page.Sync)
		for index := range records {
			records[index].Checkpoint = checkpoint
		}
		if requiresProjectionRebuild(records) {
			rebuilt, rebuildErr := s.Projection.RefreshRefs(ctx, work.RefreshRefsOptions{Limit: 100})
			if rebuildErr != nil {
				return nil, s.failWithCause(state, syncFailureCode(rebuildErr, "SYNC_REBUILD_FAILED"), "A configuration event required a bounded projection rebuild, but the rebuild failed.", rebuildErr)
			}
			result.Rebuilt = true
			result.Warnings = append(result.Warnings, "A project configuration event required a bounded projection rebuild.")
			for _, item := range rebuilt.Items {
				result.ResourcesRefreshed = append(result.ResourcesRefreshed, item.GID)
			}
		} else if len(taskGIDs) > 0 {
			refreshed, refreshErr := s.Projection.RefreshChangedRefs(ctx, taskGIDs)
			if refreshErr != nil {
				return nil, s.failWithCause(state, syncFailureCode(refreshErr, "SYNC_PROJECTION_REFRESH_FAILED"), "Could not verify and refresh changed resources.", refreshErr)
			}
			result.ResourcesRefreshed = append(result.ResourcesRefreshed, refreshed.Refreshed...)
			result.ResourcesRemoved = append(result.ResourcesRemoved, refreshed.Removed...)
		}
		previous := state.Cursor
		state.Cursor = page.Sync
		state.CursorState = "ready"
		state.LastSuccessAt = s.now().UTC().Format(time.RFC3339)
		state.LastErrorCode = ""
		state.EventsObserved += int64(len(records))
		for _, record := range records {
			if record.EventAt > state.LastObservedAt {
				state.LastObservedAt = record.EventAt
			}
		}
		if err := s.store().Save(state); err != nil {
			return nil, output.NewError("SYNC_STATE_WRITE_FAILED", "Events were processed, but the synchronization cursor could not be committed.")
		}
		result.CursorAdvanced = result.CursorAdvanced || previous != page.Sync
		result.Events = append(result.Events, records...)
		result.EventsObserved += len(records)
		result.CursorState = state.CursorState
		if page.HasMore && previous == page.Sync {
			return nil, s.fail(state, "SYNC_CURSOR_STALLED", "Asana reported more events without advancing the synchronization cursor.")
		}
		if !page.HasMore {
			result.LagSeconds = lagSeconds(state.LastObservedAt, s.now())
			sort.Strings(result.ResourcesRefreshed)
			sort.Strings(result.ResourcesRemoved)
			return result, nil
		}
	}
	return nil, s.fail(state, "SYNC_PAGE_LIMIT_EXCEEDED", "The event stream exceeded the bounded 100-page synchronization limit.")
}

func (s *Service) scope(ctx context.Context) (Scope, string, error) {
	if s.Auth == nil || s.Config == nil || s.Events == nil || s.Projection == nil {
		return Scope{}, "", output.NewError("SYNC_NOT_CONFIGURED", "Incremental synchronization services are not configured.")
	}
	resolved, err := s.Auth.Resolve(ctx)
	if err != nil {
		return Scope{}, "", err
	}
	cfg, err := s.Config.Load()
	if err != nil {
		return Scope{}, "", output.NewError("CONFIG_READ_FAILED", "Could not read the effective project context.")
	}
	if cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return Scope{}, "", output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured.")
	}
	effectiveUser := resolved.User
	if effectiveUser == nil {
		if validator, ok := s.Auth.(IdentityValidator); ok {
			validated, validateErr := validator.Validate(ctx)
			if validateErr != nil {
				return Scope{}, "", validateErr
			}
			effectiveUser = &validated.User
		}
	}
	contextName := cfg.ActiveContext
	if contextName == "" {
		contextName = "project:" + cfg.ActiveProject.GID
	}
	if contextValue, ok := cfg.ContextByName(contextName); ok {
		if contextValue.AuthProfile != "" && contextValue.AuthProfile != resolved.Profile {
			return Scope{}, "", output.NewError("AUTH_CONTEXT_MISMATCH", "The effective authentication profile does not own the synchronization context.")
		}
		if contextValue.UserGID != "" && (effectiveUser == nil || effectiveUser.GID != contextValue.UserGID) {
			return Scope{}, "", output.NewError("AUTH_CONTEXT_MISMATCH", "The effective Asana identity does not own the synchronization context.")
		}
	}
	identity := resolved.Profile
	if effectiveUser != nil && effectiveUser.GID != "" {
		identity = effectiveUser.GID
	}
	if identity == "" {
		if contextValue, ok := cfg.ContextByName(contextName); ok && contextValue.UserGID != "" {
			identity = contextValue.UserGID
		} else {
			identity = string(resolved.Provider) + ":" + resolved.Source
		}
	}
	return Scope{Identity: identity, WorkspaceGID: cfg.ActiveProject.WorkspaceGID, ProjectGID: cfg.ActiveProject.GID, Context: contextName}, resolved.Token, nil
}

func (s *Service) fail(state *State, code, message string) error {
	state.CursorState = "degraded"
	state.LastErrorCode = code
	_ = s.store().Save(state)
	return output.NewError(code, message)
}

func (s *Service) failWithCause(state *State, code, message string, cause error) error {
	state.CursorState = "degraded"
	state.LastErrorCode = code
	_ = s.store().Save(state)
	details := map[string]any{}
	var apiErr *asana.APIError
	if errors.As(cause, &apiErr) {
		details["status"] = apiErr.StatusCode
		if apiErr.RequestID != "" {
			details["request_id"] = apiErr.RequestID
		}
		if apiErr.RetryAfter != "" {
			details["retry_after"] = apiErr.RetryAfter
		}
	}
	var appErr *output.AppError
	if errors.As(cause, &appErr) && appErr.Details != nil {
		details["cause"] = appErr.Details
	}
	if len(details) == 0 {
		return output.NewError(code, message)
	}
	return output.NewErrorWithDetails(code, message, details)
}

func syncFailureCode(cause error, fallback string) string {
	var apiErr *asana.APIError
	if errors.As(cause, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized:
			return "SYNC_AUTHENTICATION_REQUIRED"
		case apiErr.StatusCode == http.StatusForbidden:
			return "SYNC_ACCESS_DENIED"
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return "SYNC_RATE_LIMITED"
		case apiErr.StatusCode >= 500:
			return "SYNC_TRANSIENT_FAILURE"
		}
	}
	var appErr *output.AppError
	if errors.As(cause, &appErr) {
		switch appErr.Code {
		case "ASANA_RATE_LIMITED":
			return "SYNC_RATE_LIMITED"
		case "ASANA_TRANSIENT_FAILURE", "ASANA_REQUEST_FAILED":
			return "SYNC_TRANSIENT_FAILURE"
		case "INVALID_AUTH":
			return "SYNC_AUTHENTICATION_REQUIRED"
		case "ASANA_ACCESS_DENIED":
			return "SYNC_ACCESS_DENIED"
		}
	}
	return fallback
}

func (s *Service) store() *Store {
	if s.Store == nil {
		return &Store{Root: config.DefaultDir()}
	}
	return s.Store
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func normalizeEvents(contextName string, events []asana.Event, observed time.Time) ([]EventRecord, []string) {
	records := make([]EventRecord, 0, len(events))
	var taskGIDs []string
	seenTasks := map[string]bool{}
	for _, event := range events {
		typeName := normalizedEventType(event)
		id := event.GID
		if id == "" {
			raw := fmt.Sprintf("%s|%s|%s|%s|%v|%s|%s", event.Resource.GID, event.Resource.ResourceType, event.Action, event.Change.Field, event.Change.NewValue, event.CreatedAt, typeName)
			digest := sha256.Sum256([]byte(raw))
			id = "evt_" + hex.EncodeToString(digest[:12])
		}
		records = append(records, EventRecord{
			SchemaVersion: SchemaVersion, ID: id, Context: contextName,
			ResourceGID: event.Resource.GID, ResourceType: event.Resource.ResourceType,
			ObservedAt: observed.UTC().Format(time.RFC3339), EventAt: event.CreatedAt,
			Type: typeName, Disposition: "processed", Action: event.Action, Field: event.Change.Field,
		})
		if event.Resource.ResourceType == "task" && event.Resource.GID != "" && !seenTasks[event.Resource.GID] {
			seenTasks[event.Resource.GID] = true
			taskGIDs = append(taskGIDs, event.Resource.GID)
		}
	}
	return records, taskGIDs
}

func requiresProjectionRebuild(records []EventRecord) bool {
	for _, record := range records {
		switch record.ResourceType {
		case "custom_field", "custom_field_setting", "project":
			return true
		}
	}
	return false
}

func cursorCheckpoint(cursor string) string {
	digest := sha256.Sum256([]byte(cursor))
	return "cursor_" + hex.EncodeToString(digest[:8])
}

func normalizedEventType(event asana.Event) string {
	resourceType := strings.ToLower(event.Resource.ResourceType)
	action := strings.ToLower(event.Action)
	if resourceType == "task" {
		if event.Change.Field == "completed" {
			if value, ok := event.Change.NewValue.(bool); ok && !value {
				return "work.uncompleted"
			}
			return "work.completed"
		}
		switch action {
		case "added":
			return "work.added"
		case "deleted":
			return "work.deleted"
		case "undeleted":
			return "work.undeleted"
		default:
			return "work.changed"
		}
	}
	if resourceType == "project" {
		return "project.changed"
	}
	return resourceType + "." + action
}
