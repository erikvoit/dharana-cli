package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type Item struct {
	Kind              string `json:"kind"`
	Path              string `json:"path"`
	Current           string `json:"current_version,omitempty"`
	Target            string `json:"target_version"`
	MigrationRequired bool   `json:"migration_required"`
	Supported         bool   `json:"supported"`
	BackupPath        string `json:"backup_path,omitempty"`
}

type Result struct {
	Required bool   `json:"required"`
	DryRun   bool   `json:"dry_run"`
	Applied  bool   `json:"applied"`
	Items    []Item `json:"items"`
}
type Service struct {
	ConfigDir string
	Now       func() time.Time
}

func (s *Service) Status() (*Result, error) { return s.inspect(false) }

func (s *Service) Apply(dryRun bool) (*Result, error) {
	result, err := s.inspect(dryRun)
	if err != nil {
		return nil, err
	}
	if dryRun || !result.Required {
		return result, nil
	}
	for i := range result.Items {
		item := &result.Items[i]
		if !item.MigrationRequired {
			continue
		}
		if !item.Supported {
			return result, output.NewErrorWithDetails("MIGRATION_UNSUPPORTED", "Local state requires an unsupported migration.", *item)
		}
		data, err := os.ReadFile(item.Path)
		if err != nil {
			return result, output.NewError("MIGRATION_READ_FAILED", "Could not read local state for migration.")
		}
		backup := item.Path + ".bak." + s.now().UTC().Format("20060102T150405Z")
		if err := os.WriteFile(backup, data, 0o600); err != nil {
			return result, output.NewError("MIGRATION_BACKUP_FAILED", "Could not create a recoverable state backup.")
		}
		var value map[string]any
		if err := json.Unmarshal(data, &value); err != nil {
			return result, output.NewError("MIGRATION_INVALID_STATE", "Local state is not valid JSON.")
		}
		value["schema_version"] = item.Target
		encoded, _ := json.MarshalIndent(value, "", "  ")
		encoded = append(encoded, '\n')
		if err := atomicWrite(item.Path, encoded); err != nil {
			return result, output.NewError("MIGRATION_WRITE_FAILED", "Could not atomically write migrated local state.")
		}
		item.BackupPath = backup
		item.Current = item.Target
		item.MigrationRequired = false
	}
	result.Required = false
	result.Applied = true
	return result, nil
}

func (s *Service) inspect(dryRun bool) (*Result, error) {
	dir := s.dir()
	candidates := []struct{ kind, path, target string }{{"config", filepath.Join(dir, "config.json"), config.SchemaVersion}, {"auth_profiles", filepath.Join(dir, "auth-profiles.json"), auth.ProfileSchemaVersion}, {"reference_cache", filepath.Join(dir, "refs.json"), "1"}}
	bindingPaths, _ := filepath.Glob(filepath.Join(dir, "plans", "*", "*.bindings.json"))
	for _, path := range bindingPaths {
		candidates = append(candidates, struct{ kind, path, target string }{"plan_bindings", path, "1"})
	}
	result := &Result{DryRun: dryRun, Items: []Item{}}
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(data, &header); err != nil {
			return nil, output.NewErrorWithDetails("MIGRATION_INVALID_STATE", "Local state is not valid JSON.", candidate.path)
		}
		current := header.SchemaVersion
		if current == "" {
			current = "1"
		}
		item := Item{Kind: candidate.kind, Path: candidate.path, Current: current, Target: candidate.target, Supported: supportedVersion(current, candidate.target), MigrationRequired: current != candidate.target}
		if item.MigrationRequired {
			result.Required = true
		}
		result.Items = append(result.Items, item)
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].Path < result.Items[j].Path })
	return result, nil
}

func (s *Service) dir() string {
	if s.ConfigDir != "" {
		return s.ConfigDir
	}
	return config.DefaultDir()
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func supportedVersion(current, target string) bool {
	a, errA := strconv.Atoi(current)
	b, errB := strconv.Atoi(target)
	return errA == nil && errB == nil && a <= b
}
