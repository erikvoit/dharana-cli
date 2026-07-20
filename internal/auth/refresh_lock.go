package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
)

func acquireRefreshLock(profile string) (func(), error) {
	dir := filepath.Join(defaultConfigDir(), "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, profile)
	path := filepath.Join(dir, "oauth-refresh-"+name+".lock")
	deadline := time.Now().Add(5 * time.Second)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.WriteString(time.Now().UTC().Format(time.RFC3339))
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, output.NewError("OAUTH_REFRESH_LOCK_FAILED", "Another process is refreshing this OAuth profile.")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
