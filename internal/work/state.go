package work

import (
	"context"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/workflowstate"
)

type TransitionWorkOptions struct {
	Ref            string
	To             string
	Reason         string
	DryRun         bool
	SkipRefRefresh bool
}

type TransitionWorkResult struct {
	Target          DependencyRef `json:"target"`
	BeforeState     string        `json:"before_state,omitempty"`
	AfterState      string        `json:"after_state"`
	BeforeCompleted bool          `json:"before_completed"`
	AfterCompleted  bool          `json:"after_completed"`
	AllowedNext     []string      `json:"allowed_next"`
	Reason          string        `json:"reason,omitempty"`
	DryRun          bool          `json:"dry_run"`
	Noop            bool          `json:"noop"`
	ReasonRecorded  bool          `json:"reason_recorded,omitempty"`
	RefreshedRefs   bool          `json:"refreshed_refs,omitempty"`
}

type StateCapabilities struct {
	SchemaVersion string                     `json:"schema_version"`
	States        []workflowstate.Definition `json:"states"`
	Transitions   map[string][]string        `json:"transitions"`
	Derived       []string                   `json:"derived_properties"`
	Separate      []string                   `json:"separate_lifecycles"`
}

func WorkStateCapabilities() StateCapabilities {
	transitions := map[string][]string{}
	for _, state := range workflowstate.Names() {
		transitions[state] = workflowstate.AllowedTransitions(state)
	}
	return StateCapabilities{SchemaVersion: "1", States: workflowstate.Definitions(), Transitions: transitions, Derived: []string{"blocked"}, Separate: []string{"released"}}
}

func (s *Service) TransitionWork(ctx context.Context, opts TransitionWorkOptions) (*TransitionWorkResult, error) {
	opts.Ref = strings.TrimSpace(opts.Ref)
	opts.Reason = strings.TrimSpace(opts.Reason)
	to, ok := workflowstate.Normalize(opts.To)
	if !ok {
		return nil, output.NewErrorWithDetails("WORK_STATE_INVALID", "Unknown canonical work state.", map[string]any{"provided": opts.To, "supported": workflowstate.Names()})
	}
	resolved, cfg, err := s.resolveActive(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.States.Complete() {
		return nil, output.NewError("STATE_MAPPING_NOT_CONFIGURED", "Configure and bind every canonical workflow state before transitioning work.")
	}
	target, err := s.resolveWorkReference(ctx, resolved.Token, opts.Ref)
	if err != nil {
		return nil, err
	}
	if target.Type == "epic" {
		return nil, output.NewError("UNSUPPORTED_WORK_TYPE", "Epic workflow state is derived from its executable children.")
	}
	before := stateForTask(*target.Task, cfg.States)
	result := &TransitionWorkResult{
		Target: dependencyRef(target), BeforeState: before, AfterState: to,
		BeforeCompleted: target.Task.Completed, AfterCompleted: workflowstate.IsTerminal(to),
		AllowedNext: workflowstate.AllowedTransitions(to), Reason: opts.Reason, DryRun: opts.DryRun, Noop: before == to,
	}
	if result.Noop {
		result.AfterCompleted = target.Task.Completed
		return result, nil
	}
	if !workflowstate.CanTransition(before, to) {
		return nil, output.NewErrorWithDetails("WORK_STATE_TRANSITION_INVALID", "The requested workflow transition is not allowed.", map[string]any{"from": before, "to": to, "allowed": workflowstate.AllowedTransitions(before)})
	}
	if opts.DryRun {
		return result, nil
	}
	completed := workflowstate.IsTerminal(to)
	updated, err := s.asana().UpdateTask(ctx, resolved.Token, target.Task.GID, asana.UpdateTaskInput{Completed: &completed, CustomFields: map[string]string{cfg.States.FieldGID: cfg.States.Option(to)}})
	if err != nil {
		return nil, mapAsanaError(err, "Could not transition the work item.")
	}
	updated, err = s.verifyTransition(ctx, resolved.Token, target.Task.GID, updated, cfg.States, to, completed)
	if err != nil {
		return nil, err
	}
	result.AfterCompleted = updated.Completed
	if opts.Reason != "" {
		text := "Dharana state transition: " + displayState(before) + " → " + workflowstate.DisplayName(to) + "\n\nReason: " + opts.Reason
		if _, err := s.asana().AddStory(ctx, resolved.Token, target.Task.GID, text); err != nil {
			return result, output.NewErrorWithDetails("STATE_TRANSITION_PARTIAL", "The state transition succeeded but its reason comment could not be recorded.", map[string]string{"gid": target.Task.GID, "state": to, "recovery": "dharana work comment " + target.Task.GID + " --body <reason> --json"})
		}
		result.ReasonRecorded = true
	}
	if !opts.SkipRefRefresh {
		if err := s.refreshRefsBestEffort(ctx); err == nil {
			result.RefreshedRefs = true
		}
	}
	return result, nil
}

func (s *Service) verifyTransition(ctx context.Context, token, gid string, updated *asana.Task, mapping config.StateMappings, expectedState string, expectedCompleted bool) (*asana.Task, error) {
	if transitionConverged(updated, mapping, expectedState, expectedCompleted) {
		return updated, nil
	}
	delays := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	var lastReadErr error
	for _, delay := range delays {
		if err := s.sleep(ctx, delay); err != nil {
			return nil, err
		}
		observed, err := s.asana().Task(ctx, token, gid)
		if err != nil {
			lastReadErr = err
			continue
		}
		updated = observed
		lastReadErr = nil
		if transitionConverged(updated, mapping, expectedState, expectedCompleted) {
			return updated, nil
		}
	}
	if lastReadErr != nil {
		return nil, output.NewErrorWithDetails("STATE_TRANSITION_VERIFY_FAILED", "The state mutation returned but authoritative verification failed.", map[string]string{"gid": gid, "expected_state": expectedState})
	}
	observedState := stateForTask(*updated, mapping)
	return nil, output.NewErrorWithDetails("STATE_TRANSITION_NOT_CONVERGED", "The remote work item did not converge to the requested state.", map[string]any{"gid": gid, "expected_state": expectedState, "observed_state": observedState, "expected_completed": expectedCompleted, "observed_completed": updated.Completed})
}

func transitionConverged(task *asana.Task, mapping config.StateMappings, expectedState string, expectedCompleted bool) bool {
	return task != nil && stateForTask(*task, mapping) == expectedState && task.Completed == expectedCompleted
}

func (s *Service) sleep(ctx context.Context, delay time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func stateForTask(task asana.Task, mapping config.StateMappings) string {
	if mapping.FieldGID == "" {
		return ""
	}
	for _, field := range task.CustomFields {
		if field.GID != mapping.FieldGID || field.EnumValue == nil {
			continue
		}
		for _, state := range workflowstate.Names() {
			if mapping.Option(state) == field.EnumValue.GID {
				return state
			}
		}
		if state, ok := workflowstate.Normalize(field.EnumValue.Name); ok {
			return state
		}
	}
	return ""
}

func displayState(value string) string {
	if value == "" {
		return "Unassigned"
	}
	if display := workflowstate.DisplayName(value); display != "" {
		return display
	}
	return value
}
