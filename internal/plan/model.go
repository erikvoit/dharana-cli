package plan

import (
	"context"

	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type WorkBackend interface {
	WorkTree(context.Context, work.WorkTreeOptions) (*work.WorkTreeResult, error)
	GetWork(context.Context, string) (*work.GetWorkResult, error)
	UpdateWork(context.Context, work.UpdateWorkOptions) (*work.UpdateWorkResult, error)
	ValidateProperties(context.Context, work.ValidatePropertiesOptions) (*work.ValidatePropertiesResult, error)
	CreateEpic(context.Context, work.CreateEpicOptions) (*work.CreateEpicResult, error)
	CreateStory(context.Context, work.CreateStoryOptions) (*work.CreateStoryResult, error)
	CreateBug(context.Context, work.CreateBugOptions) (*work.CreateBugResult, error)
	CreateSpike(context.Context, work.CreateSpikeOptions) (*work.CreateSpikeResult, error)
	CreateImplementationTask(context.Context, work.CreateTaskOptions) (*work.CreateTaskResult, error)
	CompleteWork(context.Context, work.CompleteWorkOptions) (*work.CompleteWorkResult, error)
	MoveWork(context.Context, work.MoveWorkOptions) (*work.MoveWorkResult, error)
	AddDependency(context.Context, work.AddDependencyOptions) (*work.AddDependencyResult, error)
	RemoveDependency(context.Context, work.RemoveDependencyOptions) (*work.RemoveDependencyResult, error)
}

type ConfigStore interface {
	Load() (*config.File, error)
}

type Service struct {
	Work     WorkBackend
	Config   ConfigStore
	Bindings *BindingStore
}

type RemoteObject struct {
	GID          string              `json:"gid"`
	Ref          string              `json:"ref,omitempty"`
	Type         string              `json:"type"`
	Name         string              `json:"name"`
	ParentGID    string              `json:"parent_gid,omitempty"`
	Completed    bool                `json:"completed"`
	Properties   work.WorkProperties `json:"properties"`
	Dependencies []string            `json:"dependency_gids,omitempty"`
}

type Snapshot struct {
	Project work.ProjectSummary     `json:"project"`
	Objects map[string]RemoteObject `json:"objects"`
	Issues  []work.TreeIssue        `json:"issues,omitempty"`
}

type Operation struct {
	ID                string         `json:"id"`
	Kind              string         `json:"kind"`
	LogicalID         string         `json:"logical_id"`
	GID               string         `json:"gid,omitempty"`
	Reason            string         `json:"reason"`
	Current           map[string]any `json:"current,omitempty"`
	LastApplied       map[string]any `json:"last_applied,omitempty"`
	Desired           map[string]any `json:"desired,omitempty"`
	Prerequisites     []string       `json:"prerequisites,omitempty"`
	ResolutionOptions []string       `json:"resolution_options,omitempty"`
	Conflict          bool           `json:"conflict,omitempty"`
	Destructive       bool           `json:"destructive,omitempty"`
}

type DiffResult struct {
	ManifestID     string              `json:"manifest_id"`
	ManifestDigest string              `json:"manifest_digest"`
	Project        work.ProjectSummary `json:"project"`
	Validation     ValidationResult    `json:"validation"`
	Operations     []Operation         `json:"operations"`
	NoopLogicalIDs []string            `json:"no_op_logical_ids,omitempty"`
	Unmanaged      []RemoteObject      `json:"unmanaged,omitempty"`
	Converged      bool                `json:"converged"`
	Conflicted     bool                `json:"conflicted"`
	BindingsPath   string              `json:"bindings_path,omitempty"`
}

type OperationResult struct {
	OperationID string `json:"operation_id"`
	LogicalID   string `json:"logical_id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	GID         string `json:"gid,omitempty"`
	Message     string `json:"message,omitempty"`
}

type ApplyOptions struct {
	DryRun bool
}

type ApplyResult struct {
	ManifestID     string            `json:"manifest_id"`
	ManifestDigest string            `json:"manifest_digest"`
	DryRun         bool              `json:"dry_run"`
	Converged      bool              `json:"converged"`
	Partial        bool              `json:"partial"`
	Diff           *DiffResult       `json:"diff"`
	Results        []OperationResult `json:"results"`
	BindingsPath   string            `json:"bindings_path,omitempty"`
}

type StatusResult struct {
	ManifestID     string      `json:"manifest_id"`
	State          string      `json:"state"`
	ManifestDigest string      `json:"manifest_digest"`
	AppliedDigest  string      `json:"applied_digest,omitempty"`
	Diff           *DiffResult `json:"diff"`
	Message        string      `json:"message,omitempty"`
}

type AdoptOptions struct {
	DryRun bool
	Apply  bool
}

type AdoptResult struct {
	ManifestID   string      `json:"manifest_id"`
	DryRun       bool        `json:"dry_run"`
	Applied      bool        `json:"applied"`
	Bindings     []Binding   `json:"bindings"`
	Conflicts    []Operation `json:"conflicts,omitempty"`
	Candidates   []Operation `json:"candidates,omitempty"`
	BindingsPath string      `json:"bindings_path,omitempty"`
}

type ExportResult struct {
	Manifest     *Manifest     `json:"manifest"`
	Bindings     *BindingState `json:"bindings"`
	Findings     []Finding     `json:"findings,omitempty"`
	BindingsPath string        `json:"bindings_path,omitempty"`
}
