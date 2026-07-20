package automation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/workflowstate"
	"gopkg.in/yaml.v3"
)

const (
	PolicyAPIVersion = "dharana.dev/v1alpha1"
	PolicyKind       = "AutomationPolicy"
)

var supportedEvents = []string{"project.changed", "work.added", "work.changed", "work.completed", "work.deleted", "work.undeleted", "work.uncompleted"}
var supportedActions = []string{"comment", "complete", "emit", "reopen", "transition"}

type Policy struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind" yaml:"kind"`
	Metadata   PolicyMetadata `json:"metadata" yaml:"metadata"`
	Spec       PolicySpec     `json:"spec" yaml:"spec"`
	Source     string         `json:"-" yaml:"-"`
	Version    string         `json:"version" yaml:"-"`
}

type PolicyMetadata struct {
	ID string `json:"id" yaml:"id"`
}

type PolicySpec struct {
	Context          string            `json:"context" yaml:"context"`
	Mode             string            `json:"mode" yaml:"mode"`
	When             Trigger           `json:"when" yaml:"when"`
	Evaluate         EvaluationQuery   `json:"evaluate,omitempty" yaml:"evaluate,omitempty"`
	Actions          []Action          `json:"actions" yaml:"actions"`
	Permissions      PolicyPermissions `json:"permissions,omitempty" yaml:"permissions,omitempty"`
	FailureThreshold int               `json:"failureThreshold,omitempty" yaml:"failureThreshold,omitempty"`
}

type Trigger struct {
	Event string `json:"event" yaml:"event"`
}

type EvaluationQuery struct {
	Query   string              `json:"query,omitempty" yaml:"query,omitempty"`
	Filters map[string][]string `json:"filters,omitempty" yaml:"filters,omitempty"`
}

type Action struct {
	ID     string `json:"id,omitempty" yaml:"id,omitempty"`
	Type   string `json:"type" yaml:"type"`
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
	Body   string `json:"body,omitempty" yaml:"body,omitempty"`
	State  string `json:"state,omitempty" yaml:"state,omitempty"`
}

