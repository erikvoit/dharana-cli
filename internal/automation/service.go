package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/syncer"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type AutomationService struct {
	Runtime *Runtime
	Sync    Synchronizer
	Journal *Journal
	Auth    ScopeAuthorizer
	Config  ConfigStore
	Now     func() time.Time
}

type ConfigStore interface{ Load() (*config.File, error) }

type HealthFinding struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
}

type Status struct {
	SchemaVersion   string               `json:"schema_version"`
	Health          string               `json:"health"`
	Profile         string               `json:"profile,omitempty"`
	Context         string               `json:"context,omitempty"`
	Sync            *syncer.StatusResult `json:"sync,omitempty"`
	EnabledPolicies []PolicyStatus       `json:"enabled_policies"`
	RecentOutcomes  []ActionOutcome      `json:"recent_outcomes"`
	Findings        []HealthFinding      `json:"findings"`
}

type PolicyStatus struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Mode    string `json:"mode"`
	Context string `json:"context"`
}

type DoctorResult struct {
	Healthy  bool               `json:"healthy"`
	Policies []ValidationResult `json:"policies"`
	Findings []HealthFinding    `json:"findings"`
}

func (s *AutomationService) Status(ctx context.Context, policies []*Policy) (*Status, error) {
	result := &Status{SchemaVersion: "1", Health: "healthy", EnabledPolicies: []PolicyStatus{}, RecentOutcomes: []ActionOutcome{}, Findings: []HealthFinding{}}
	if s.Sync == nil {
		result.Health = "failed"
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_NOT_CONFIGURED", "Synchronization is not configured.", "Configure an active context and authentication profile."))
		return result, nil
	}
	syncStatus, err := s.Sync.Status(ctx)
	if err != nil {
		code := "AUTOMATION_SYNC_STATUS_FAILED"
		result.Health = "failed"
		var appErr *output.AppError
		if errors.As(err, &appErr) {
			code = appErr.Code
			if strings.HasPrefix(code, "OAUTH_") || strings.HasPrefix(code, "AUTH_") || code == "INVALID_AUTH" || code == "TOKEN_NOT_CONFIGURED" {
				result.Health = "authentication-required"
			}
		}
		result.Findings = append(result.Findings, healthFinding(code, "Synchronization status could not resolve the effective identity and context.", "Validate authentication and the active context."))
		return result, nil
	}
	result.Sync = syncStatus
	result.Context = syncStatus.Scope.Context
	result.Profile = syncStatus.Scope.Identity
	switch syncStatus.CursorState {
	case "uninitialized":
		result.Health = "rebuild-required"
		result.Findings = append(result.Findings, healthFinding("SYNC_REBUILD_REQUIRED", "No synchronization cursor has been established.", "Run dharana sync pull."))
	case "degraded":
		if syncStatus.LastErrorCode == "SYNC_AUTHENTICATION_REQUIRED" {
			result.Health = "authentication-required"
		} else {
			result.Health = "degraded"
		}
		result.Findings = append(result.Findings, healthFinding(syncStatus.LastErrorCode, "The last synchronization attempt failed.", "Run dharana automation doctor and retry sync pull."))
	}
	if syncStatus.CursorState == "ready" && syncStatus.LastSuccessAt == "" {
		result.Health = "degraded"
		result.Findings = append(result.Findings, healthFinding("SYNC_FRESHNESS_UNKNOWN", "Synchronization has no verified successful checkpoint.", "Run dharana sync pull."))
	} else if lastSuccess, parseErr := time.Parse(time.RFC3339, syncStatus.LastSuccessAt); parseErr == nil && s.now().Sub(lastSuccess) > 15*time.Minute {
		result.Health = "degraded"
		result.Findings = append(result.Findings, healthFinding("SYNC_STALE", "Synchronization has not advanced successfully within 15 minutes.", "Check the supervised runtime and run dharana sync pull."))
	}
	for _, policy := range policies {
		result.EnabledPolicies = append(result.EnabledPolicies, PolicyStatus{ID: policy.Metadata.ID, Version: policy.Version, Mode: policy.Spec.Mode, Context: policy.Spec.Context})
	}
	sort.SliceStable(result.EnabledPolicies, func(i, j int) bool { return result.EnabledPolicies[i].ID < result.EnabledPolicies[j].ID })
	journal := s.journal()
	outcomes, journalErr := journal.RecentOutcomes(20)
	if journalErr != nil {
		result.Health = "failed"
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_JOURNAL_READ_FAILED", "The automation journal is unavailable.", "Repair journal storage before enabling apply mode."))
		return result, nil
	}
	result.RecentOutcomes = outcomes
	seenOutcomes := map[string]bool{}
	for _, outcome := range outcomes {
		if seenOutcomes[outcome.IdempotencyKey] {
			continue
		}
		seenOutcomes[outcome.IdempotencyKey] = true
		switch outcome.Disposition {
		case "quarantined":
			result.Health = "paused"
			result.Findings = append(result.Findings, healthFinding("AUTOMATION_ACTION_QUARANTINED", "A policy action is quarantined.", "Inspect automation history and retry only after correcting the failure."))
		case "failed", "conflicted":
			if result.Health == "healthy" {
				result.Health = "degraded"
			}
			result.Findings = append(result.Findings, healthFinding("AUTOMATION_ACTION_FAILED", "A recent policy action did not succeed.", "Inspect the action with automation explain."))
		}
	}
	return result, nil
}

