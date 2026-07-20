package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/syncer"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type Synchronizer interface {
	Pull(context.Context) (*syncer.PullResult, error)
	Status(context.Context) (*syncer.StatusResult, error)
}

type WorkBackend interface {
	ReadyWork(context.Context, work.ReadyWorkOptions) (*work.ReadyWorkResult, error)
	GetWork(context.Context, string) (*work.GetWorkResult, error)
	CommentWork(context.Context, work.CommentWorkOptions) (*work.CommentWorkResult, error)
	CompleteWork(context.Context, work.CompleteWorkOptions) (*work.CompleteWorkResult, error)
}

type ScopeAuthorizer interface {
	RequireScopes(context.Context, []string) error
}

type Runtime struct {
	Sync            Synchronizer
	Work            WorkBackend
	Auth            ScopeAuthorizer
	Journal         *Journal
	LeaseRoot       string
	LeaseStaleAfter time.Duration
	Now             func() time.Time
}

type RunOptions struct {
	DryRun   bool          `json:"dry_run"`
	Apply    bool          `json:"apply"`
	Once     bool          `json:"once"`
	Interval time.Duration `json:"-"`
}

type RunResult struct {
	Policies        []string           `json:"policies"`
	Events          int                `json:"events"`
	Evaluations     int                `json:"evaluations"`
	ActionsObserved int                `json:"actions_observed"`
	Actions         []ActionOutcome    `json:"actions"`
	Sync            *syncer.PullResult `json:"sync,omitempty"`
	DryRun          bool               `json:"dry_run"`
	Apply           bool               `json:"apply"`
}

type RetryResult struct {
	OriginalActionID string        `json:"original_action_id"`
	Action           ActionOutcome `json:"action"`
}

func (r *Runtime) Run(ctx context.Context, policies []*Policy, opts RunOptions, emit func(any) error) (*RunResult, error) {
	if len(policies) == 0 {
		return nil, output.NewError("AUTOMATION_POLICY_REQUIRED", "Provide at least one automation policy.")
	}
	for _, policy := range policies {
		validation := Validate(policy)
		if !validation.Valid {
			return nil, output.NewErrorWithDetails("AUTOMATION_POLICY_INVALID", "An automation policy failed validation.", validation)
		}
		if policy.Spec.Mode == "apply" && (opts.Apply || opts.DryRun) && r.Auth != nil {
			if err := r.Auth.RequireScopes(ctx, policy.Spec.Permissions.Scopes); err != nil {
				return nil, err
			}
		}
	}
	if r.Sync == nil || r.Work == nil {
		return nil, output.NewError("AUTOMATION_NOT_CONFIGURED", "Automation runtime services are not configured.")
	}
	release, heartbeat, err := r.acquireLease(policies)
	if err != nil {
		return nil, err
	}
	defer release()
	if opts.Once || opts.Interval == 0 {
		if err := heartbeat(); err != nil {
			return nil, err
		}
		return r.runOnce(ctx, policies, opts, emit)
	}
	if opts.Interval < time.Second || opts.Interval > 15*time.Minute {
		return nil, output.NewError("AUTOMATION_INTERVAL_INVALID", "Automation interval must be between 1 second and 15 minutes.")
	}
	aggregate := &RunResult{DryRun: opts.DryRun, Apply: opts.Apply}
	for {
		if err := heartbeat(); err != nil {
			return aggregate, err
		}
		result, runErr := r.runOnce(ctx, policies, opts, emit)
		if runErr != nil {
			return aggregate, runErr
		}
		aggregate.Policies = result.Policies
		aggregate.Events += result.Events
		aggregate.Evaluations += result.Evaluations
		aggregate.ActionsObserved += result.ActionsObserved
		aggregate.Actions = append(aggregate.Actions, result.Actions...)
		if len(aggregate.Actions) > 1000 {
			aggregate.Actions = aggregate.Actions[len(aggregate.Actions)-1000:]
		}
		aggregate.Sync = result.Sync
		timer := time.NewTimer(opts.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return aggregate, nil
		case <-timer.C:
		}
	}
}

