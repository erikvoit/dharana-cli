package plan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/richtext"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type testConfigStore struct{ cfg *config.File }

func (s testConfigStore) Load() (*config.File, error) { return s.cfg, nil }

type fakePlanBackend struct {
	objects              map[string]RemoteObject
	next                 int
	failKind             string
	failBaselineReadOnce bool
	mutationLog          []string
}

func newFakePlanBackend() *fakePlanBackend {
	return &fakePlanBackend{objects: map[string]RemoteObject{}, next: 100}
}

func (f *fakePlanBackend) WorkTree(_ context.Context, opts work.WorkTreeOptions) (*work.WorkTreeResult, error) {
	result := &work.WorkTreeResult{Project: work.ProjectSummary{GID: "project-1", Name: "Payments", WorkspaceGID: "workspace-1", WorkspaceName: "Acme"}}
	byParent := map[string][]RemoteObject{}
	for _, value := range f.objects {
		byParent[value.ParentGID] = append(byParent[value.ParentGID], value)
	}
	for key := range byParent {
		sort.SliceStable(byParent[key], func(i, j int) bool { return byParent[key][i].Name < byParent[key][j].Name })
	}
	var build func(RemoteObject) work.TreeNode
	build = func(value RemoteObject) work.TreeNode {
		item := work.WorkItem{GID: value.GID, Ref: strings.ToUpper(value.Type) + ":" + value.Name, Name: value.Name, Type: value.Type, Status: "incomplete", State: value.Properties.State}
		if value.Completed {
			item.Status = "completed"
		}
		if value.ParentGID != "" {
			item.Parent = &work.TaskParent{GID: value.ParentGID}
		}
		node := work.TreeNode{Item: item}
		for _, child := range byParent[value.GID] {
			node.Children = append(node.Children, build(child))
		}
		return node
	}
	for _, epic := range byParent[""] {
		if epic.Type != "epic" {
			continue
		}
		if opts.EpicRef != "" && opts.EpicRef != epic.GID && opts.EpicRef != epic.Name && opts.EpicRef != "EPIC:"+epic.Name {
			continue
		}
		result.Epics = append(result.Epics, build(epic))
	}
	return result, nil
}

func (f *fakePlanBackend) GetWork(_ context.Context, ref string) (*work.GetWorkResult, error) {
	value, ok := f.objects[ref]
	if !ok {
		return nil, errors.New("work not found")
	}
	detail := work.WorkDetail{GID: value.GID, Ref: value.Ref, Name: value.Name, Type: value.Type, Status: "incomplete", State: value.Properties.State, Dependencies: work.DependencySet{Blockers: []work.DependencyRef{}}}
	if value.Completed {
		detail.Status = "completed"
	}
	for _, blockerGID := range value.Dependencies {
		blocker := f.objects[blockerGID]
		detail.Dependencies.Blockers = append(detail.Dependencies.Blockers, work.DependencyRef{GID: blocker.GID, Ref: blocker.Ref, Name: blocker.Name, Type: blocker.Type})
	}
	return &work.GetWorkResult{Item: detail, Authority: work.Authority{RemoteValidated: true, Source: "fake"}}, nil
}

