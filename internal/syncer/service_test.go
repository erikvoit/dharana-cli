package syncer

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type fakeAuth struct{ resolved *auth.ResolvedToken }

func (f fakeAuth) Resolve(context.Context) (*auth.ResolvedToken, error) { return f.resolved, nil }

type fakeConfig struct{ file *config.File }

func (f fakeConfig) Load() (*config.File, error) { return f.file, nil }

type eventResponse struct {
	page *asana.EventPage
	err  error
}

type fakeEvents struct {
	responses []eventResponse
	cursors   []string
}

func (f *fakeEvents) Events(_ context.Context, _, _, cursor string) (*asana.EventPage, error) {
	f.cursors = append(f.cursors, cursor)
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response.page, response.err
}

type fakeProjection struct {
	fullCalls    int
	changedCalls [][]string
	changedErr   error
}

func (f *fakeProjection) RefreshRefs(context.Context, work.RefreshRefsOptions) (*work.RefreshRefsResult, error) {
	f.fullCalls++
	return &work.RefreshRefsResult{Items: []refcache.Entry{{GID: "t1"}}}, nil
}

func (f *fakeProjection) RefreshChangedRefs(_ context.Context, gids []string) (*work.RefreshChangedRefsResult, error) {
	f.changedCalls = append(f.changedCalls, append([]string(nil), gids...))
	if f.changedErr != nil {
		return nil, f.changedErr
	}
	return &work.RefreshChangedRefsResult{Refreshed: append([]string(nil), gids...)}, nil
}

func testService(root string, events *fakeEvents, projection *fakeProjection) *Service {
	return &Service{
		Auth:       fakeAuth{resolved: &auth.ResolvedToken{Token: "token", Profile: "automation", Provider: auth.ProviderPAT}},
		Config:     fakeConfig{file: &config.File{ActiveContext: "payments", ActiveProject: &config.ProjectConfig{GID: "p1", WorkspaceGID: "w1"}}},
		Events:     events,
		Projection: projection,
		Store:      &Store{Root: root},
		Now:        func() time.Time { return time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC) },
	}
}

func TestPullRebuildsBeforeCommittingReplacementCursor(t *testing.T) {
	events := &fakeEvents{responses: []eventResponse{{page: &asana.EventPage{Sync: "cursor-1"}, err: &asana.APIError{StatusCode: http.StatusPreconditionFailed}}}}
	projection := &fakeProjection{}
	service := testService(t.TempDir(), events, projection)
	result, err := service.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Rebuilt || projection.fullCalls != 1 || !result.CursorAdvanced {
		t.Fatalf("unexpected rebuild result %#v calls=%d", result, projection.fullCalls)
	}
	status, err := service.Status(context.Background())
	if err != nil || status.CursorState != "ready" || status.Rebuilds != 1 {
		t.Fatalf("unexpected status %#v err=%v", status, err)
	}
}

func TestPullDoesNotAdvanceCursorPastFailedProjection(t *testing.T) {
	root := t.TempDir()
	events := &fakeEvents{}
	projection := &fakeProjection{changedErr: errors.New("projection failed")}
	service := testService(root, events, projection)
	scope, _, _ := service.scope(context.Background())
	if err := service.Store.Save(&State{SchemaVersion: SchemaVersion, Scope: scope, Cursor: "cursor-old", CursorState: "ready"}); err != nil {
		t.Fatal(err)
	}
	events.responses = []eventResponse{{
		page: &asana.EventPage{Sync: "cursor-new", Events: []asana.Event{{
			GID: "e1", Type: "task", Action: "changed",
			Resource: asana.EventResource{GID: "t1", ResourceType: "task"},
		}}},
	}}
	if _, err := service.Pull(context.Background()); err == nil {
		t.Fatal("expected projection failure")
	}
	state, err := service.Store.Load(scope)
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "cursor-old" || state.LastErrorCode != "SYNC_PROJECTION_REFRESH_FAILED" {
		t.Fatalf("cursor advanced past failed projection: %#v", state)
	}
}

func TestPullNormalizesEventsAndAdvancesEachProcessedPage(t *testing.T) {
	root := t.TempDir()
	events := &fakeEvents{}
	projection := &fakeProjection{}
	service := testService(root, events, projection)
	scope, _, _ := service.scope(context.Background())
	_ = service.Store.Save(&State{SchemaVersion: SchemaVersion, Scope: scope, Cursor: "cursor-0", CursorState: "ready"})
	events.responses = []eventResponse{
		{page: &asana.EventPage{Sync: "cursor-1", HasMore: true, Events: []asana.Event{{GID: "e1", Action: "changed", CreatedAt: "2026-07-20T02:59:00Z", Resource: asana.EventResource{GID: "t1", ResourceType: "task"}, Change: asana.EventChange{Field: "completed", NewValue: true}}}}},
		{page: &asana.EventPage{Sync: "cursor-2", Events: []asana.Event{{GID: "e2", Action: "changed", CreatedAt: "2026-07-20T02:59:30Z", Resource: asana.EventResource{GID: "t2", ResourceType: "task"}}}}},
	}
	result, err := service.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.EventsObserved != 2 || result.Events[0].Type != "work.completed" || len(projection.changedCalls) != 2 {
		t.Fatalf("unexpected incremental result %#v changed=%v", result, projection.changedCalls)
	}
	state, _ := service.Store.Load(scope)
	if state.Cursor != "cursor-2" || state.EventsObserved != 2 {
		t.Fatalf("unexpected committed state %#v", state)
	}
}

func TestConfigurationEventTriggersBoundedRebuildBeforeCursorCommit(t *testing.T) {
	root := t.TempDir()
	events := &fakeEvents{}
	projection := &fakeProjection{}
	service := testService(root, events, projection)
	scope, _, _ := service.scope(context.Background())
	_ = service.Store.Save(&State{SchemaVersion: SchemaVersion, Scope: scope, Cursor: "cursor-0", CursorState: "ready"})
	events.responses = []eventResponse{{page: &asana.EventPage{Sync: "cursor-1", Events: []asana.Event{{
		GID: "e-config", Action: "changed", Resource: asana.EventResource{GID: "field-1", ResourceType: "custom_field"},
	}}}}}
	result, err := service.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, _ := service.Store.Load(scope)
	if !result.Rebuilt || projection.fullCalls != 1 || state.Cursor != "cursor-1" {
		t.Fatalf("configuration rebuild did not commit safely: result=%#v state=%#v", result, state)
	}
}

func TestRateLimitRemainsDistinctInPersistedHealth(t *testing.T) {
	root := t.TempDir()
	events := &fakeEvents{}
	service := testService(root, events, &fakeProjection{})
	scope, _, _ := service.scope(context.Background())
	_ = service.Store.Save(&State{SchemaVersion: SchemaVersion, Scope: scope, Cursor: "cursor-0", CursorState: "ready"})
	events.responses = []eventResponse{{err: &asana.APIError{StatusCode: http.StatusTooManyRequests, RetryAfter: "30", RequestID: "request-1"}}}
	_, err := service.Pull(context.Background())
	var appErr *output.AppError
	if !errors.As(err, &appErr) || appErr.Code != "SYNC_RATE_LIMITED" {
		t.Fatalf("expected distinct rate-limit error, got %v", err)
	}
	state, _ := service.Store.Load(scope)
	if state.LastErrorCode != "SYNC_RATE_LIMITED" || state.Cursor != "cursor-0" {
		t.Fatalf("unexpected rate-limit state %#v", state)
	}
}