func (r *Runtime) runOnce(ctx context.Context, policies []*Policy, opts RunOptions, emit func(any) error) (*RunResult, error) {
	pull, err := r.Sync.Pull(ctx)
	if err != nil {
		return nil, err
	}
	result := &RunResult{Sync: pull, Events: len(pull.Events), DryRun: opts.DryRun, Apply: opts.Apply}
	for _, policy := range policies {
		result.Policies = append(result.Policies, policy.Metadata.ID)
	}
	sort.Strings(result.Policies)
	failures := map[string]int{}
	for _, policy := range policies {
		count, err := r.journal().UnresolvedFailures(policy.Metadata.ID)
		if err != nil {
			return nil, err
		}
		failures[policy.Metadata.ID] = count
	}
	for _, event := range pull.Events {
		for _, policy := range policies {
			evaluation, evalID, evalErr := r.evaluate(ctx, policy, event)
			if evalErr != nil {
				return nil, evalErr
			}
			result.Evaluations++
			if err := r.journal().Append(JournalEntry{ID: evalID, Kind: "evaluation", PolicyID: policy.Metadata.ID, PolicyVersion: policy.Version, Event: &event, Evaluation: evaluation}); err != nil {
				return nil, err
			}
			if emit != nil {
				if err := emit(map[string]any{"schema_version": "1", "record_type": "evaluation", "evaluation_id": evalID, "policy_id": policy.Metadata.ID, "evaluation": evaluation}); err != nil {
					return nil, err
				}
			}
			if !evaluation.Matched {
				continue
			}
			ordinal := 0
			for _, declaredAction := range policy.Spec.Actions {
				for _, action := range expandAction(declaredAction, *evaluation, event) {
					outcome := r.executeAction(ctx, policy, event, evalID, ordinal, action, opts, failures[policy.Metadata.ID] >= policy.Spec.FailureThreshold && policy.Spec.FailureThreshold > 0)
					if outcome.Disposition == "failed" {
						failures[policy.Metadata.ID]++
					}
					result.ActionsObserved++
					result.Actions = append(result.Actions, outcome)
					actionID := actionRecordID(evalID, ordinal, action)
					if err := r.journal().Append(JournalEntry{ID: actionID, Kind: "action", PolicyID: policy.Metadata.ID, PolicyVersion: policy.Version, EvaluationID: evalID, Event: &event, Action: &outcome}); err != nil {
						return nil, err
					}
					if emit != nil {
						if err := emit(map[string]any{"schema_version": "1", "record_type": "action", "action_id": actionID, "policy_id": policy.Metadata.ID, "action": outcome}); err != nil {
							return nil, err
						}
					}
					ordinal++
				}
			}
		}
	}
	return result, nil
}

