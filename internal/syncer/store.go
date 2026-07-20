package syncer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Store struct{ Root string }

var ErrUnsupportedSchema = errors.New("sync state has an unsupported schema version")

func (s *Store) Load(scope Scope) (*State, error) {
	data, err := os.ReadFile(s.path(scope))
	if errors.Is(err, os.ErrNotExist) {
		return &State{SchemaVersion: SchemaVersion, Scope: scope, CursorState: "uninitialized"}, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.SchemaVersion != SchemaVersion {
		return nil, ErrUnsupportedSchema
	}
	if state.Scope != scope {
		return nil, errors.New("sync state scope does not match the effective context")
	}
	return &state, nil
}

func (s *Store) Save(state *State) error {
	if state == nil {
		return errors.New("sync state is nil")
	}
	state.SchemaVersion = SchemaVersion
	path := s.path(state.Scope)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *Store) Reset(scope Scope) (bool, error) {
	err := os.Remove(s.path(scope))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) Acquire(scope Scope) (func(), error) {
	path := s.path(scope) + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
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
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > time.Hour {
			if os.Remove(path) == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, errors.New("synchronization scope is locked")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Store) path(scope Scope) string {
	encoded, _ := json.Marshal(scope)
	digest := sha256.Sum256(encoded)
	return filepath.Join(s.Root, "sync", hex.EncodeToString(digest[:12])+".json")
}
