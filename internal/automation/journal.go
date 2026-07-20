package automation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/syncer"
)

const JournalSchemaVersion = "1"

type JournalEntry struct {
	SchemaVersion string              `json:"schema_version"`
	ID            string              `json:"id"`
	Kind          string              `json:"kind"`
	RecordedAt    string              `json:"recorded_at"`
	PolicyID      string              `json:"policy_id,omitempty"`
	PolicyVersion string              `json:"policy_version,omitempty"`
	EvaluationID  string              `json:"evaluation_id,omitempty"`
	Event         *syncer.EventRecord `json:"event,omitempty"`
	Evaluation    *Evaluation         `json:"evaluation,omitempty"`
	Action        *ActionOutcome      `json:"action,omitempty"`
}

type Evaluation struct {
	Matched      bool                `json:"matched"`
	TriggerMatch bool                `json:"trigger_match"`
	ContextMatch bool                `json:"context_match"`
	Query        string              `json:"query,omitempty"`
	Filters      map[string][]string `json:"filters,omitempty"`
	MatchedGIDs  []string            `json:"matched_gids,omitempty"`
	Explanation  []string            `json:"explanation"`
}

type ActionOutcome struct {
	ActionID       string `json:"action_id"`
	Type           string `json:"type"`
	Target         string `json:"target,omitempty"`
	Body           string `json:"body,omitempty"`
	State          string `json:"state,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	Disposition    string `json:"disposition"`
	Message        string `json:"message,omitempty"`
	RemoteGID      string `json:"remote_gid,omitempty"`
	Retryable      bool   `json:"retryable"`
	DryRun         bool   `json:"dry_run"`
	Details        any    `json:"details,omitempty"`
}

type HistoryResult struct {
	SchemaVersion string         `json:"schema_version"`
	Entries       []JournalEntry `json:"entries"`
	Retention     Retention      `json:"retention"`
}

type Retention struct {
	MaxEntries                  int  `json:"max_entries"`
	PreservesUnresolvedFailures bool `json:"preserves_unresolved_failures"`
}

type ExplainResult struct {
	Evaluation JournalEntry   `json:"evaluation"`
	Actions    []JournalEntry `json:"actions"`
}

type Journal struct {
	Path       string
	MaxEntries int
	Now        func() time.Time
}

func (j *Journal) Append(entry JournalEntry) error {
	path := j.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not create the automation journal directory.")
	}
	release, err := acquireJournalLock(path + ".lock")
	if err != nil {
		return output.NewError("AUTOMATION_JOURNAL_LOCKED", "Another process is writing the automation journal.")
	}
	defer release()
	if err := repairJournalTail(path); err != nil {
		return err
	}
	entry.SchemaVersion = JournalSchemaVersion
	if entry.RecordedAt == "" {
		entry.RecordedAt = j.now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not encode an automation journal record.")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not open the automation journal.")
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not append an automation journal record.")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not durably commit an automation journal record.")
	}
	if err := file.Close(); err != nil {
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not close the automation journal.")
	}
	return j.compactLocked()
}

func (j *Journal) History(limit int) (*HistoryResult, error) {
	entries, err := j.readAll()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return &HistoryResult{SchemaVersion: JournalSchemaVersion, Entries: entries, Retention: Retention{MaxEntries: j.maxEntries(), PreservesUnresolvedFailures: true}}, nil
}

func (j *Journal) Explain(evaluationID string) (*ExplainResult, error) {
	entries, err := j.readAll()
	if err != nil {
		return nil, err
	}
	var result ExplainResult
	for _, entry := range entries {
		if entry.ID == evaluationID && entry.Kind == "evaluation" {
			result.Evaluation = entry
		}
		if entry.EvaluationID == evaluationID && entry.Kind == "action" {
			result.Actions = append(result.Actions, entry)
		}
	}
	if result.Evaluation.ID == "" {
		return nil, output.NewError("AUTOMATION_EVALUATION_NOT_FOUND", "No journal evaluation matched the supplied ID.")
	}
	return &result, nil
}

func (j *Journal) Action(id string) (*JournalEntry, error) {
	entries, err := j.readAll()
	if err != nil {
		return nil, err
	}
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].ID == id && entries[index].Kind == "action" {
			entry := entries[index]
			return &entry, nil
		}
	}
	return nil, output.NewError("AUTOMATION_ACTION_NOT_FOUND", "No journal action matched the supplied ID.")
}

func (j *Journal) Succeeded(idempotencyKey string) (bool, error) {
	entries, err := j.readAll()
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Action != nil && entry.Action.IdempotencyKey == idempotencyKey && (entry.Action.Disposition == "succeeded" || entry.Action.Disposition == "no-op") {
			return true, nil
		}
	}
	return false, nil
}

func (j *Journal) RecentOutcomes(limit int) ([]ActionOutcome, error) {
	entries, err := j.readAll()
	if err != nil {
		return nil, err
	}
	var outcomes []ActionOutcome
	for index := len(entries) - 1; index >= 0 && len(outcomes) < limit; index-- {
		if entries[index].Action != nil {
			outcomes = append(outcomes, *entries[index].Action)
		}
	}
	return outcomes, nil
}

func (j *Journal) UnresolvedFailures(policyID string) (int, error) {
	entries, err := j.readAll()
	if err != nil {
		return 0, err
	}
	count := 0
	seen := map[string]bool{}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.PolicyID != policyID || entry.Action == nil {
			continue
		}
		key := entry.Action.IdempotencyKey
		if seen[key] {
			continue
		}
		seen[key] = true
		if entry.Action.Disposition == "failed" {
			count++
		}
	}
	return count, nil
}

func (j *Journal) readAll() ([]JournalEntry, error) {
	file, err := os.Open(j.path())
	if errors.Is(err, os.ErrNotExist) {
		return []JournalEntry{}, nil
	}
	if err != nil {
		return nil, output.NewError("AUTOMATION_JOURNAL_READ_FAILED", "Could not open the automation journal.")
	}
	defer file.Close()
	var entries []JournalEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	var pending []byte
	for scanner.Scan() {
		if pending != nil {
			entry, decodeErr := decodeJournalEntry(pending)
			if decodeErr != nil {
				return nil, decodeErr
			}
			entries = append(entries, entry)
		}
		pending = append(pending[:0], scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
		return nil, output.NewError("AUTOMATION_JOURNAL_READ_FAILED", "Could not read the automation journal.")
	}
	if pending != nil {
		entry, decodeErr := decodeJournalEntry(pending)
		if decodeErr != nil {
			info, statErr := file.Stat()
			last := []byte{0}
			if statErr != nil || info.Size() == 0 {
				return nil, decodeErr
			}
			if _, readErr := file.ReadAt(last, info.Size()-1); readErr != nil || last[0] == '\n' {
				return nil, decodeErr
			}
			return entries, nil
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func decodeJournalEntry(data []byte) (JournalEntry, error) {
	var entry JournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return JournalEntry{}, output.NewError("AUTOMATION_JOURNAL_CORRUPT", "The automation journal contains an invalid record.")
	}
	if entry.SchemaVersion != JournalSchemaVersion {
		return JournalEntry{}, output.NewError("AUTOMATION_JOURNAL_SCHEMA_UNSUPPORTED", "The automation journal uses an unsupported schema version.")
	}
	return entry, nil
}

func repairJournalTail(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not inspect the automation journal boundary.")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	window := int64(2 << 20)
	start := info.Size() - window
	if start < 0 {
		start = 0
	}
	tail := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(tail, start); err != nil {
		return err
	}
	boundary := bytes.LastIndexByte(tail, '\n') + 1
	fragment := tail[boundary:]
	if _, err := decodeJournalEntry(fragment); err == nil {
		if _, err := file.WriteAt([]byte{'\n'}, info.Size()); err != nil {
			return err
		}
		return file.Sync()
	}
	if boundary == 0 && start > 0 {
		return output.NewError("AUTOMATION_JOURNAL_CORRUPT", "The final automation journal record exceeds the safe recovery limit.")
	}
	if err := file.Truncate(start + int64(boundary)); err != nil {
		return err
	}
	return file.Sync()
}

func (j *Journal) compactLocked() error {
	entries, err := j.readAll()
	if err != nil || len(entries) <= j.maxEntries() {
		return err
	}
	protectedEvaluations := map[string]bool{}
	for _, entry := range entries {
		if entry.Action != nil && (entry.Action.Disposition == "failed" || entry.Action.Disposition == "conflicted" || entry.Action.Disposition == "quarantined") {
			protectedEvaluations[entry.EvaluationID] = true
		}
	}
	protected := make([]JournalEntry, 0)
	ordinary := make([]JournalEntry, 0)
	for _, entry := range entries {
		if protectedEvaluations[entry.EvaluationID] || protectedEvaluations[entry.ID] {
			protected = append(protected, entry)
		} else {
			ordinary = append(ordinary, entry)
		}
	}
	keepOrdinary := j.maxEntries() - len(protected)
	if keepOrdinary < 0 {
		keepOrdinary = 0
	}
	if len(ordinary) > keepOrdinary {
		ordinary = ordinary[len(ordinary)-keepOrdinary:]
	}
	entries = append(ordinary, protected...)
	sort.SliceStable(entries, func(i, k int) bool { return entries[i].RecordedAt < entries[k].RecordedAt })
	path := j.path()
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not compact the automation journal.")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	for _, entry := range entries {
		data, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			_ = tmp.Close()
			return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not compact the automation journal.")
		}
		if _, err := tmp.Write(append(data, '\n')); err != nil {
			_ = tmp.Close()
			return output.NewError("AUTOMATION_JOURNAL_WRITE_FAILED", "Could not compact the automation journal.")
		}
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

func acquireJournalLock(path string) (func(), error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) || time.Now().After(deadline) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > time.Hour {
			if os.Remove(path) == nil {
				continue
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (j *Journal) path() string {
	if j != nil && j.Path != "" {
		return j.Path
	}
	return filepath.Join(".dharana", "automation-journal.jsonl")
}

func (j *Journal) maxEntries() int {
	if j != nil && j.MaxEntries > 0 {
		return j.MaxEntries
	}
	return 10000
}

func (j *Journal) now() time.Time {
	if j != nil && j.Now != nil {
		return j.Now()
	}
	return time.Now()
}