func (r *Runtime) evaluate(ctx context.Context, policy *Policy, event syncer.EventRecord) (*Evaluation, string, error) {
	evaluation := &Evaluation{
		TriggerMatch: policy.Spec.When.Event == event.Type,
		ContextMatch: policy.Spec.Context == event.Context,
		Query:        policy.Spec.Evaluate.Query,
		Filters:      copyFilters(policy.Spec.Evaluate.Filters),
		Explanation:  []string{},
	}
	evaluation.Explanation = append(evaluation.Explanation, explainMatch("trigger", evaluation.TriggerMatch, policy.Spec.When.Event, event.Type))
	evaluation.Explanation = append(evaluation.Explanation, explainMatch("context", evaluation.ContextMatch, policy.Spec.Context, event.Context))
	if !evaluation.TriggerMatch || !evaluation.ContextMatch {
		evaluation.Matched = false
		return evaluation, evaluationID(policy, event), nil
	}
	switch policy.Spec.Evaluate.Query {
	case "", "event.resource":
		if event.ResourceGID == "" {
			evaluation.Explanation = append(evaluation.Explanation, "event resource did not include a stable GID")
			break
		}
		detail, err := r.Work.GetWork(ctx, event.ResourceGID)
		if err != nil {
			evaluation.Explanation = append(evaluation.Explanation, "event resource is not currently applicable")
			break
		}
		if matchesWorkDetail(detail.Item, policy.Spec.Evaluate.Filters) {
			evaluation.MatchedGIDs = []string{detail.Item.GID}
		}
	case "work.ready":
		ready, err := r.Work.ReadyWork(ctx, work.ReadyWorkOptions{
			Types:      policy.Spec.Evaluate.Filters["type"],
			Priorities: policy.Spec.Evaluate.Filters["priority"],
			Components: policy.Spec.Evaluate.Filters["component"],
		})
		if err != nil {
			return nil, "", err
		}
		for _, item := range ready.Items {
			if matchesValues(item.Status, policy.Spec.Evaluate.Filters["status"]) {
				evaluation.MatchedGIDs = append(evaluation.MatchedGIDs, item.GID)
			}
		}
	}
	sort.Strings(evaluation.MatchedGIDs)
	evaluation.Matched = len(evaluation.MatchedGIDs) > 0
	if evaluation.Matched {
		evaluation.Explanation = append(evaluation.Explanation, "deterministic query matched "+itoa(len(evaluation.MatchedGIDs))+" resource(s)")
	} else {
		evaluation.Explanation = append(evaluation.Explanation, "deterministic query matched no resources")
	}
	return evaluation, evaluationID(policy, event), nil
}

func (r *Runtime) executeAction(ctx context.Context, policy *Policy, event syncer.EventRecord, evalID string, index int, action Action, opts RunOptions, paused bool) ActionOutcome {
	target := strings.TrimSpace(action.Target)
	if target == "" || target == "event.resource" {
		target = event.ResourceGID
	}
	key := actionRecordID(evalID, index, action)
	outcome := ActionOutcome{ActionID: actionIdentity(index, action), Type: action.Type, Target: target, Body: action.Body, IdempotencyKey: key, DryRun: opts.DryRun}
	if paused {
		outcome.Disposition = "quarantined"
		outcome.Message = "The policy reached its configured failure threshold."
		return outcome
	}
	succeeded, journalErr := r.journal().Succeeded(key)
	if journalErr != nil {
		outcome.Disposition = "quarantined"
		outcome.Message = "Replay protection could not read the automation journal."
		return outcome
	}
	if succeeded {
		outcome.Disposition = "no-op"
		outcome.Message = "This idempotent action already succeeded."
		return outcome
	}
	if action.Type == "emit" {
		if opts.DryRun {
			outcome.Disposition = "proposed"
			outcome.Message = "Event emission was evaluated in dry-run mode."
		} else {
			outcome.Disposition = "succeeded"
			outcome.Message = "Event emitted."
		}
		return outcome
	}
	if policy.Spec.Mode != "apply" || (!opts.Apply && !opts.DryRun) {
		outcome.Disposition = "proposed"
		outcome.Message = "Policy or runtime is not explicitly enabled for apply."
		return outcome
	}
	if action.Type != "emit" && r.Auth != nil {
		if err := r.Auth.RequireScopes(ctx, policy.Spec.Permissions.Scopes); err != nil {
			outcome.Disposition = "conflicted"
			outcome.Message = "Required OAuth scopes are unavailable."
			return outcome
		}
	}
	switch action.Type {
	case "comment":
		value, err := r.Work.CommentWork(ctx, work.CommentWorkOptions{Ref: target, Body: action.Body, DryRun: opts.DryRun})
		if err != nil {
			return failedOutcome(outcome, err)
		}
		if opts.DryRun {
			outcome.Disposition = "proposed"
		} else {
			outcome.Disposition = "succeeded"
		}
		if value.Story != nil {
			outcome.RemoteGID = value.Story.GID
		}
	case "complete", "reopen":
		value, err := r.Work.CompleteWork(ctx, work.CompleteWorkOptions{Ref: target, DryRun: opts.DryRun, Reopen: action.Type == "reopen"})
		if err != nil {
			return failedOutcome(outcome, err)
		}
		switch {
		case value.Noop:
			outcome.Disposition = "no-op"
		case opts.DryRun:
			outcome.Disposition = "proposed"
		default:
			outcome.Disposition = "succeeded"
		}
		outcome.RemoteGID = value.Target.GID
	default:
		outcome.Disposition = "quarantined"
		outcome.Message = "Unsupported action reached execution."
	}
	return outcome
}