func (f *fakePlanBackend) UpdateWork(_ context.Context, opts work.UpdateWorkOptions) (*work.UpdateWorkResult, error) {
	if opts.DryRun && f.failBaselineReadOnce {
		f.failBaselineReadOnce = false
		return nil, errors.New("injected baseline read failure")
	}
	value, ok := f.objects[opts.Ref]
	if !ok {
		return nil, errors.New("work not found")
	}
	before := value.Properties
	before.Name = value.Name
	before.Completed = value.Completed
	before.ParentGID = value.ParentGID
	after := before
	if opts.Name != nil {
		after.Name = *opts.Name
	}
	if opts.Notes != nil {
		after.Notes = *opts.Notes
	}
	if opts.Description != nil {
		after.HTMLNotes, _ = richtext.RenderMarkdown(opts.Description.Content)
		after.Notes, _ = richtext.PlainTextFromHTML(after.HTMLNotes)
	}
	if opts.Assignee != nil {
		after.Assignee = &work.UserValue{GID: *opts.Assignee, Name: *opts.Assignee, Email: *opts.Assignee}
	}
	if opts.ClearAssignee {
		after.Assignee = nil
	}
	if opts.DueOn != nil {
		after.DueOn = *opts.DueOn
	}
	if opts.ClearDueOn {
		after.DueOn = ""
	}
	if opts.Priority != nil {
		after.Priority = *opts.Priority
	}
	if opts.Component != nil {
		after.Component = *opts.Component
	}
	if !opts.DryRun {
		if f.failKind == "update" {
			return nil, errors.New("injected update failure")
		}
		value.Name = after.Name
		value.Properties = after
		f.objects[value.GID] = value
		f.mutationLog = append(f.mutationLog, "update:"+value.GID)
	}
	return &work.UpdateWorkResult{Target: work.DependencyRef{GID: value.GID, Name: value.Name, Type: value.Type}, Before: before, After: after, DryRun: opts.DryRun, Noop: reflect.DeepEqual(before, after)}, nil
}

func (f *fakePlanBackend) ValidateProperties(_ context.Context, opts work.ValidatePropertiesOptions) (*work.ValidatePropertiesResult, error) {
	result := &work.ValidatePropertiesResult{}
	if opts.Assignee != nil && *opts.Assignee == "missing@example.com" {
		return nil, errors.New("user not found")
	}
	return result, nil
}

func (f *fakePlanBackend) CreateEpic(_ context.Context, opts work.CreateEpicOptions) (*work.CreateEpicResult, error) {
	value := f.create("epic", opts.Name, "", opts.Notes)
	f.setDescription(value.GID, opts.Description)
	return &work.CreateEpicResult{Epic: work.EpicValue{GID: value.GID, Name: value.Name, Created: true}}, nil
}

func (f *fakePlanBackend) CreateStory(_ context.Context, opts work.CreateStoryOptions) (*work.CreateStoryResult, error) {
	value := f.create("story", opts.Name, opts.EpicRef, opts.Notes)
	f.setDescription(value.GID, opts.Description)
	return &work.CreateStoryResult{Story: work.StoryValue{GID: value.GID, Name: value.Name, Created: true}}, nil
}

func (f *fakePlanBackend) CreateBug(_ context.Context, opts work.CreateBugOptions) (*work.CreateBugResult, error) {
	value := f.create("bug", opts.Name, opts.EpicRef, opts.Notes)
	f.setDescription(value.GID, opts.Description)
	return &work.CreateBugResult{Bug: work.BugValue{GID: value.GID, Name: value.Name, Created: true}}, nil
}

func (f *fakePlanBackend) CreateSpike(_ context.Context, opts work.CreateSpikeOptions) (*work.CreateSpikeResult, error) {
	node := Node{Type: "spike", Notes: stringPointer(opts.Notes), Timebox: optionalStringPointer(opts.Timebox)}
	notes := opts.Notes
	if value := effectiveNotes(node); value != nil {
		notes = *value
	}
	value := f.create("spike", opts.Name, opts.EpicRef, notes)
	richNode := Node{Type: "spike", Description: opts.Description, Timebox: optionalStringPointer(opts.Timebox)}
	f.setDescription(value.GID, effectiveDescription(richNode))
	return &work.CreateSpikeResult{Spike: work.SpikeValue{GID: value.GID, Name: value.Name, Created: true}}, nil
}

func (f *fakePlanBackend) CreateImplementationTask(_ context.Context, opts work.CreateTaskOptions) (*work.CreateTaskResult, error) {
	node := Node{Type: "task", Notes: stringPointer(opts.Notes), Estimate: optionalStringPointer(opts.Estimate)}
	notes := opts.Notes
	if value := effectiveNotes(node); value != nil {
		notes = *value
	}
	value := f.create("task", opts.Name, opts.ParentRef, notes)
	richNode := Node{Type: "task", Description: opts.Description, Estimate: optionalStringPointer(opts.Estimate)}
	f.setDescription(value.GID, effectiveDescription(richNode))
	return &work.CreateTaskResult{Task: work.ImplementationTaskValue{GID: value.GID, Name: value.Name, Created: true}}, nil
}

