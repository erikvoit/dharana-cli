package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRejectsNewerSchemaAndUpgradesOnSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store := &Store{Path: path}
	if err := os.WriteFile(path, []byte(`{"schema_version":"99"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected newer schema rejection")
	}
	if err := store.Save(&File{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := store.Load()
	if err != nil || cfg.SchemaVersion != SchemaVersion {
		t.Fatalf("expected current schema, cfg=%#v err=%v", cfg, err)
	}
}