type PolicyPermissions struct {
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

type Finding struct {
	Code        string `json:"code"`
	Path        string `json:"path"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type ValidationResult struct {
	Valid    bool      `json:"valid"`
	PolicyID string    `json:"policy_id,omitempty"`
	Version  string    `json:"policy_version,omitempty"`
	Findings []Finding `json:"findings"`
}

type RuntimeCapabilities struct {
	SchemaVersion    string   `json:"schema_version"`
	PolicyAPIVersion string   `json:"policy_api_version"`
	Events           []string `json:"events"`
	Actions          []string `json:"actions"`
	Modes            []string `json:"modes"`
	Queries          []string `json:"queries"`
	Operators        []string `json:"operators"`
}

func Capabilities() RuntimeCapabilities {
	return RuntimeCapabilities{SchemaVersion: "1", PolicyAPIVersion: PolicyAPIVersion, Events: append([]string(nil), supportedEvents...), Actions: append([]string(nil), supportedActions...), Modes: []string{"report", "apply"}, Queries: []string{"event.resource", "work.ready"}, Operators: []string{"equals", "in"}}
}

func ParseFile(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, output.NewError("AUTOMATION_POLICY_READ_FAILED", "Could not read the automation policy.")
	}
	policy, err := Parse(data)
	if err != nil {
		return nil, err
	}
	policy.Source = path
	return policy, nil
}

func Parse(data []byte) (*Policy, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return nil, output.NewError("AUTOMATION_POLICY_PARSE_FAILED", "The automation policy is not valid YAML or JSON.")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, output.NewError("AUTOMATION_POLICY_MULTIDOC_UNSUPPORTED", "Use one automation policy per file.")
	}
	normalized, _ := yaml.Marshal(policy)
	digest := sha256.Sum256(normalized)
	policy.Version = hex.EncodeToString(digest[:12])
	return &policy, nil
}

func Validate(policy *Policy) ValidationResult {
	result := ValidationResult{Findings: []Finding{}}
	if policy == nil {
		result.Findings = append(result.Findings, finding("AUTOMATION_POLICY_REQUIRED", "$", "Provide an automation policy.", "Pass a readable YAML or JSON policy file."))
		return result
	}
	result.PolicyID, result.Version = policy.Metadata.ID, policy.Version
	if policy.APIVersion != PolicyAPIVersion {
		result.Findings = append(result.Findings, finding("AUTOMATION_POLICY_VERSION_UNSUPPORTED", "$.apiVersion", "Unsupported automation policy API version.", "Use "+PolicyAPIVersion+"."))
	}
	if policy.Kind != PolicyKind {
		result.Findings = append(result.Findings, finding("AUTOMATION_POLICY_KIND_INVALID", "$.kind", "Policy kind must be AutomationPolicy.", "Set kind to AutomationPolicy."))
	}
	if strings.TrimSpace(policy.Metadata.ID) == "" {
		result.Findings = append(result.Findings, finding("AUTOMATION_POLICY_ID_REQUIRED", "$.metadata.id", "Policy metadata.id is required.", "Choose a stable logical policy ID."))
	}
	if strings.TrimSpace(policy.Spec.Context) == "" {
		result.Findings = append(result.Findings, finding("AUTOMATION_POLICY_CONTEXT_REQUIRED", "$.spec.context", "Policy context is required.", "Name one configured Dharana context."))
	}
	if policy.Spec.Mode != "report" && policy.Spec.Mode != "apply" {
		result.Findings = append(result.Findings, finding("AUTOMATION_POLICY_MODE_INVALID", "$.spec.mode", "Policy mode must be report or apply.", "Use report for proposals or apply for explicitly authorized mutation."))
	}
	if !slices.Contains(supportedEvents, policy.Spec.When.Event) {
		result.Findings = append(result.Findings, finding("AUTOMATION_EVENT_UNSUPPORTED", "$.spec.when.event", "Policy trigger event is unsupported.", "Choose an event exposed by automation capabilities."))
	}
	if policy.Spec.Evaluate.Query != "" && policy.Spec.Evaluate.Query != "event.resource" && policy.Spec.Evaluate.Query != "work.ready" {
		result.Findings = append(result.Findings, finding("AUTOMATION_QUERY_UNSUPPORTED", "$.spec.evaluate.query", "Policy query is unsupported.", "Use event.resource or work.ready."))
	}
	for key := range policy.Spec.Evaluate.Filters {
		if key != "type" && key != "priority" && key != "component" && key != "status" && key != "state" {
			result.Findings = append(result.Findings, finding("AUTOMATION_FILTER_UNSUPPORTED", "$.spec.evaluate.filters."+key, "Policy filter is unsupported.", "Use type, priority, component, status, or state."))
		}
		if policy.Spec.Evaluate.Query != "work.ready" && (key == "priority" || key == "component") {
			result.Findings = append(result.Findings, finding("AUTOMATION_FILTER_QUERY_MISMATCH", "$.spec.evaluate.filters."+key, "Priority and component filters require the work.ready query.", "Use work.ready so configured field mappings are applied authoritatively."))
		}
	}
	if len(policy.Spec.Actions) == 0 {
		result.Findings = append(result.Findings, finding("AUTOMATION_ACTION_REQUIRED", "$.spec.actions", "At least one action is required.", "Add an emit or supported work action."))
	}
	requiredScopes := map[string]bool{}
	seenActionIDs := map[string]bool{}
	for index, action := range policy.Spec.Actions {
		path := "$.spec.actions"
		if index >= 0 {
			path += "[" + strconv.Itoa(index) + "]"
		}
		if !slices.Contains(supportedActions, action.Type) {
			result.Findings = append(result.Findings, finding("AUTOMATION_ACTION_UNSUPPORTED", path+".type", "Policy action is unsupported.", "Choose an action exposed by automation capabilities."))
			continue
		}
		if action.ID != "" {
			if seenActionIDs[action.ID] {
				result.Findings = append(result.Findings, finding("AUTOMATION_ACTION_ID_DUPLICATE", path+".id", "Action IDs must be unique within a policy.", "Choose a unique action ID."))
			}
			seenActionIDs[action.ID] = true
		}
		if action.Type == "comment" && strings.TrimSpace(action.Body) == "" {
			result.Findings = append(result.Findings, finding("AUTOMATION_COMMENT_BODY_REQUIRED", path+".body", "Comment actions require a body.", "Set a deterministic plain-text comment body."))
		}
		if action.Type == "transition" {
			state, valid := workflowstate.Normalize(action.State)
			if !valid {
				result.Findings = append(result.Findings, finding("AUTOMATION_STATE_INVALID", path+".state", "Transition actions require a canonical target state.", "Choose a state exposed by work state-capabilities."))
			} else if policy.Spec.When.Event == "work.changed" && (len(policy.Spec.Evaluate.Filters["state"]) == 0 || matchesPolicyState(policy.Spec.Evaluate.Filters["state"], state)) {
				result.Findings = append(result.Findings, finding("AUTOMATION_RECURSIVE_ACTION", path+".state", "This transition can directly match the state-change event it produces.", "Filter work.changed by source states that exclude the transition target."))
			}
		}
		if policy.Spec.Evaluate.Query == "work.ready" && action.Type != "emit" && action.Target != "query.matches" {
			result.Findings = append(result.Findings, finding("AUTOMATION_ACTION_TARGET_REQUIRED", path+".target", "Mutating work.ready actions must explicitly target query.matches.", "Set target to query.matches so the deterministic match set is explicit."))
		}
		if action.Type == "complete" || action.Type == "reopen" || action.Type == "transition" {
			requiredScopes["tasks:read"] = true
			requiredScopes["tasks:write"] = true
		}
		if action.Type == "comment" {
			requiredScopes["tasks:read"] = true
			requiredScopes["stories:write"] = true
		}
		if (policy.Spec.When.Event == "work.completed" && action.Type == "complete") || (policy.Spec.When.Event == "work.uncompleted" && action.Type == "reopen") {
			result.Findings = append(result.Findings, finding("AUTOMATION_RECURSIVE_ACTION", path+".type", "The action can directly retrigger the same policy event.", "Use a different trigger or action."))
		}
	}
	if policy.Spec.FailureThreshold < 0 || policy.Spec.FailureThreshold > 100 {
		result.Findings = append(result.Findings, finding("AUTOMATION_FAILURE_THRESHOLD_INVALID", "$.spec.failureThreshold", "Failure threshold must be between 0 and 100.", "Use 0 to disable pausing or a bounded positive threshold."))
	}
	declared := append([]string(nil), policy.Spec.Permissions.Scopes...)
	sort.Strings(declared)
	for scope := range requiredScopes {
		if !slices.Contains(declared, scope) {
			result.Findings = append(result.Findings, finding("AUTOMATION_SCOPE_UNDECLARED", "$.spec.permissions.scopes", "Mutation action requires undeclared scope "+scope+".", "Declare every required OAuth scope explicitly."))
		}
	}
	result.Valid = len(result.Findings) == 0
	return result
}

func matchesPolicyState(values []string, target string) bool {
	for _, value := range values {
		if normalized, ok := workflowstate.Normalize(value); ok && normalized == target {
			return true
		}
	}
	return false
}

func finding(code, path, message, remediation string) Finding {
	return Finding{Code: code, Path: path, Message: message, Remediation: remediation}
}