func (f *fakePlanBackend) CompleteWork(_ context.Context, opts work.CompleteWorkOptions) (*work.CompleteWorkResult, error) {
	value := f.objects[opts.Ref]
	value.Completed = !opts.Reopen
	value.Properties.Completed = value.Completed
	f.objects[value.GID] = value
	f.mutationLog = append(f.mutationLog, "complete:"+value.GID)
	return &work.CompleteWorkResult{Target: work.DependencyRef{GID: value.GID}, AfterCompleted: value.Completed}, nil
}

func (f *fakePlanBackend) TransitionWork(_ context.Context, opts work.TransitionWorkOptions) (*work.TransitionWorkResult, error) {
	value := f.objects[opts.Ref]
	value.Properties.State = opts.To
	value.Completed = opts.To == "done" || opts.To == "canceled"
	value.Properties.Completed = value.Completed
	f.objects[value.GID] = value
	f.mutationLog = append(f.mutationLog, "transition:"+value.GID+":"+opts.To)
	return &work.TransitionWorkResult{Target: work.DependencyRef{GID: value.GID}, AfterState: opts.To, AfterCompleted: value.Completed}, nil
}

func (f *fakePlanBackend) MoveWork(_ context.Context, opts work.MoveWorkOptions) (*work.MoveWorkResult, error) {
	value := f.objects[opts.Ref]
	value.ParentGID = opts.ParentRef
	value.Properties.ParentGID = opts.ParentRef
	f.objects[value.GID] = value
	f.mutationLog = append(f.mutationLog, "move:"+value.GID)
	return &work.MoveWorkResult{Target: work.DependencyRef{GID: value.GID}}, nil
}

func (f *fakePlanBackend) AddDependency(_ context.Context, opts work.AddDependencyOptions) (*work.AddDependencyResult, error) {
	if f.failKind == "add_dependency" {
		return nil, errors.New("injected dependency failure")
	}
	value := f.objects[opts.BlockedRef]
	for _, existing := range value.Dependencies {
		if existing == opts.BlockedByRef {
			return &work.AddDependencyResult{IdempotentExisting: true}, nil
		}
	}
	value.Dependencies = append(value.Dependencies, opts.BlockedByRef)
	sort.Strings(value.Dependencies)
	f.objects[value.GID] = value
	f.mutationLog = append(f.mutationLog, "dependency:"+value.GID)
	return &work.AddDependencyResult{Added: true}, nil
}

func (f *fakePlanBackend) RemoveDependency(_ context.Context, opts work.RemoveDependencyOptions) (*work.RemoveDependencyResult, error) {
	value := f.objects[opts.BlockedRef]
	var kept []string
	for _, existing := range value.Dependencies {
		if existing != opts.BlockedByRef {
			kept = append(kept, existing)
		}
	}
	value.Dependencies = kept
	f.objects[value.GID] = value
	f.mutationLog = append(f.mutationLog, "remove-dependency:"+value.GID)
	return &work.RemoveDependencyResult{Found: true, Removed: true}, nil
}

func (f *fakePlanBackend) create(kind, name, parent, notes string) RemoteObject {
	f.next++
	gid := "gid-" + itoa(f.next)
	value := RemoteObject{GID: gid, Ref: strings.ToUpper(kind) + ":" + name, Type: kind, Name: name, ParentGID: parent, Properties: work.WorkProperties{Name: name, Notes: notes, ParentGID: parent}}
	f.objects[gid] = value
	f.mutationLog = append(f.mutationLog, "create:"+kind+":"+gid)
	return value
}

func (f *fakePlanBackend) setDescription(gid string, description *richtext.Description) {
	if description == nil {
		return
	}
	value := f.objects[gid]
	value.Properties.HTMLNotes, _ = richtext.RenderMarkdown(description.Content)
	value.Properties.Notes, _ = richtext.PlainTextFromHTML(value.Properties.HTMLNotes)
	f.objects[gid] = value
}

