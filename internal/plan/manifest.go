package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/richtext"
	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "dharana.dev/v1alpha1"
	Kind       = "EpicPlan"
)

type Manifest struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       Spec     `json:"spec" yaml:"spec"`
}

type Metadata struct {
	ID      string `json:"id" yaml:"id"`
	Context string `json:"context,omitempty" yaml:"context,omitempty"`
}

type Spec struct {
	Project       string `json:"project,omitempty" yaml:"project,omitempty"`
	Epic          Epic   `json:"epic" yaml:"epic"`
	Work          []Work `json:"work,omitempty" yaml:"work,omitempty"`
	RemovalPolicy string `json:"removalPolicy,omitempty" yaml:"removalPolicy,omitempty"`
}

type Epic struct {
	ID          string                `json:"id" yaml:"id"`
	Name        string                `json:"name" yaml:"name"`
	Notes       *string               `json:"notes,omitempty" yaml:"notes,omitempty"`
	Description *richtext.Description `json:"description,omitempty" yaml:"description,omitempty"`
}

type Work struct {
	ID          string                `json:"id" yaml:"id"`
	Type        string                `json:"type" yaml:"type"`
	Name        string                `json:"name" yaml:"name"`
	Notes       *string               `json:"notes,omitempty" yaml:"notes,omitempty"`
	Description *richtext.Description `json:"description,omitempty" yaml:"description,omitempty"`
	Assignee    *string               `json:"assignee,omitempty" yaml:"assignee,omitempty"`
	DueOn       *string               `json:"dueOn,omitempty" yaml:"dueOn,omitempty"`
	Priority    *string               `json:"priority,omitempty" yaml:"priority,omitempty"`
	Component   *string               `json:"component,omitempty" yaml:"component,omitempty"`
	Timebox     *string               `json:"timebox,omitempty" yaml:"timebox,omitempty"`
	Completed   *bool                 `json:"completed,omitempty" yaml:"completed,omitempty"`
	State       *string               `json:"state,omitempty" yaml:"state,omitempty"`
	BlockedBy   []string              `json:"blockedBy,omitempty" yaml:"blockedBy,omitempty"`
	Tasks       []Task                `json:"tasks,omitempty" yaml:"tasks,omitempty"`
}

type Task struct {
	ID          string                `json:"id" yaml:"id"`
	Name        string                `json:"name" yaml:"name"`
	Notes       *string               `json:"notes,omitempty" yaml:"notes,omitempty"`
	Description *richtext.Description `json:"description,omitempty" yaml:"description,omitempty"`
	Assignee    *string               `json:"assignee,omitempty" yaml:"assignee,omitempty"`
	DueOn       *string               `json:"dueOn,omitempty" yaml:"dueOn,omitempty"`
	Estimate    *string               `json:"estimate,omitempty" yaml:"estimate,omitempty"`
	Completed   *bool                 `json:"completed,omitempty" yaml:"completed,omitempty"`
	State       *string               `json:"state,omitempty" yaml:"state,omitempty"`
	BlockedBy   []string              `json:"blockedBy,omitempty" yaml:"blockedBy,omitempty"`
}

type Node struct {
	ID          string
	Type        string
	Name        string
	ParentID    string
	Notes       *string
	Description *richtext.Description
	Assignee    *string
	DueOn       *string
	Priority    *string
	Component   *string
	Timebox     *string
	Estimate    *string
	Completed   *bool
	State       *string
	BlockedBy   []string
}

func ParseFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func Parse(data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode plan manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode plan manifest: %w", err)
	} else if err == nil {
		return nil, errors.New("decode plan manifest: multiple YAML documents are not supported")
	}
	manifest.Normalize()
	return &manifest, nil
}

func MarshalYAML(manifest *Manifest) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("plan manifest is nil")
	}
	copyValue := *manifest
	copyValue.Normalize()
	return yaml.Marshal(&copyValue)
}

func (m *Manifest) Normalize() {
	if m == nil {
		return
	}
	m.APIVersion = strings.TrimSpace(m.APIVersion)
	m.Kind = strings.TrimSpace(m.Kind)
	m.Metadata.ID = strings.TrimSpace(m.Metadata.ID)
	m.Metadata.Context = strings.TrimSpace(m.Metadata.Context)
	m.Spec.Project = strings.TrimSpace(m.Spec.Project)
	m.Spec.RemovalPolicy = strings.ToLower(strings.TrimSpace(m.Spec.RemovalPolicy))
	if m.Spec.RemovalPolicy == "" {
		m.Spec.RemovalPolicy = "preserve"
	}
	m.Spec.Epic.ID = strings.TrimSpace(m.Spec.Epic.ID)
	m.Spec.Epic.Name = strings.TrimSpace(m.Spec.Epic.Name)
	normalizeDescription(m.Spec.Epic.Description)
	for i := range m.Spec.Work {
		item := &m.Spec.Work[i]
		item.ID = strings.TrimSpace(item.ID)
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Name = strings.TrimSpace(item.Name)
		normalizeDescription(item.Description)
		item.BlockedBy = normalizedIDs(item.BlockedBy)
		for j := range item.Tasks {
			task := &item.Tasks[j]
			task.ID = strings.TrimSpace(task.ID)
			task.Name = strings.TrimSpace(task.Name)
			normalizeDescription(task.Description)
			task.BlockedBy = normalizedIDs(task.BlockedBy)
		}
	}
}

func normalizeDescription(value *richtext.Description) {
	if value == nil {
		return
	}
	value.Format = strings.ToLower(strings.TrimSpace(value.Format))
	value.Content = strings.ReplaceAll(value.Content, "\r\n", "\n")
}

func (m *Manifest) Nodes() []Node {
	if m == nil {
		return nil
	}
	nodes := []Node{{
		ID: m.Spec.Epic.ID, Type: "epic", Name: m.Spec.Epic.Name, Notes: m.Spec.Epic.Notes, Description: m.Spec.Epic.Description,
	}}
	for _, item := range m.Spec.Work {
		nodes = append(nodes, Node{
			ID: item.ID, Type: item.Type, Name: item.Name, ParentID: m.Spec.Epic.ID,
			Notes: item.Notes, Description: item.Description, Assignee: item.Assignee, DueOn: item.DueOn, Priority: item.Priority,
			Component: item.Component, Timebox: item.Timebox, Completed: item.Completed, State: item.State,
			BlockedBy: append([]string(nil), item.BlockedBy...),
		})
		for _, task := range item.Tasks {
			nodes = append(nodes, Node{
				ID: task.ID, Type: "task", Name: task.Name, ParentID: item.ID,
				Notes: task.Notes, Description: task.Description, Assignee: task.Assignee, DueOn: task.DueOn,
				Estimate: task.Estimate, Completed: task.Completed, State: task.State,
				BlockedBy: append([]string(nil), task.BlockedBy...),
			})
		}
	}
	return nodes
}

func (m *Manifest) Digest() string {
	data, err := MarshalYAML(m)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizedIDs(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
