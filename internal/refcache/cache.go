package refcache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/config"
)

type Cache struct {
	SchemaVersion string  `json:"schema_version,omitempty"`
	ProjectGID    string  `json:"project_gid,omitempty"`
	ProjectName   string  `json:"project_name,omitempty"`
	WorkspaceGID  string  `json:"workspace_gid,omitempty"`
	WorkspaceName string  `json:"workspace_name,omitempty"`
	UpdatedAt     string  `json:"updated_at,omitempty"`
	Items         []Entry `json:"items"`
}

type Entry struct {
	Ref       string `json:"ref"`
	GID       string `json:"gid"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status,omitempty"`
	ParentRef string `json:"parent_ref,omitempty"`
	ParentGID string `json:"parent_gid,omitempty"`
	Permalink string `json:"permalink_url,omitempty"`
}

type Store struct {
	Path    string
	Project *config.ProjectConfig
}

func NewStore() *Store {
	return &Store{Path: filepath.Join(config.DefaultDir(), "refs.json")}
}

func (s *Store) Load() (*Cache, error) {
	path := s.path()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Cache{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return &Cache{}, nil
	}
	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache.SchemaVersion == "" {
		cache.SchemaVersion = "1"
	}
	if s.Project != nil && cache.ProjectGID != "" && cache.ProjectGID != s.Project.GID {
		return nil, ErrProjectMismatch
	}
	return &cache, nil
}

func (s *Store) Save(cache *Cache) error {
	path := s.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if cache != nil {
		if cache.SchemaVersion == "" {
			cache.SchemaVersion = "1"
		}
		if s.Project != nil {
			cache.ProjectGID = s.Project.GID
			cache.ProjectName = s.Project.Name
			cache.WorkspaceGID = s.Project.WorkspaceGID
			cache.WorkspaceName = s.Project.WorkspaceName
		}
	}
	data, err := json.MarshalIndent(cache, "", "  ")
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

func (s *Store) Replace(entries []Entry) (*Cache, error) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Ref < entries[j].Ref
	})
	cache := &Cache{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Items:     entries,
	}
	return cache, s.Save(cache)
}

func (s *Store) Resolve(ref string) (*Entry, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrReferenceRequired
	}
	cache, err := s.Load()
	if err != nil {
		return nil, err
	}
	for _, entry := range cache.Items {
		if entry.GID == ref || strings.EqualFold(entry.Ref, ref) {
			item := entry
			return &item, nil
		}
	}
	return nil, ErrReferenceNotFound
}

func (s *Store) path() string {
	if s == nil || s.Path == "" {
		return filepath.Join(config.DefaultDir(), "refs.json")
	}
	return s.Path
}

var ErrReferenceRequired = errors.New("reference required")
var ErrReferenceNotFound = errors.New("reference not found")
var ErrProjectMismatch = errors.New("reference cache belongs to a different project")
