package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type File struct {
	ActiveProject *ProjectConfig `json:"active_project,omitempty"`
	TaskTypes     TaskTypes      `json:"task_types,omitempty"`
	Fields        FieldMappings  `json:"fields,omitempty"`
}

type ProjectConfig struct {
	GID           string `json:"gid"`
	Name          string `json:"name"`
	WorkspaceGID  string `json:"workspace_gid"`
	WorkspaceName string `json:"workspace_name"`
}

type TaskTypes struct {
	FieldGID string `json:"field_gid,omitempty"`
	Epic     string `json:"epic,omitempty"`
	Story    string `json:"story,omitempty"`
	Bug      string `json:"bug,omitempty"`
	Spike    string `json:"spike,omitempty"`
}

type FieldMappings struct {
	PriorityGID  string `json:"priority_gid,omitempty"`
	ComponentGID string `json:"component_gid,omitempty"`
}

type Store struct {
	Path string
}

func NewStore() *Store {
	return &Store{Path: DefaultPath()}
}

func DefaultPath() string {
	return filepath.Join(DefaultDir(), "config.json")
}

func DefaultDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ".dharana"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "dharana")
}

func (s *Store) Load() (*File, error) {
	path := s.path()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg File
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *Store) Save(cfg *File) error {
	path := s.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func (s *Store) path() string {
	if s == nil || s.Path == "" {
		return DefaultPath()
	}
	return s.Path
}