func (r *Runtime) Retry(ctx context.Context, actionID string, dryRun bool) (*RetryResult, error) {
	entry, err := r.journal().Action(actionID)
	if err != nil {
		return nil, err
	}
	if entry.Action == nil || !entry.Action.Retryable || entry.Action.Disposition != "failed" {
		return nil, output.NewError("AUTOMATION_ACTION_NOT_RETRYABLE", "Only safely retryable failed actions can be retried.")
	}
	if succeeded, err := r.journal().Succeeded(entry.Action.IdempotencyKey); err != nil {
		return nil, err
	} else if succeeded {
		return nil, output.NewError("AUTOMATION_REPLAY_BLOCKED", "The action already succeeded and cannot be replayed.")
	}
	action := Action{ID: entry.Action.ActionID, Type: entry.Action.Type, Target: entry.Action.Target, Body: entry.Action.Body}
	policy := &Policy{Metadata: PolicyMetadata{ID: entry.PolicyID}, Version: entry.PolicyVersion, Spec: PolicySpec{Mode: "apply", Permissions: PolicyPermissions{Scopes: scopesForAction(action.Type)}}}
	event := syncer.EventRecord{}
	if entry.Event != nil {
		event = *entry.Event
	}
	outcome := r.executeAction(ctx, policy, event, entry.EvaluationID, 0, action, RunOptions{DryRun: dryRun, Apply: true}, false)
	outcome.IdempotencyKey = entry.Action.IdempotencyKey
	retryID := actionID + ":retry:" + r.now().UTC().Format("20060102T150405.000000000")
	if err := r.journal().Append(JournalEntry{ID: retryID, Kind: "action", PolicyID: entry.PolicyID, PolicyVersion: entry.PolicyVersion, EvaluationID: entry.EvaluationID, Event: &event, Action: &outcome}); err != nil {
		return nil, err
	}
	return &RetryResult{OriginalActionID: actionID, Action: outcome}, nil
}

func failedOutcome(outcome ActionOutcome, err error) ActionOutcome {
	outcome.Disposition = "failed"
	outcome.Retryable = false
	outcome.Message = "The action failed after authoritative precondition checks."
	var appErr *output.AppError
	if errors.As(err, &appErr) {
		outcome.Message = appErr.Message
		outcome.Details = appErr.Details
		switch appErr.Code {
		case "ASANA_RATE_LIMITED", "ASANA_TRANSIENT_FAILURE", "ASANA_REQUEST_FAILED":
			outcome.Retryable = true
		}
	}
	return outcome
}

func evaluationID(policy *Policy, event syncer.EventRecord) string {
	digest := sha256.Sum256([]byte(policy.Metadata.ID + "|" + policy.Version + "|" + event.ID))
	return "eval_" + hex.EncodeToString(digest[:12])
}

func actionRecordID(evalID string, index int, action Action) string {
	raw := evalID + "|" + itoa(index) + "|" + actionIdentity(index, action) + "|" + action.Type + "|" + action.Target
	digest := sha256.Sum256([]byte(raw))
	return "act_" + hex.EncodeToString(digest[:12])
}

func actionIdentity(index int, action Action) string {
	if action.ID != "" {
		return action.ID
	}
	return "action-" + itoa(index+1)
}

