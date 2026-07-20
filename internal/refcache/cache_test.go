package refcache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/config"
)

func TestLoadEmptyCacheFileReturnsEmptyCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refs.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	cache, err := (&Store{Path: path}).Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cache == nil || len(cache.Items) != 0 {
		t.Fatalf("expected empty cache, got %#v", cache)
	}
}

func TestLoadRejectsDifferentProjectCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refs.json")
	writer := &Store{Path: path, Project: &config.ProjectConfig{GID: "p1", Name: "One"}}
	if err := writer.Save(&Cache{Items: []Entry{{Ref: "EPIC:One", GID: "1", Name: "One", Type: "epic"}}}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	_, err := (&Store{Path: path, Project: &config.ProjectConfig{GID: "p2", Name: "Two"}}).Load()
	if !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("expected ErrProjectMismatch, got %v", err)
	}
}

func TestLoadRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refs.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"99","items":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (&Store{Path: path}).Load()
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("expected ErrUnsupportedSchema, got %v", err)
	}
}
