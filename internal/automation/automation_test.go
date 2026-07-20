package automation

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erikvoit/dharana-cli/internal/syncer"
	"github.com/erikvoit/dharana-cli/internal/work"
)

const validPolicyYAML = `apiVersion: dharana.dev/v1alpha1
kind: AutomationPolicy
metadata:
  id: complete-ready
spec:
  context: payments
  mode: apply
  when:
    event: work.changed
  evaluate:
    query: event.resource
    filters:
      type: [story]
  permissions:
    scopes: [tasks:read, tasks:write]
  failureThreshold: 2
  actions:
    - id: complete
      type: complete
`

func TestPolicyValidationIsVersionedDeterministicAndStrict(t *testing.T) {
	first, err := Parse([]byte(validPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse([]byte(validPolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	result := Validate(first)
	if !result.Valid || first.Version == "" || first.Version != second.Version {
		t.Fatalf("unexpected validation %#v first=%#v second=%#v", result, first, second)
	}
	invalid, err := Parse([]byte(`apiVersion: dharana.dev/v1alpha1
kind: AutomationPolicy
metadata: {id: loop}
spec:
  context: payments
  mode: apply
  when: {event: work.completed}
  actions: [{type: complete}]
`))
	if err != nil {
		t.Fatal(err)
	}
	invalidResult := Validate(invalid)
	if invalidResult.Valid || !hasFinding(invalidResult.Findings, "AUTOMATION_RECURSIVE_ACTION") || !hasFinding(invalidResult.Findings, "AUTOMATION_SCOPE_UNDECLARED") {
		t.Fatalf("expected recursive action and scope findings: %#v", invalidResult)
	}
	untargeted, err := Parse([]byte(strings.Replace(validPolicyYAML, "query: event.resource", "query: work.ready", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if result := Validate(untargeted); result.Valid || !hasFinding(result.Findings, "AUTOMATION_ACTION_TARGET_REQUIRED") {
		t.Fatalf("expected explicit query.matches target finding: %#v", result)
	}
}

type fakeSync struct{ pull *syncer.PullResult }

func (f *fakeSync) Pull(context.Context) (*syncer.PullResult, error) { return f.pull, nil }
func (f *fakeSync) Status(context.Context) (*syncer.StatusResult, error) {
	return &syncer.StatusResult{Scope: syncer.Scope{Context: "payments"}, CursorState: "ready"}, nil
}

type fakeWork struct {
	completeCalls int
	lastDryRun    bool
	readyItems    []work.WorkItem
	completedRefs []string
	transitions   []work.TransitionWorkOptions
}

func (f *fakeWork) ReadyWork(context.Context, work.ReadyWorkOptions) (*work.ReadyWorkResult, error) {
	return &work.ReadyWorkResult{Items: f.readyItems}, nil
}
func (f *fakeWork) GetWork(_ context.Context, ref string) (*work.GetWorkResult, error) {
	return &work.GetWorkResult{Item: work.WorkDetail{GID: ref, Ref: "STORY:Example", Type: "story", Status: "open", State: "in_progress"}}, nil
}
func (f *fakeWork) CommentWork(context.Context, work.CommentWorkOptions) (*work.CommentWorkResult, error) {
	return &work.CommentWorkResult{}, nil
}
func (f *fakeWork) CompleteWork(_ context.Context, opts work.CompleteWorkOptions) (*work.CompleteWorkResult, error) {
	f.completeCalls++
	f.lastDryRun = opts.DryRun
	f.completedRefs = append(f.completedRefs, opts.Ref)
	return &work.CompleteWorkResult{Target: work.DependencyRef{GID: opts.Ref}}, nil
}
func (f *fakeWork) TransitionWork(_ context.Context, opts work.TransitionWorkOptions) (*work.TransitionWorkResult, error) {
	f.transitions = append(f.transitions, opts)
	return &work.TransitionWorkResult{Target: work.DependencyRef{GID: opts.Ref}, AfterState: opts.To, DryRun: opts.DryRun}, nil
}

func TestTransitionPolicyValidatesLoopSafetyAndExecutesCanonicalState(t *testing.T) {
	policy, err := Parse([]byte(`apiVersion: dharana.dev/v1alpha1
kind: AutomationPolicy
metadata: {id: verify-progress}
spec:
  context: payments
  mode: apply
  when: {event: work.changed}
  evaluate:
    query: event.resource
    filters: {state: [in_progress]}
  permissions:
    scopes: [tasks:read, tasks:write]
  actions:
    - {id: verify, type: transition, state: verification}
`))
	if err != nil || !Validate(policy).Valid {
		t.Fatalf("expected valid transition policy: err=%v validation=%#v", err, Validate(policy))
	}
	unsafe := *policy
	unsafe.Spec.Evaluate.Filters = map[string][]string{"state": {"verification"}}
	if result := Validate(&unsafe); result.Valid || !hasFinding(result.Findings, "AUTOMATION_RECURSIVE_ACTION") {
		t.Fatalf("expected recursive transition finding: %#v", result)
	}

	event := syncer.EventRecord{ID: "event-transition", Context: "payments", ResourceGID: "task-1", ResourceType: "task", Type: "work.changed"}
	workService := &fakeWork{}
	root := t.TempDir()
	runtime := &Runtime{Sync: &fakeSync{pull: &syncer.PullResult{Events: []syncer.EventRecord{event}}}, Work: workService, Auth: allowScopes{}, Journal: &Journal{Path: filepath.Join(root, "journal.jsonl")}, LeaseRoot: filepath.Join(root, "leases")}
	result, err := runtime.Run(context.Background(), []*Policy{policy}, RunOptions{Once: true, Apply: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(workService.transitions) != 1 || workService.transitions[0].To != "verification" || len(result.Actions) != 1 || result.Actions[0].State != "verification" {
		t.Fatalf("unexpected transition execution: calls=%#v result=%#v", workService.transitions, result)
	}
}

type allowScopes struct{}

func (allowScopes) RequireScopes(context.Context, []string) error { return nil }

func TestRuntimePreventsDuplicateMutationForDuplicateEvent(t *testing.T) {
	policy, _ := Parse([]byte(validPolicyYAML))
	event := syncer.EventRecord{SchemaVersion: "1", ID: "event-1", Context: "payments", ResourceGID: "task-1", ResourceType: "task", Type: "work.changed", ObservedAt: "2026-07-20T03:00:00Z"}
	syncService := &fakeSync{pull: &syncer.PullResult{EventsObserved: 1, Events: []syncer.EventRecord{event}}}
	workService := &fakeWork{}
	root := t.TempDir()
	runtime := &Runtime{Sync: syncService, Work: workService, Auth: allowScopes{}, Journal: &Journal{Path: filepath.Join(root, "journal.jsonl")}, LeaseRoot: filepath.Join(root, "leases")}
	first, err := runtime.Run(context.Background(), []*Policy{policy}, RunOptions{Once: true, Apply: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Run(context.Background(), []*Policy{policy}, RunOptions{Once: true, Apply: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if workService.completeCalls != 1 || first.Actions[0].Disposition != "succeeded" || second.Actions[0].Disposition != "no-op" {
		t.Fatalf("duplicate action was not suppressed: calls=%d first=%#v second=%#v", workService.completeCalls, first, second)
	}
	explained, err := runtime.Journal.Explain(firstEvaluationID(t, runtime.Journal))
	if err != nil || len(explained.Actions) == 0 {
		t.Fatalf("expected explainable journal, result=%#v err=%v", explained, err)
	}
}

func TestDryRunRechecksActionWithoutApplyAuthorization(t *testing.T) {
	policy, _ := Parse([]byte(validPolicyYAML))
	event := syncer.EventRecord{ID: "event-dry", Context: "payments", ResourceGID: "task-1", ResourceType: "task", Type: "work.changed"}
	workService := &fakeWork{}
	root := t.TempDir()
	runtime := &Runtime{Sync: &fakeSync{pull: &syncer.PullResult{Events: []syncer.EventRecord{event}}}, Work: workService, Auth: allowScopes{}, Journal: &Journal{Path: filepath.Join(root, "journal.jsonl")}, LeaseRoot: filepath.Join(root, "leases")}
	result, err := runtime.Run(context.Background(), []*Policy{policy}, RunOptions{Once: true, DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if workService.completeCalls != 1 || !workService.lastDryRun || result.Actions[0].Disposition != "proposed" {
		t.Fatalf("dry-run did not use authoritative action path: calls=%d result=%#v", workService.completeCalls, result)
	}
}

func TestQueryMatchedMutationExpandsInStableTargetOrder(t *testing.T) {
	policy, err := Parse([]byte(`apiVersion: dharana.dev/v1alpha1
kind: AutomationPolicy
metadata: {id: complete-ready-set}
spec:
  context: payments
  mode: apply
  when: {event: work.changed}
  evaluate: {query: work.ready}
  permissions:
    scopes: [tasks:read, tasks:write]
  actions:
    - {id: complete-match, type: complete, target: query.matches}
`))
	if err != nil || !Validate(policy).Valid {
		t.Fatalf("invalid test policy: %v %#v", err, Validate(policy))
	}
	event := syncer.EventRecord{ID: "event-set", Context: "payments", ResourceGID: "trigger", Type: "work.changed"}
	workService := &fakeWork{readyItems: []work.WorkItem{{GID: "task-2", Status: "open"}, {GID: "task-1", Status: "open"}}}
	root := t.TempDir()
	runtime := &Runtime{Sync: &fakeSync{pull: &syncer.PullResult{Events: []syncer.EventRecord{event}}}, Work: workService, Auth: allowScopes{}, Journal: &Journal{Path: filepath.Join(root, "journal.jsonl")}, LeaseRoot: filepath.Join(root, "leases")}
	result, err := runtime.Run(context.Background(), []*Policy{policy}, RunOptions{Once: true, Apply: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 2 || workService.completedRefs[0] != "task-1" || workService.completedRefs[1] != "task-2" {
		t.Fatalf("query matches were not expanded deterministically: result=%#v refs=%v", result, workService.completedRefs)
	}
}

func TestJournalConcurrentAppendAndRetentionPreserveFailures(t *testing.T) {
	journal := &Journal{Path: filepath.Join(t.TempDir(), "journal.jsonl"), MaxEntries: 5, Now: func() time.Time { return time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC) }}
	var group sync.WaitGroup
	for index := 0; index < 12; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			entry := JournalEntry{ID: "entry-" + strconv.Itoa(index), Kind: "action", EvaluationID: "eval-" + strconv.Itoa(index), Action: &ActionOutcome{IdempotencyKey: "key-" + strconv.Itoa(index), Disposition: "succeeded"}}
			if index == 0 {
				entry.Action.Disposition = "failed"
			}
			if err := journal.Append(entry); err != nil {
				t.Errorf("append %d: %v", index, err)
			}
		}(index)
	}
	group.Wait()
	history, err := journal.History(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) < 5 || !containsEntry(history.Entries, "entry-0") {
		t.Fatalf("retention removed unresolved failure: %#v", history)
	}
}

func TestJournalRepairsIncompleteTrailingRecordBeforeAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	journal := &Journal{Path: path}
	if err := journal.Append(JournalEntry{ID: "first", Kind: "evaluation", Evaluation: &Evaluation{Matched: true}}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"schema_version":"1","id":"partial"`)
	_ = file.Close()
	if history, err := journal.History(10); err != nil || len(history.Entries) != 1 {
		t.Fatalf("concurrent reader did not tolerate trailing partial record: %#v err=%v", history, err)
	}
	if err := journal.Append(JournalEntry{ID: "second", Kind: "evaluation", Evaluation: &Evaluation{Matched: false}}); err != nil {
		t.Fatal(err)
	}
	history, err := journal.History(10)
	if err != nil || len(history.Entries) != 2 || history.Entries[1].ID != "second" {
		t.Fatalf("journal tail was not repaired: %#v err=%v", history, err)
	}
}

func TestStopAndDrainTimerDoesNotBlockAfterChannelWasDrained(t *testing.T) {
	timer := time.NewTimer(time.Millisecond)
	<-timer.C
	done := make(chan struct{})
	go func() {
		stopAndDrainTimer(timer)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopping an already-drained timer blocked")
	}
}

func hasFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func firstEvaluationID(t *testing.T, journal *Journal) string {
	t.Helper()
	history, err := journal.History(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history.Entries {
		if entry.Kind == "evaluation" {
			return entry.ID
		}
	}
	t.Fatal("evaluation not found")
	return ""
}

func containsEntry(entries []JournalEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