func testManifest() *Manifest {
	notes := "Investigate the failure."
	priority := "P1"
	completed := false
	return &Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{ID: "payment-recovery", Context: "payments"},
		Spec: Spec{
			Epic: Epic{ID: "epic", Name: "Payment recovery"},
			Work: []Work{
				{ID: "diagnose", Type: "spike", Name: "Diagnose failure", Notes: &notes, Completed: &completed},
				{ID: "fix-state", Type: "bug", Name: "Fix state", Priority: &priority, BlockedBy: []string{"diagnose"}, Tasks: []Task{{ID: "write-test", Name: "Write regression test"}}},
			},
			RemovalPolicy: "preserve",
		},
	}
}

func testService(t *testing.T, backend *fakePlanBackend) *Service {
	t.Helper()
	cfg := &config.File{
		ActiveProject: &config.ProjectConfig{GID: "project-1", Name: "Payments", WorkspaceGID: "workspace-1", WorkspaceName: "Acme"},
		ActiveContext: "payments",
		Contexts:      []config.Context{{Name: "payments", Project: config.ProjectConfig{GID: "project-1", Name: "Payments", WorkspaceGID: "workspace-1", WorkspaceName: "Acme"}}},
		TaskTypes:     config.TaskTypes{FieldGID: "type-field", Epic: "epic-option", Story: "story-option", Bug: "bug-option", Spike: "spike-option"},
		Fields:        config.FieldMappings{PriorityGID: "priority-field", ComponentGID: "component-field"},
	}
	return &Service{Work: backend, Config: testConfigStore{cfg: cfg}, Bindings: &BindingStore{Path: filepath.Join(t.TempDir(), "bindings.json")}}
}

func TestParseYAMLAndJSONHaveEquivalentSemantics(t *testing.T) {
	yamlPlan := []byte("apiVersion: dharana.dev/v1alpha1\nkind: EpicPlan\nmetadata:\n  id: example\n  context: payments\nspec:\n  epic:\n    id: epic\n    name: Example\n")
	jsonPlan := []byte(`{"apiVersion":"dharana.dev/v1alpha1","kind":"EpicPlan","metadata":{"id":"example","context":"payments"},"spec":{"epic":{"id":"epic","name":"Example"}}}`)
	a, err := Parse(yamlPlan)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(jsonPlan)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() != b.Digest() || !reflect.DeepEqual(a, b) {
		t.Fatalf("expected equivalent plans\nyaml=%#v\njson=%#v", a, b)
	}
	if _, err := Parse([]byte("apiVersion: dharana.dev/v1alpha1\nkind: EpicPlan\nunknown: true\n")); err == nil {
		t.Fatal("expected unknown fields to be rejected")
	}
}

func TestValidateLocalReportsGraphAndFormatErrors(t *testing.T) {
	manifest := testManifest()
	manifest.Spec.Work[0].DueOn = stringPointer("07/17/2026")
	manifest.Spec.Work[0].Timebox = stringPointer("four hours")
	manifest.Spec.Work[0].BlockedBy = []string{"fix-state"}
	manifest.Spec.Work[1].BlockedBy = []string{"diagnose", "missing"}
	manifest.Spec.Work[1].Tasks[0].ID = "diagnose"
	result := ValidateLocal(manifest)
	if result.Valid {
		t.Fatal("expected invalid manifest")
	}
	codes := map[string]bool{}
	for _, finding := range result.LocalFindings {
		codes[finding.Code] = true
	}
	for _, expected := range []string{"INVALID_DUE_ON", "INVALID_TIMEBOX", "DUPLICATE_LOGICAL_ID", "DEPENDENCY_TARGET_NOT_FOUND", "DEPENDENCY_CYCLE"} {
		if !codes[expected] {
			t.Fatalf("expected finding %s, got %#v", expected, result.LocalFindings)
		}
	}
}

