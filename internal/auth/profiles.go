package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const ProfileSchemaVersion = "1"

type Provider string

const (
	ProviderEnvironment Provider = "environment"
	ProviderPAT         Provider = "pat_keychain"
	ProviderOAuth       Provider = "oauth"
)

type Profile struct {
	Name       string         `json:"name"`
	Provider   Provider       `json:"provider"`
	User       ConfiguredUser `json:"user,omitempty"`
	Scopes     []string       `json:"scopes,omitempty"`
	ScopeKnown bool           `json:"scope_known"`
	ExpiresAt  string         `json:"expires_at,omitempty"`
	UpdatedAt  string         `json:"updated_at"`
}

type ProfileState struct {
	SchemaVersion string    `json:"schema_version"`
	Active        string    `json:"active_profile,omitempty"`
	Profiles      []Profile `json:"profiles"`
}

type ProfileStore interface {
	Load() (*ProfileState, error)
	Save(*ProfileState) error
}

type FileProfileStore struct{ Path string }

func NewFileProfileStore() *FileProfileStore {
	return &FileProfileStore{Path: filepath.Join(defaultConfigDir(), "auth-profiles.json")}
}

func (s *FileProfileStore) Load() (*ProfileState, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return &ProfileState{SchemaVersion: ProfileSchemaVersion, Profiles: []Profile{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var state ProfileState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = ProfileSchemaVersion
	}
	if state.SchemaVersion != ProfileSchemaVersion {
		return nil, errors.New("unsupported auth profile schema version " + state.SchemaVersion)
	}
	if state.Profiles == nil {
		state.Profiles = []Profile{}
	}
	return &state, nil
}

func (s *FileProfileStore) Save(state *ProfileState) error {
	if state == nil {
		return errors.New("auth profile state is required")
	}
	state.SchemaVersion = ProfileSchemaVersion
	sort.SliceStable(state.Profiles, func(i, j int) bool { return state.Profiles[i].Name < state.Profiles[j].Name })
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := s.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *FileProfileStore) path() string {
	if s != nil && strings.TrimSpace(s.Path) != "" {
		return s.Path
	}
	return filepath.Join(defaultConfigDir(), "auth-profiles.json")
}

func (s *ProfileState) Profile(name string) (*Profile, bool) {
	for i := range s.Profiles {
		if s.Profiles[i].Name == name {
			return &s.Profiles[i], true
		}
	}
	return nil, false
}

func (s *ProfileState) Upsert(profile Profile) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	profile.Scopes = normalizeScopes(profile.Scopes)
	for i := range s.Profiles {
		if s.Profiles[i].Name == profile.Name {
			s.Profiles[i] = profile
			return
		}
	}
	s.Profiles = append(s.Profiles, profile)
}

func (s *ProfileState) Remove(name string) bool {
	for i := range s.Profiles {
		if s.Profiles[i].Name == name {
			s.Profiles = append(s.Profiles[:i], s.Profiles[i+1:]...)
			if s.Active == name {
				s.Active = ""
			}
			return true
		}
	}
	return false
}

func defaultConfigDir() string {
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		return filepath.Join(base, "dharana")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".dharana"
	}
	return filepath.Join(home, ".config", "dharana")
}

func normalizeScopes(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, scope := range strings.Fields(value) {
			if !seen[scope] {
				seen[scope] = true
				out = append(out, scope)
			}
		}
	}
	sort.Strings(out)
	return out
}

func DefaultScopes() []string {
	return []string{"custom_fields:read", "custom_fields:write", "projects:read", "projects:write", "stories:read", "stories:write", "tasks:read", "tasks:write", "users:read", "workspaces:read"}
}
