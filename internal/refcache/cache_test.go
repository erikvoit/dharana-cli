package refcache

import (
	"os"
	"path/filepath"
	"testing"
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
