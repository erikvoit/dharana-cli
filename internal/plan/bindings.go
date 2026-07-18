package plan

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/erikvoit/dharana-cli/internal/config"
)

const BindingSchemaVersion = "1"

type BindingState struct {
	SchemaVersion  string                     `json:"schema_version"`
	ManifestID     string                     `json:"manifest_id"`
	Context        string                     `json:"context,omitempty"`
	ManifestDigest string                     `json:"manifest_digest,omitempty"`
	WorkspaceGID   string                     `json:"workspace_gid,omitempty"`
	ProjectGID     string                     `json:"project_gid"`
	LastAppliedAt  string                     `json:"last_applied_at,omitempty"`
	Objects        map[string]Binding         `json:"objects"`
	Operations     map[string]OperationRecord `json:"operations,omitempty"`
}

type Binding struct {
	LogicalID         string       `json:"logical_id"`
	LogicalPath       string       `json:"logical_path,omitempty"`
	GID               string       `json:"gid"`
	Type              string       `json:"type"`
	ParentID          string       `json:"parent_id,omitempty"`
	LastKnownName     string       `json:"last_known_name,omitempty"`
	LastVerifiedAt    string       `json:"last_verified_at,omitempty"`
	ManagedBlockerIDs []string     `json:"managed_blocker_ids,omitempty"`
	LastApplied       AppliedState `json:"last_applied,omitempty"`
}

type OperationRecord struct {
	OperationID string `json:"operation_id"`
	LogicalID   string `json:"logical_id"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	GID         string `json:"gid,omitempty"`
	AttemptedAt string `json:"attempted_at"`
	Message     string `json:"message,omitempty"`
}

type AppliedState struct {
	Name      string  `json:"name,omitempty"`
	Notes     *string `json:"notes,omitempty"`
	HTMLNotes *string `json:"html_notes,omitempty"`
	Assignee  *string `json:"assignee,omitempty"`
	DueOn     *string `json:"due_on,omitempty"`
	Priority  *string `json:"priority,omitempty"`
	Component *string `json:"component,omitempty"`
	Completed *bool   `json:"completed,omitempty"`
	ParentID  string  `json:"parent_id,omitempty"`
}

type BindingStore struct {
	Path string
}

const (
	bindingLockTimeout = 5 * time.Second
	bindingLockStale   = 10 * time.Minute
)

func NewBindingState(manifestID, projectGID, workspaceGID string) *BindingState {
	return &BindingState{
		SchemaVersion: BindingSchemaVersion,
		ManifestID:    manifestID,
		ProjectGID:    projectGID,
		WorkspaceGID:  workspaceGID,
		Objects:       map[string]Binding{},
		Operations:    map[string]OperationRecord{},
	}
}

func DefaultBindingPath(manifestID, projectGID string) string {
	return filepath.Join(config.DefaultDir(), "plans", safePathPart(projectGID), safePathPart(manifestID)+".bindings.json")
}

func (s *BindingStore) Load(manifestID, projectGID, workspaceGID string) (*BindingState, error) {
	path := s.path(manifestID, projectGID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewBindingState(manifestID, projectGID, workspaceGID), nil
	}
	if err != nil {
		return nil, err
	}
	var state BindingState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.SchemaVersion != BindingSchemaVersion {
		return nil, errors.New("unsupported binding schema version")
	}
	if state.ManifestID != manifestID {
		return nil, errors.New("binding manifest identity mismatch")
	}
	if state.ProjectGID != projectGID {
		return nil, errors.New("binding project identity mismatch")
	}
	if workspaceGID != "" && state.WorkspaceGID != "" && state.WorkspaceGID != workspaceGID {
		return nil, errors.New("binding workspace identity mismatch")
	}
	if state.Objects == nil {
		state.Objects = map[string]Binding{}
	}
	if state.Operations == nil {
		state.Operations = map[string]OperationRecord{}
	}
	return &state, nil
}

func (s *BindingStore) Save(state *BindingState) error {
	if state == nil {
		return errors.New("binding state is nil")
	}
	state.SchemaVersion = BindingSchemaVersion
	if state.Objects == nil {
		state.Objects = map[string]Binding{}
	}
	if state.Operations == nil {
		state.Operations = map[string]OperationRecord{}
	}
	path := s.path(state.ManifestID, state.ProjectGID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Acquire serializes read-modify-write binding sessions across CLI processes.
// The binding file itself remains atomically replaceable and readable while a
// live apply owns the adjacent lock directory.
func (s *BindingStore) Acquire(manifestID, projectGID string) (func(), error) {
	path := s.path(manifestID, projectGID) + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(bindingLockTimeout)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			var once sync.Once
			return func() { once.Do(func() { _ = os.Remove(path) }) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > bindingLockStale {
			if removeErr := os.Remove(path); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the plan binding lock")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (s *BindingStore) path(manifestID, projectGID string) string {
	if s != nil && strings.TrimSpace(s.Path) != "" {
		return s.Path
	}
	return DefaultBindingPath(manifestID, projectGID)
}

func (state *BindingState) Bind(node Node, gid string) {
	if state.Objects == nil {
		state.Objects = map[string]Binding{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	state.Objects[node.ID] = Binding{
		LogicalID: node.ID, GID: gid, Type: node.Type, ParentID: node.ParentID,
		LogicalPath:   state.logicalPath(node),
		LastKnownName: node.Name, LastVerifiedAt: now,
		ManagedBlockerIDs: append([]string(nil), node.BlockedBy...),
		LastApplied:       appliedState(node),
	}
}

func (state *BindingState) RecordOperation(operation Operation, status, gid, message string) {
	if state.Operations == nil {
		state.Operations = map[string]OperationRecord{}
	}
	state.Operations[operation.ID] = OperationRecord{
		OperationID: operation.ID, LogicalID: operation.LogicalID, Kind: operation.Kind,
		Status: status, GID: gid, AttemptedAt: time.Now().UTC().Format(time.RFC3339), Message: message,
	}
}

func (state *BindingState) SortedOperationRecords() []OperationRecord {
	values := make([]OperationRecord, 0, len(state.Operations))
	for _, value := range state.Operations {
		values = append(values, value)
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].OperationID < values[j].OperationID })
	return values
}

func (state *BindingState) logicalPath(node Node) string {
	if node.ParentID == "" {
		return node.ID
	}
	if parent, ok := state.Objects[node.ParentID]; ok && parent.LogicalPath != "" {
		return parent.LogicalPath + "/" + node.ID
	}
	return node.ParentID + "/" + node.ID
}

func (state *BindingState) SortedBindings() []Binding {
	values := make([]Binding, 0, len(state.Objects))
	for _, value := range state.Objects {
		values = append(values, value)
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].LogicalID < values[j].LogicalID })
	return values
}

func appliedState(node Node) AppliedState {
	return AppliedState{
		Name: node.Name, Notes: effectiveNotes(node), HTMLNotes: effectiveHTMLNotes(node), Assignee: node.Assignee, DueOn: node.DueOn,
		Priority: node.Priority, Component: node.Component, Completed: node.Completed,
		ParentID: node.ParentID,
	}
}

func safePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}