func (s *AutomationService) Doctor(ctx context.Context, policies []*Policy) (*DoctorResult, error) {
	result := &DoctorResult{Healthy: true, Policies: []ValidationResult{}, Findings: []HealthFinding{}}
	if s.Sync == nil {
		result.Healthy = false
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_NOT_CONFIGURED", "Synchronization services are not configured.", "Configure authentication and an active project context."))
	} else if _, err := s.Sync.Status(ctx); err != nil {
		result.Healthy = false
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_IDENTITY_INVALID", "The effective authentication identity or context could not be resolved.", "Validate the selected profile and context ownership."))
	}
	cfg, err := s.Config.Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read configuration for automation diagnostics.")
	}
	for _, policy := range policies {
		validation := Validate(policy)
		result.Policies = append(result.Policies, validation)
		if !validation.Valid {
			result.Healthy = false
		}
		contextValue, ok := cfg.ContextByName(policy.Spec.Context)
		if !ok {
			result.Healthy = false
			result.Findings = append(result.Findings, healthFinding("AUTOMATION_CONTEXT_NOT_FOUND", "Policy "+policy.Metadata.ID+" references an unknown context.", "Create or select the declared context."))
			continue
		}
		if cfg.ActiveProject == nil || contextValue.Project.GID != cfg.ActiveProject.GID {
			result.Healthy = false
			result.Findings = append(result.Findings, healthFinding("AUTOMATION_CONTEXT_NOT_ACTIVE", "Policy "+policy.Metadata.ID+" does not match the effective project.", "Run the policy with its declared context active."))
		}
		if policy.Spec.Mode == "apply" && s.Auth != nil {
			if err := s.Auth.RequireScopes(ctx, policy.Spec.Permissions.Scopes); err != nil {
				result.Healthy = false
				result.Findings = append(result.Findings, healthFinding("AUTOMATION_SCOPE_REQUIRED", "Policy "+policy.Metadata.ID+" lacks an effective required scope.", "Reauthorize the profile with the declared least-privilege scopes."))
			}
		}
		if s.Runtime != nil && s.Runtime.Work != nil && policy.Spec.Evaluate.Query == "work.ready" {
			_, queryErr := s.Runtime.Work.ReadyWork(ctx, work.ReadyWorkOptions{Types: policy.Spec.Evaluate.Filters["type"], Priorities: policy.Spec.Evaluate.Filters["priority"], Components: policy.Spec.Evaluate.Filters["component"]})
			if queryErr != nil {
				result.Healthy = false
				result.Findings = append(result.Findings, healthFinding("AUTOMATION_QUERY_UNRESOLVED", "Policy "+policy.Metadata.ID+" could not resolve its fields and filters.", "Correct project field mappings or policy filters."))
			}
		}
		if s.Runtime != nil && s.Runtime.Work != nil {
			for _, action := range policy.Spec.Actions {
				target := strings.TrimSpace(action.Target)
				if action.Type == "emit" || target == "" || target == "event.resource" || target == "query.matches" {
					continue
				}
				if _, targetErr := s.Runtime.Work.GetWork(ctx, target); targetErr != nil {
					result.Healthy = false
					result.Findings = append(result.Findings, healthFinding("AUTOMATION_ACTION_TARGET_UNRESOLVED", "Policy "+policy.Metadata.ID+" has an inaccessible action target.", "Use an authoritative GID or resolvable Dharana reference."))
				}
			}
		}
	}
	path := s.journal().path()
	parent := filepath.Dir(path)
	if info, err := os.Stat(parent); err == nil && !info.IsDir() {
		result.Healthy = false
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_STORAGE_INVALID", "The journal parent path is not a directory.", "Choose a writable automation storage directory."))
	} else if err == nil && info.Mode().Perm()&0o222 == 0 {
		result.Healthy = false
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_STORAGE_READ_ONLY", "The automation storage directory is not writable.", "Grant the runtime identity write access to its Dharana state directory."))
	} else if err != nil && !os.IsNotExist(err) {
		result.Healthy = false
		result.Findings = append(result.Findings, healthFinding("AUTOMATION_STORAGE_UNAVAILABLE", "Automation storage cannot be inspected.", "Correct storage permissions."))
	}
	return result, nil
}

func healthFinding(code, message, remediation string) HealthFinding {
	if code == "" {
		code = "AUTOMATION_UNHEALTHY"
	}
	return HealthFinding{Code: code, Message: message, Remediation: remediation}
}

func (s *AutomationService) journal() *Journal {
	if s.Journal == nil {
		return &Journal{}
	}
	return s.Journal
}

func (s *AutomationService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
