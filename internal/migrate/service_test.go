package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationDryRunAndApplyCreateRecoverableBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte("{\n  \"schema_version\": \"1\",\n  \"contexts\": []\n}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{ConfigDir: dir, Now: func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }}
	preview, err := service.Apply(true)
	if err != nil || !preview.Required || !preview.DryRun {
		t.Fatalf("unexpected preview %#v err=%v", preview, err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(original) {
		t.Fatal("dry-run changed state")
	}
	result, err := service.Apply(false)
	if err != nil || !result.Applied || result.Required {
		t.Fatalf("unexpected apply %#v err=%v", result, err)
	}
	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), `"schema_version": "2"`) {
		t.Fatalf("not migrated: %s", updated)
	}
	backup := path + ".bak.20260719T120000Z"
	backedUp, err := os.ReadFile(backup)
	if err != nil || string(backedUp) != string(original) {
		t.Fatalf("invalid backup %q err=%v", backedUp, err)
	}
}
