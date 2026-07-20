package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const SchemaVersion = "2"

type File struct {
	ActiveProject *ProjectConfig `json:"active_project,omitempty"`
	TaskTypes     TaskTypes      `json:"task_types,omitempty"`
	Fields        FieldMappings  `json:"fields,omitempty"`
	Contexts      []Context      `json:"contexts,omitempty"`
	ActiveContext string         `json:"active_context,omitempty"`
	SchemaVersion string         `json:"schema_version,omitempty"`
}

type ProjectConfig struct {
	GID           string `json:"gid"`
	Name          string `json:"name"`
	WorkspaceGID  string `json:"workspace_gid"`
	WorkspaceName string `json:"workspace_name"`
}

type Context struct {
	Name        string        `json:"name"`
	Project     ProjectConfig `json:"project"`
	AuthProfile string        `json:"auth_profile,omitempty"`
	UserGID     string        `json:"user_gid,omitempty"`
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
	if cfg.SchemaVersion == "" {
		cfg.SchemaVersion = "1"
	}
	if cfg.SchemaVersion != "1" && cfg.SchemaVersion != SchemaVersion {
		return nil, errors.New("configuration was written by a newer Dharana version")
	}
	return &cfg, nil
}

func (s *Store) Save(cfg *File) error {
	path := s.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if cfg != nil {
		cfg.SchemaVersion = SchemaVersion
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
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

func (s *Store) path() string {
	if s == nil || s.Path == "" {
		return DefaultPath()
	}
	return s.Path
}

func (cfg *File) UpsertContext(name string, project ProjectConfig) {
	if cfg == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	for i := range cfg.Contexts {
		if cfg.Contexts[i].Name == name {
			cfg.Contexts[i].Project = project
			return
		}
	}
	cfg.Contexts = append(cfg.Contexts, Context{Name: name, Project: project})
}

func (cfg *File) BindContextIdentity(name, profile, userGID string) {
	if cfg == nil {
		return
	}
	for i := range cfg.Contexts {
		if cfg.Contexts[i].Name == name {
			cfg.Contexts[i].AuthProfile = strings.TrimSpace(profile)
			cfg.Contexts[i].UserGID = strings.TrimSpace(userGID)
			return
		}
	}
}

func (cfg *File) ContextByName(name string) (*Context, bool) {
	if cfg == nil {
		return nil, false
	}
	for i := range cfg.Contexts {
		if cfg.Contexts[i].Name == name {
			return &cfg.Contexts[i], true
		}
	}
	return nil, false
}

type OverrideStore struct {
	Base    *Store
	Project *ProjectConfig
}

func (s *OverrideStore) Load() (*File, error) {
	base := s.Base
	if base == nil {
		base = NewStore()
	}
	cfg, err := base.Load()
	if err != nil {
		return nil, err
	}
	if s.Project != nil {
		project := *s.Project
		cfg.ActiveProject = &project
	}
	return cfg, nil
}

func (s *OverrideStore) Save(cfg *File) error {
	base := s.Base
	if base == nil {
		base = NewStore()
	}
	return base.Save(cfg)
}

type RepoContextStore struct {
	Base    *Store
	WorkDir string
}

func (s *RepoContextStore) Load() (*File, error) {
	base := s.Base
	if base == nil {
		base = NewStore()
	}
	cfg, err := base.Load()
	if err != nil {
		return nil, err
	}
	local, err := LoadRepoContext(s.WorkDir)
	if err == nil && local != nil {
		project := local.Project
		cfg.ActiveProject = &project
		cfg.ActiveContext = local.Name
	}
	return cfg, nil
}

func (s *RepoContextStore) Save(cfg *File) error {
	base := s.Base
	if base == nil {
		base = NewStore()
	}
	return base.Save(cfg)
}

func SaveRepoContext(workDir string, contextValue Context) error {
	path := RepoContextPath(workDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(contextValue, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
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

func LoadRepoContext(workDir string) (*Context, error) {
	path := RepoContextPath(workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var contextValue Context
	if err := json.Unmarshal(data, &contextValue); err != nil {
		return nil, err
	}
	return &contextValue, nil
}

func RepoContextPath(workDir string) string {
	if workDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workDir = cwd
		}
	}
	if workDir == "" {
		workDir = "."
	}
	return filepath.Join(workDir, ".dharana", "context.json")
}