func matchesWorkDetail(item work.WorkDetail, filters map[string][]string) bool {
	return matchesValues(item.Type, filters["type"]) && matchesValues(item.Status, filters["status"]) && matchesField(item.Fields, "priority", filters["priority"]) && matchesField(item.Fields, "component", filters["component"])
}

func matchesField(fields []work.FieldValue, name string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	for _, field := range fields {
		if !strings.EqualFold(field.Name, name) {
			continue
		}
		if matchesValues(field.DisplayValue, expected) || matchesValues(field.EnumName, expected) {
			return true
		}
	}
	return false
}

func expandAction(action Action, evaluation Evaluation, event syncer.EventRecord) []Action {
	if action.Target != "query.matches" {
		return []Action{action}
	}
	expanded := make([]Action, 0, len(evaluation.MatchedGIDs))
	for _, gid := range evaluation.MatchedGIDs {
		copyValue := action
		copyValue.Target = gid
		expanded = append(expanded, copyValue)
	}
	if len(expanded) == 0 && action.Type == "emit" {
		copyValue := action
		copyValue.Target = event.ResourceGID
		return []Action{copyValue}
	}
	return expanded
}

func matchesValues(actual string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	for _, value := range expected {
		if strings.EqualFold(strings.TrimSpace(value), actual) {
			return true
		}
	}
	return false
}

func explainMatch(label string, matched bool, expected, actual string) string {
	if matched {
		return label + " matched " + expected
	}
	return label + " did not match: expected " + expected + ", observed " + actual
}

func copyFilters(filters map[string][]string) map[string][]string {
	if len(filters) == 0 {
		return nil
	}
	copyValue := make(map[string][]string, len(filters))
	for key, values := range filters {
		copyValue[key] = append([]string(nil), values...)
		slices.Sort(copyValue[key])
	}
	return copyValue
}

func scopesForAction(actionType string) []string {
	if actionType == "emit" {
		return nil
	}
	if actionType == "comment" {
		return []string{"stories:write", "tasks:read"}
	}
	return []string{"tasks:read", "tasks:write"}
}

func (r *Runtime) acquireLease(policies []*Policy) (func(), func() error, error) {
	ids := make([]string, 0, len(policies))
	for _, policy := range policies {
		ids = append(ids, policy.Spec.Context+":"+policy.Metadata.ID)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "|")))
	root := r.LeaseRoot
	if root == "" {
		root = filepath.Join(".dharana", "automation")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, nil, output.NewError("AUTOMATION_LEASE_FAILED", "Could not create the automation lease directory.")
	}
	path := filepath.Join(root, hex.EncodeToString(digest[:12])+".lease")
	staleAfter := r.LeaseStaleAfter
	if staleAfter == 0 {
		staleAfter = time.Hour
	}
	var file *os.File
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			break
		}
		info, statErr := os.Stat(path)
		if !errors.Is(err, os.ErrExist) || statErr != nil || staleAfter < time.Minute || r.now().Sub(info.ModTime()) <= staleAfter || os.Remove(path) != nil {
			return nil, nil, output.NewError("AUTOMATION_LEASE_HELD", "Another runtime already holds the lease for this context and policy set.")
		}
	}
	if file == nil {
		return nil, nil, output.NewError("AUTOMATION_LEASE_HELD", "Another runtime already holds the lease for this context and policy set.")
	}
	_, _ = file.WriteString(r.now().UTC().Format(time.RFC3339))
	_ = file.Close()
	heartbeat := func() error {
		now := r.now()
		if err := os.Chtimes(path, now, now); err != nil {
			return output.NewError("AUTOMATION_LEASE_LOST", "The automation runtime lost its context and policy-set lease.")
		}
		return nil
	}
	return func() { _ = os.Remove(path) }, heartbeat, nil
}

func (r *Runtime) journal() *Journal {
	if r.Journal == nil {
		return &Journal{}
	}
	return r.Journal
}

func (r *Runtime) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
