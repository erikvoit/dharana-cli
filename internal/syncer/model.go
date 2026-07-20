package syncer

import "time"

const SchemaVersion = "1"

type Scope struct {
	Identity     string `json:"identity"`
	WorkspaceGID string `json:"workspace_gid"`
	ProjectGID   string `json:"project_gid"`
	Context      string `json:"context"`
}

type State struct {
	SchemaVersion  string `json:"schema_version"`
	Scope          Scope  `json:"scope"`
	Cursor         string `json:"cursor,omitempty"`
	CursorState    string `json:"cursor_state"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	LastSuccessAt  string `json:"last_success_at,omitempty"`
	LastObservedAt string `json:"last_observed_at,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	Rebuilds       int    `json:"rebuilds"`
	EventsObserved int64  `json:"events_observed"`
}

type EventRecord struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Context       string `json:"context"`
	ResourceGID   string `json:"resource_gid,omitempty"`
	ResourceType  string `json:"resource_type,omitempty"`
	ObservedAt    string `json:"observed_at"`
	EventAt       string `json:"event_at,omitempty"`
	Type          string `json:"event_type"`
	Disposition   string `json:"disposition"`
	Action        string `json:"source_action,omitempty"`
	Field         string `json:"source_field,omitempty"`
	Checkpoint    string `json:"cursor_checkpoint,omitempty"`
}

type PullResult struct {
	Scope              Scope         `json:"scope"`
	CursorAdvanced     bool          `json:"cursor_advanced"`
	CursorState        string        `json:"cursor_state"`
	EventsObserved     int           `json:"events_observed"`
	ResourcesRefreshed []string      `json:"resources_refreshed,omitempty"`
	ResourcesRemoved   []string      `json:"resources_removed,omitempty"`
	Rebuilt            bool          `json:"rebuilt"`
	Warnings           []string      `json:"warnings,omitempty"`
	LagSeconds         *int64        `json:"lag_seconds,omitempty"`
	Events             []EventRecord `json:"events,omitempty"`
}

type StatusResult struct {
	Scope          Scope  `json:"scope"`
	Configured     bool   `json:"configured"`
	CursorState    string `json:"cursor_state"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	LastSuccessAt  string `json:"last_success_at,omitempty"`
	LastObservedAt string `json:"last_observed_at,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
	EventsObserved int64  `json:"events_observed"`
	Rebuilds       int    `json:"rebuilds"`
	LagSeconds     *int64 `json:"lag_seconds,omitempty"`
}

type ResetResult struct {
	Scope  Scope `json:"scope"`
	DryRun bool  `json:"dry_run"`
	Reset  bool  `json:"reset"`
}

func lagSeconds(value string, now time.Time) *int64 {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	lag := int64(now.Sub(parsed).Seconds())
	if lag < 0 {
		lag = 0
	}
	return &lag
}