func TestValidateLocalRejectsInvalidOrAmbiguousState(t *testing.T) {
	manifest := testManifest()
	manifest.Spec.Work[0].State = stringPointer("doing")
	result := ValidateLocal(manifest)
	if result.Valid || !hasLocalFinding(result.LocalFindings, "INVALID_WORK_STATE") {
		t.Fatalf("expected invalid state finding, got %#v", result.LocalFindings)
	}

	manifest.Spec.Work[0].State = stringPointer("selected")
	result = ValidateLocal(manifest)
	if result.Valid || !hasLocalFinding(result.LocalFindings, "STATE_COMPLETED_CONFLICT") {
		t.Fatalf("expected state/completed conflict, got %#v", result.LocalFindings)
	}
}

func hasLocalFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func TestValidateLocalRejectsUnsafeMarkdownAndNotesConflict(t *testing.T) {
	manifest := testManifest()
	manifest.Spec.Work[1].Description = &richtext.Description{Format: "markdown", Content: "<script>bad</script>"}
	notes := "plain"
	manifest.Spec.Work[1].Notes = &notes
	result := ValidateLocal(manifest)
	codes := map[string]bool{}
	for _, finding := range result.LocalFindings {
		codes[finding.Code] = true
	}
	if !codes["INVALID_MARKDOWN_DESCRIPTION"] || !codes["DESCRIPTION_NOTES_CONFLICT"] {
		t.Fatalf("expected rich description findings, got %#v", result.LocalFindings)
	}
}

func TestBindingStoreRejectsProjectMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	store := &BindingStore{Path: path}
	state := NewBindingState("example", "project-1", "workspace-1")
	state.Bind(Node{ID: "epic", Type: "epic", Name: "Example"}, "gid-1")
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("example", "project-2", "workspace-1"); err == nil {
		t.Fatal("expected project identity mismatch")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("expected private binding permissions, got %v", info.Mode().Perm())
		}
	}
}

func TestBindingStoreSerializesMutationSessions(t *testing.T) {
	store := &BindingStore{Path: filepath.Join(t.TempDir(), "bindings.json")}
	releaseFirst, err := store.Acquire("example", "project-1")
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	go func() {
		releaseSecond, acquireErr := store.Acquire("example", "project-1")
		if acquireErr == nil {
			releaseSecond()
		}
		acquired <- acquireErr
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second session acquired before release: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	releaseFirst()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second session did not acquire the released binding lock")
	}
}

func TestDiffOrdersCreatesBeforeDependencies(t *testing.T) {
	service := testService(t, newFakePlanBackend())
	diff, err := service.Diff(context.Background(), testManifest())
	if err != nil {
		t.Fatal(err)
	}
	if diff.Converged || diff.Conflicted {
		t.Fatalf("unexpected diff state: %#v", diff)
	}
	var kinds []string
	for _, operation := range diff.Operations {
		kinds = append(kinds, operation.Kind+":"+operation.LogicalID)
	}
	expectedPrefix := []string{"create:epic", "create:fix-state", "create:diagnose", "create:write-test"}
	if len(kinds) < len(expectedPrefix) || !reflect.DeepEqual(kinds[:len(expectedPrefix)], expectedPrefix) {
		t.Fatalf("expected hierarchy-safe create order %v, got %v", expectedPrefix, kinds)
	}
	if kinds[len(kinds)-1] != "add_dependency:fix-state" {
		t.Fatalf("expected dependency last, got %v", kinds)
	}
}

func TestApplyConvergesAndSecondApplyIsNoop(t *testing.T) {
	backend := newFakePlanBackend()
	service := testService(t, backend)
	manifest := testManifest()
	result, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err != nil {
		t.Fatalf("apply failed: %v details=%#v", err, result)
	}
	if !result.Converged || result.Partial {
		t.Fatalf("expected converged apply, got %#v", result)
	}
	if len(backend.objects) != 4 {
		t.Fatalf("expected four created objects, got %d", len(backend.objects))
	}
	bindings, err := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings.Objects) != 4 || bindings.ManifestDigest != manifest.Digest() {
		t.Fatalf("unexpected bindings %#v", bindings)
	}
	if len(bindings.Operations) == 0 {
		t.Fatal("expected durable operation outcomes")
	}
	for _, record := range bindings.Operations {
		if record.Status != "succeeded" {
			t.Fatalf("expected successful durable outcome, got %#v", record)
		}
	}
	logLength := len(backend.mutationLog)
	second, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Converged || len(second.Results) != 0 || len(backend.mutationLog) != logLength {
		t.Fatalf("expected idempotent no-op, result=%#v log=%v", second, backend.mutationLog)
	}
	if len(second.Diff.NoopLogicalIDs) != len(manifest.Nodes()) {
		t.Fatalf("expected every managed node to be classified as no-op, got %v", second.Diff.NoopLogicalIDs)
	}
}

func TestApplyTraversesCanonicalPathToDesiredState(t *testing.T) {
	backend := newFakePlanBackend()
	service := testService(t, backend)
	manifest := testManifest()
	manifest.Spec.Work[0].Completed = nil
	manifest.Spec.Work[0].State = stringPointer("done")

	result, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err != nil || !result.Converged {
		t.Fatalf("state apply failed: result=%#v err=%v", result, err)
	}
	bindings, _ := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	value := backend.objects[bindings.Objects["diagnose"].GID]
	if value.Properties.State != "done" || !value.Completed {
		t.Fatalf("desired state did not converge: %#v", value)
	}
	var transitions []string
	for _, entry := range backend.mutationLog {
		if strings.HasPrefix(entry, "transition:"+value.GID+":") {
			transitions = append(transitions, entry)
		}
	}
	if len(transitions) < 2 || transitions[len(transitions)-1] != "transition:"+value.GID+":done" {
		t.Fatalf("expected canonical intermediate transitions, got %v", transitions)
	}
}

func TestMarkdownDescriptionConvergesAndDetectsFormattingDrift(t *testing.T) {
	backend := newFakePlanBackend()
	service := testService(t, backend)
	manifest := testManifest()
	manifest.Spec.Work[1].Description = &richtext.Description{Format: "markdown", Content: "## Acceptance criteria\n\n- **Retry** safely"}
	if _, err := service.Apply(context.Background(), manifest, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	bug := backend.objects[bindings.Objects["fix-state"].GID]
	if !strings.Contains(bug.Properties.HTMLNotes, "<strong>Retry</strong>") {
		t.Fatalf("expected rendered HTML notes, got %q", bug.Properties.HTMLNotes)
	}
	second, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err != nil || !second.Converged {
		t.Fatalf("expected rich description no-op, result=%#v err=%v", second, err)
	}
	bug.Properties.HTMLNotes = "<body><h2>Acceptance criteria</h2>\n<ul><li><em>Retry</em> safely</li></ul></body>"
	backend.objects[bug.GID] = bug
	manifest.Spec.Work[1].Description.Content = "## Acceptance criteria\n\n- `Retry` safely"
	diff, err := service.Diff(context.Background(), manifest)
	if err != nil || !diff.Conflicted {
		t.Fatalf("expected three-way description conflict, diff=%#v err=%v", diff, err)
	}
}

func TestAdoptExactMatchesAndManageBindingsExplicitly(t *testing.T) {
	backend := newFakePlanBackend()
	manifest := testManifest()
	epic := backend.create("epic", manifest.Spec.Epic.Name, "", "")
	backend.create("spike", manifest.Spec.Work[0].Name, epic.GID, *manifest.Spec.Work[0].Notes)
	bug := backend.create("bug", manifest.Spec.Work[1].Name, epic.GID, "")
	task := backend.create("task", manifest.Spec.Work[1].Tasks[0].Name, bug.GID, "")
	service := testService(t, backend)

	dryRun, err := service.Adopt(context.Background(), manifest, AdoptOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dryRun.Bindings) != 4 || dryRun.Applied {
		t.Fatalf("unexpected adoption preview: %#v", dryRun)
	}
	adopted, err := service.Adopt(context.Background(), manifest, AdoptOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.Applied || len(adopted.Bindings) != 4 {
		t.Fatalf("unexpected adoption result: %#v", adopted)
	}
	inspected, err := service.InspectBindings(manifest)
	if err != nil || len(inspected.Bindings) != 4 {
		t.Fatalf("unexpected bindings: %#v err=%v", inspected, err)
	}
	preview, err := service.Unbind(manifest, "write-test", false)
	if err != nil || !preview.DryRun || preview.Applied {
		t.Fatalf("unexpected unbind preview: %#v err=%v", preview, err)
	}
	if _, err := service.Unbind(manifest, "write-test", true); err != nil {
		t.Fatal(err)
	}
	replaced, err := service.ReplaceBinding(context.Background(), manifest, "write-test", task.GID, true)
	if err != nil || !replaced.Applied || replaced.After == nil || replaced.After.GID != task.GID {
		t.Fatalf("unexpected replacement: %#v err=%v", replaced, err)
	}
}

func TestAdoptReportsFuzzyCandidatesWithoutBindingThem(t *testing.T) {
	backend := newFakePlanBackend()
	manifest := testManifest()
	epic := backend.create("epic", manifest.Spec.Epic.Name, "", "")
	backend.create("spike", "Diagnose failures", epic.GID, "")
	service := testService(t, backend)
	result, err := service.Adopt(context.Background(), manifest, AdoptOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].LogicalID != "diagnose" || result.Candidates[0].Kind != "candidate" {
		t.Fatalf("expected one non-binding fuzzy candidate, got %#v", result.Candidates)
	}
	for _, binding := range result.Bindings {
		if binding.LogicalID == "diagnose" {
			t.Fatalf("fuzzy candidate was adopted automatically: %#v", binding)
		}
	}
}

func TestExternalManagedChangeProducesConflict(t *testing.T) {
	backend := newFakePlanBackend()
	service := testService(t, backend)
	manifest := testManifest()
	if _, err := service.Apply(context.Background(), manifest, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	bugBinding := bindings.Objects["fix-state"]
	bug := backend.objects[bugBinding.GID]
	bug.Name = "Human rename"
	bug.Properties.Name = bug.Name
	backend.objects[bug.GID] = bug
	manifest.Spec.Work[1].Name = "Manifest rename"
	diff, err := service.Diff(context.Background(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Conflicted {
		t.Fatalf("expected conflict, got %#v", diff.Operations)
	}
}

func TestPartialApplyPersistsCreatedBindingsForRetry(t *testing.T) {
	backend := newFakePlanBackend()
	backend.failKind = "add_dependency"
	service := testService(t, backend)
	manifest := testManifest()
	result, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err == nil || result == nil || !result.Partial {
		t.Fatalf("expected partial apply, result=%#v err=%v", result, err)
	}
	bindings, loadErr := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(bindings.Objects) != 4 {
		t.Fatalf("expected created object bindings to survive partial failure, got %#v", bindings.Objects)
	}
	objectCount := len(backend.objects)
	backend.failKind = ""
	retry, err := service.Reconcile(context.Background(), manifest, ApplyOptions{})
	if err != nil {
		t.Fatalf("retry failed: %v result=%#v", err, retry)
	}
	if !retry.Converged || len(backend.objects) != objectCount {
		t.Fatalf("expected retry without duplicate objects, result=%#v", retry)
	}
}

func TestCreatePersistsBindingBeforeBaselineRead(t *testing.T) {
	backend := newFakePlanBackend()
	backend.failBaselineReadOnce = true
	service := testService(t, backend)
	manifest := testManifest()
	result, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err == nil || result == nil {
		t.Fatalf("expected partial apply after baseline read failure, result=%#v err=%v", result, err)
	}
	bindings, loadErr := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	created, ok := bindings.Objects["epic"]
	if !ok || created.GID == "" {
		t.Fatalf("expected the successful create GID to be durable, got %#v", bindings.Objects)
	}
	objectCount := len(backend.objects)
	if _, err := service.Reconcile(context.Background(), manifest, ApplyOptions{}); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if len(backend.objects) != objectCount+3 {
		t.Fatalf("expected retry to reuse the epic and create only remaining nodes: before=%d after=%d", objectCount, len(backend.objects))
	}
}

func TestCreatedObjectPropertyFailureRetriesWithoutConflictOrDuplicate(t *testing.T) {
	backend := newFakePlanBackend()
	backend.failKind = "update"
	service := testService(t, backend)
	manifest := testManifest()
	assignee := "developer@example.com"
	manifest.Spec.Work[0].Assignee = &assignee
	result, err := service.Apply(context.Background(), manifest, ApplyOptions{})
	if err == nil || result == nil || !result.Partial {
		t.Fatalf("expected partial apply after property failure, result=%#v err=%v", result, err)
	}
	bindings, loadErr := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(bindings.Objects) == 0 {
		t.Fatal("expected created object binding to be committed before property update")
	}
	objectCount := len(backend.objects)
	backend.failKind = ""
	retry, err := service.Reconcile(context.Background(), manifest, ApplyOptions{})
	if err != nil {
		t.Fatalf("retry failed: %v result=%#v", err, retry)
	}
	if !retry.Converged || len(backend.objects) != 4 || len(backend.objects) < objectCount {
		t.Fatalf("expected converged retry without duplicate objects, result=%#v objects=%d", retry, len(backend.objects))
	}
}

func TestRemovalPolicyCompleteDoesNotDeleteRemoteWork(t *testing.T) {
	backend := newFakePlanBackend()
	service := testService(t, backend)
	manifest := testManifest()
	if _, err := service.Apply(context.Background(), manifest, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := service.Bindings.Load(manifest.Metadata.ID, "project-1", "workspace-1")
	removedGID := bindings.Objects["diagnose"].GID
	manifest.Spec.Work = manifest.Spec.Work[1:]
	manifest.Spec.Work[0].BlockedBy = nil
	manifest.Spec.RemovalPolicy = "complete"
	result, err := service.Reconcile(context.Background(), manifest, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Converged || !backend.objects[removedGID].Completed || len(backend.objects) != 4 {
		t.Fatalf("expected removed managed work to be completed but preserved, result=%#v", result)
	}
}

func TestExportProducesValidManifestAndBindings(t *testing.T) {
	backend := newFakePlanBackend()
	epic := backend.create("epic", "Payment recovery", "", "Epic notes")
	epic.Properties.HTMLNotes, _ = richtext.RenderMarkdown("# Payment recovery\n\nRecover **safely**.")
	backend.objects[epic.GID] = epic
	spike := backend.create("spike", "Investigate retry", epic.GID, "Timebox: 2d\n\nExpected outcomes:\n- Root-cause analysis\n- Technical recommendation\n- Follow-up story or bug, if needed\n\nSpike details")
	task := backend.create("task", "Implement retry", spike.GID, "Estimate: 3h\n\nTask details")
	spike.Dependencies = []string{task.GID}
	backend.objects[spike.GID] = spike
	service := testService(t, backend)
	result, err := service.Export(context.Background(), epic.GID)
	if err != nil {
		t.Fatal(err)
	}
	validation := ValidateLocal(result.Manifest)
	if !validation.Valid {
		t.Fatalf("expected valid exported manifest: %#v", validation.LocalFindings)
	}
	if len(result.Bindings.Objects) != 3 || result.Manifest.Spec.Work[0].Tasks[0].Name != "Implement retry" {
		t.Fatalf("unexpected export %#v", result)
	}
	if result.Manifest.Spec.Work[0].Timebox == nil || *result.Manifest.Spec.Work[0].Timebox != "2d" || result.Manifest.Spec.Work[0].Tasks[0].Estimate == nil || *result.Manifest.Spec.Work[0].Tasks[0].Estimate != "3h" {
		t.Fatalf("expected managed timebox and estimate to round-trip: %#v", result.Manifest.Spec.Work[0])
	}
	if result.Manifest.Spec.Epic.Description == nil || !strings.Contains(result.Manifest.Spec.Epic.Description.Content, "**safely**") || result.Manifest.Spec.Epic.Notes != nil {
		t.Fatalf("expected rich epic description export, got %#v", result.Manifest.Spec.Epic)
	}
	data, err := MarshalYAML(result.Manifest)
	if err != nil || !strings.Contains(string(data), "apiVersion: dharana.dev/v1alpha1") {
		t.Fatalf("unexpected YAML: %s err=%v", data, err)
	}
}
