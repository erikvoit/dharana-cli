package capabilities

import (
	"sort"
	"strings"
)

const SchemaVersion = "mvp-plus-4"

var (
	CLIVersion = "0.5.0-dev"
	Commit     = "unknown"
	BuildTime  = "unknown"
)

type VersionResult struct {
	Version                 string            `json:"version"`
	CapabilitySchemaVersion string            `json:"capability_schema_version"`
	Build                   map[string]string `json:"build,omitempty"`
}

type Result struct {
	SchemaVersion string       `json:"schema_version"`
	CLIVersion    string       `json:"cli_version"`
	Commands      []Command    `json:"commands"`
	ErrorCodes    []ErrorCode  `json:"error_codes"`
	NextCommands  []Suggestion `json:"suggested_next_commands,omitempty"`
}

type Command struct {
	Name                 string       `json:"name"`
	Summary              string       `json:"summary"`
	Arguments            []Argument   `json:"arguments,omitempty"`
	Flags                []Flag       `json:"flags,omitempty"`
	RequiresAuth         bool         `json:"requires_auth"`
	RequiresProject      bool         `json:"requires_project"`
	ReadsRemote          bool         `json:"reads_remote"`
	MutatesRemote        bool         `json:"mutates_remote"`
	MutatesLocal         bool         `json:"mutates_local"`
	OutputOperation      string       `json:"output_operation"`
	SupportsDryRun       bool         `json:"supports_dry_run,omitempty"`
	SupportsIdempotency  bool         `json:"supports_idempotency,omitempty"`
	SupportsConfirmation bool         `json:"supports_confirmation,omitempty"`
	RequiredScopes       []string     `json:"required_scopes,omitempty"`
	NextCommands         []Suggestion `json:"suggested_next_commands,omitempty"`
}

type Argument struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Variadic bool   `json:"variadic,omitempty"`
}

type Flag struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Required bool   `json:"required,omitempty"`
	Repeat   bool   `json:"repeat,omitempty"`
}

type ErrorCode struct {
	Code       string   `json:"code"`
	Meaning    string   `json:"meaning"`
	Recoveries []string `json:"suggested_recoveries,omitempty"`
}

type Suggestion struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

func Version(build map[string]string) VersionResult {
	if build == nil {
		build = map[string]string{"commit": Commit, "built_at": BuildTime}
	}
	return VersionResult{Version: CLIVersion, CapabilitySchemaVersion: SchemaVersion, Build: build}
}

func All() Result {
	commands := allCommands()
	sort.SliceStable(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return Result{
		SchemaVersion: SchemaVersion,
		CLIVersion:    CLIVersion,
		Commands:      commands,
		ErrorCodes:    stableErrors(),
		NextCommands: []Suggestion{
			{Command: "dharana auth configure --token <pat> --validate --json", Reason: "Configure authentication before remote inspection or mutation."},
			{Command: "dharana project adopt <name-or-gid> --dry-run --json", Reason: "Inspect and preview configuration for an existing project."},
			{Command: "dharana doctor --repair-plan --json", Reason: "Validate the effective configuration and view safe repairs."},
		},
	}
}

func Find(command string) (Command, bool) {
	for _, cmd := range allCommands() {
		if cmd.Name == command {
			return cmd, true
		}
	}
	return Command{}, false
}

func allCommands() []Command {
	commands := []Command{
		cmd("auth configure", "Store an Asana personal access token.", true, false, false, false, true, "auth.configure", []Flag{{Name: "token", Value: "pat"}, {Name: "stdin"}, {Name: "validate"}, {Name: "json"}}),
		cmd("auth login", "Authorize an OAuth profile with PKCE.", false, false, false, false, true, "auth.login", []Flag{{Name: "profile", Value: "name", Required: true}, {Name: "scope", Value: "scope", Repeat: true}, {Name: "no-browser"}, {Name: "timeout", Value: "duration"}, {Name: "json"}}),
		cmd("auth logout", "Remove local profile credentials and optionally revoke OAuth authorization.", false, false, false, false, true, "auth.logout", []Flag{{Name: "profile", Value: "name", Required: true}, {Name: "revoke"}, {Name: "json"}}),
		cmd("auth profile list", "List authentication profiles without secrets.", false, false, false, false, false, "auth.profile.list", []Flag{{Name: "json"}}),
		cmd("auth profile add-env", "Create metadata for the validated environment-token identity.", false, false, true, false, true, "auth.profile.add_env", []Flag{{Name: "json"}}),
		cmd("auth profile delete", "Preview or delete a profile and report affected contexts.", false, false, false, false, true, "auth.profile.delete", []Flag{{Name: "dry-run"}, {Name: "apply"}, {Name: "json"}}),
		cmd("auth profile show", "Show authentication profile metadata.", false, false, false, false, false, "auth.profile.show", []Flag{{Name: "json"}}),
		cmd("auth profile use", "Select the default authentication profile.", false, false, false, false, true, "auth.profile.use", []Flag{{Name: "json"}}),
		cmd("auth refresh", "Refresh an OAuth profile credential.", false, false, true, false, true, "auth.refresh", []Flag{{Name: "profile", Value: "name", Required: true}, {Name: "json"}}),
		cmd("auth scopes", "Inspect effective OAuth scopes.", true, false, false, false, false, "auth.scopes", []Flag{{Name: "profile", Value: "name"}, {Name: "json"}}),
		cmd("auth status", "Report configured token source without exposing secrets.", false, false, false, false, false, "auth.status", []Flag{{Name: "json"}}),
		cmd("auth validate", "Validate configured authentication against Asana.", true, false, true, false, false, "auth.validate", []Flag{{Name: "json"}}),
		cmd("capabilities", "Return machine-readable command metadata.", false, false, false, false, false, "capabilities", []Flag{{Name: "json"}}),
		cmd("config show", "Show local configuration.", false, false, false, false, false, "config.show", []Flag{{Name: "json"}}),
		cmd("config set-fields", "Configure optional field mappings.", false, false, false, false, true, "config.set_fields", []Flag{{Name: "priority-gid", Value: "gid"}, {Name: "component-gid", Value: "gid"}, {Name: "json"}}),
		cmd("config set-task-types", "Configure Epic, Story, Bug, and Spike type mappings.", false, false, false, false, true, "config.set_task_types", []Flag{{Name: "field-gid", Value: "gid"}, {Name: "epic", Value: "value", Required: true}, {Name: "story", Value: "value", Required: true}, {Name: "bug", Value: "value", Required: true}, {Name: "spike", Value: "value", Required: true}, {Name: "json"}}),
		cmd("context create", "Create or update a named project context.", true, false, true, false, true, "context.create", []Flag{{Name: "project", Value: "gid", Required: true}, {Name: "json"}}),
		cmd("context list", "List named project contexts.", false, false, false, false, false, "context.list", []Flag{{Name: "json"}}),
		mutatingCommand("context reconcile", "Detect and refresh stale local context/cache state.", true, false, true, "context.reconcile", []Flag{{Name: "apply"}}),
		cmd("context show", "Show effective project context resolution.", false, false, false, false, false, "context.show", []Flag{{Name: "json"}}),
		cmd("context use", "Select a named project context.", false, false, false, false, true, "context.use", []Flag{{Name: "json"}}),
		cmd("doctor", "Run configuration diagnostics.", true, false, true, false, false, "doctor", []Flag{{Name: "repair-plan"}, {Name: "repair"}, {Name: "dry-run"}, {Name: "json"}}),
		mutatingCreate("epic create", "Create an epic as a top-level project task.", "epic.create", []Flag{{Name: "notes", Value: "text"}}),
		mutatingCreate("story create", "Create a story under an epic.", "story.create", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "notes", Value: "text"}}),
		mutatingCreate("bug create", "Create a bug under an epic.", "bug.create", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "priority", Value: "value"}, {Name: "environment", Value: "value"}, {Name: "notes", Value: "text"}}),
		mutatingCreate("spike create", "Create a time-boxed spike under an epic.", "spike.create", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "timebox", Value: "value"}, {Name: "notes", Value: "text"}}),
		mutatingCreate("task create", "Create an implementation task under story, bug, or spike work.", "task.create", []Flag{{Name: "parent", Value: "ref", Required: true}, {Name: "assignee", Value: "value"}, {Name: "due-on", Value: "date"}, {Name: "estimate", Value: "value"}, {Name: "notes", Value: "text"}}),
		cmd("dependency add", "Mark one item as blocked by another.", true, true, true, true, false, "dependency.add", []Flag{{Name: "blocked-by", Value: "ref", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("dependency list", "List blockers and direct dependents for one work item.", true, true, true, false, false, "dependency.list", []Flag{{Name: "json"}}),
		cmd("dependency remove", "Remove a blocker relationship.", true, true, true, true, false, "dependency.remove", []Flag{{Name: "blocked-by", Value: "ref", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("field list", "List fields attached to the selected project.", true, true, true, false, false, "field.list", []Flag{{Name: "json"}}),
		cmd("help", "Return human or JSON command help.", false, false, false, false, false, "help", []Flag{{Name: "json"}}),
		cmd("migrate apply", "Apply local schema migrations with recoverable backups.", false, false, false, false, true, "migrate.apply", []Flag{{Name: "dry-run"}, {Name: "json"}}),
		cmd("migrate status", "Inspect local schema migration requirements.", false, false, false, false, false, "migrate.status", []Flag{{Name: "json"}}),
		func() Command {
			c := cmd("plan validate", "Validate a versioned EpicPlan locally or against the selected project.", false, false, true, false, false, "plan.validate", []Flag{{Name: "remote"}, {Name: "json"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		cmd("plan schema", "Return the canonical JSON Schema for EpicPlan manifests.", false, false, false, false, false, "plan.schema", []Flag{{Name: "json"}}),
		func() Command {
			c := cmd("plan diff", "Compare an EpicPlan with authoritative Asana state.", true, true, true, false, false, "plan.diff", []Flag{{Name: "json"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		func() Command {
			c := mutatingCommand("plan adopt", "Persist unambiguous exact-match plan bindings.", true, false, true, "plan.adopt", []Flag{{Name: "apply"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		func() Command {
			c := mutatingCommand("plan apply", "Apply a desired EpicPlan in dependency-aware order.", true, true, true, "plan.apply", nil)
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		func() Command {
			c := cmd("plan status", "Report converged, drifted, conflicted, partial, or invalid plan state.", true, true, true, false, false, "plan.status", []Flag{{Name: "json"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		func() Command {
			c := mutatingCommand("plan reconcile", "Reconcile managed drift under the manifest removal policy.", true, true, true, "plan.reconcile", []Flag{{Name: "apply"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		cmd("plan export", "Export an existing epic graph and persist durable logical bindings.", true, true, true, false, true, "plan.export", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "output", Value: "file", Required: true}, {Name: "json"}}),
		func() Command {
			c := cmd("plan bindings", "Inspect durable logical-ID-to-Asana-GID bindings.", false, true, false, false, false, "plan.bindings", []Flag{{Name: "json"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		func() Command {
			c := mutatingCommand("plan bind", "Preview or replace one durable plan binding after remote verification.", true, false, true, "plan.bind", []Flag{{Name: "id", Value: "logical-id", Required: true}, {Name: "gid", Value: "asana-gid", Required: true}, {Name: "apply"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		func() Command {
			c := mutatingCommand("plan unbind", "Preview or remove one durable plan binding without deleting remote work.", true, false, true, "plan.unbind", []Flag{{Name: "id", Value: "logical-id", Required: true}, {Name: "apply"}})
			c.Arguments = []Argument{{Name: "file", Required: true}}
			return c
		}(),
		cmd("project adopt", "Preview or apply local configuration for a compatible existing project.", true, false, true, false, true, "project.adopt", []Flag{{Name: "dry-run"}, {Name: "apply"}, {Name: "context", Value: "name"}, {Name: "json"}}),
		cmd("project create", "Create a blank Asana project when API permissions allow it.", true, false, true, true, true, "project.create", []Flag{{Name: "workspace", Value: "gid", Required: true}, {Name: "team", Value: "gid"}, {Name: "privacy", Value: "private|team"}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("project create-from-template", "Instantiate an Asana project template when API permissions allow it.", true, false, true, true, true, "project.create_from_template", []Flag{{Name: "name", Value: "name", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("project inspect", "Inspect workflow readiness for a project.", true, false, true, false, false, "project.inspect", []Flag{{Name: "json"}}),
		cmd("project list", "List Asana projects.", true, false, true, false, false, "project.list", []Flag{{Name: "workspace-gid", Value: "gid"}, {Name: "json"}}),
		cmd("project member add", "Add an existing Asana user to the selected project.", true, true, true, true, false, "project.member.add", []Flag{{Name: "user", Value: "email-or-gid", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("project member list", "List selected-project members.", true, true, true, false, false, "project.member.list", []Flag{{Name: "json"}}),
		cmd("project member remove", "Remove an Asana user from the selected project.", true, true, true, true, false, "project.member.remove", []Flag{{Name: "user", Value: "gid", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("project select", "Select active project by GID or exact name.", true, false, true, false, true, "project.select", []Flag{{Name: "gid", Value: "gid"}, {Name: "name", Value: "exact-name"}, {Name: "workspace-gid", Value: "gid"}, {Name: "json"}}),
		cmd("refs refresh", "Refresh the selected project's friendly-reference cache.", true, true, true, false, true, "refs.refresh", []Flag{{Name: "limit", Value: "n"}, {Name: "json"}}),
		cmd("refs resolve", "Resolve one cached friendly reference.", true, true, true, false, false, "refs.resolve", []Flag{{Name: "json"}}),
		cmd("type list", "List detected Dharana work type mappings.", true, true, true, false, false, "type.list", []Flag{{Name: "json"}}),
		cmd("upgrade check", "Report current build and advisory upgrade status.", false, false, false, false, false, "upgrade.check", []Flag{{Name: "offline"}, {Name: "json"}}),
		cmd("version", "Return CLI and capability schema version.", false, false, false, false, false, "version", []Flag{{Name: "json"}}),
		cmd("workflow bind", "Bind an existing workflow mode where supported.", true, true, true, false, true, "workflow.bind", []Flag{{Name: "mode", Value: "native-types|custom-fields", Required: true}, {Name: "json"}}),
		cmd("workflow inspect", "Inspect selected-project workflow configuration.", true, true, true, false, false, "workflow.inspect", []Flag{{Name: "json"}}),
		cmd("workflow provision", "Preview or apply safe workflow provisioning.", true, true, true, true, true, "workflow.provision", []Flag{{Name: "mode", Value: "custom-fields|native-types", Required: true}, {Name: "dry-run"}, {Name: "apply"}, {Name: "json"}}),
		mutatingWork("work assign", "Assign one work item to an accessible Asana user.", true, true, "work.assign", []Flag{{Name: "assignee", Value: "email-or-gid", Required: true}}),
		cmd("work blocked", "List blocked work.", true, true, true, false, false, "work.blocked", []Flag{{Name: "type", Value: "type", Repeat: true}, {Name: "epic", Value: "ref"}, {Name: "json"}}),
		mutatingWork("work comment", "Append a plain-text execution or handoff note.", true, true, "work.comment", []Flag{{Name: "body", Value: "text", Required: true}}),
		mutatingWork("work complete", "Mark one supported work item completed.", true, true, "work.complete", nil),
		cmd("work get", "Retrieve one authoritative work item and execution context.", true, true, true, false, false, "work.get", []Flag{{Name: "json"}}),
		cmd("work graph", "Export dependency graph.", true, true, true, false, false, "work.graph", []Flag{{Name: "epic", Value: "ref"}, {Name: "format", Value: "json|mermaid"}, {Name: "json"}}),
		cmd("work list", "List project work.", true, true, true, false, false, "work.list", []Flag{{Name: "type", Value: "type", Repeat: true}, {Name: "status", Value: "status"}, {Name: "epic", Value: "ref"}, {Name: "limit", Value: "n"}, {Name: "offset", Value: "offset"}, {Name: "json"}}),
		mutatingWork("work move", "Move supported work under a valid parent.", true, true, "work.move", []Flag{{Name: "parent", Value: "ref", Required: true}}),
		cmd("work ready", "List actionable unblocked work.", true, true, true, false, false, "work.ready", []Flag{{Name: "type", Value: "type", Repeat: true}, {Name: "priority", Value: "value", Repeat: true}, {Name: "component", Value: "value", Repeat: true}, {Name: "epic", Value: "ref"}, {Name: "json"}}),
		func() Command {
			c := mutatingCommand("work reconcile", "Detect and repair stale local work references.", true, true, true, "work.reconcile", []Flag{{Name: "apply"}})
			c.Arguments = []Argument{{Name: "ref", Required: false, Variadic: true}}
			return c
		}(),
		mutatingWork("work reopen", "Reopen one completed supported work item.", true, true, "work.reopen", nil),
		mutatingWork("work schedule", "Set or clear one work item's due date.", true, true, "work.schedule", []Flag{{Name: "due-on", Value: "YYYY-MM-DD"}, {Name: "clear-due-on"}}),
		cmd("work tree", "Show hierarchy tree.", true, true, true, false, false, "work.tree", []Flag{{Name: "epic", Value: "ref"}, {Name: "json"}}),
		mutatingWork("work unassign", "Clear one work item's assignee.", true, true, "work.unassign", nil),
		mutatingWork("work update", "Update supported work properties.", true, true, "work.update", []Flag{{Name: "name", Value: "name"}, {Name: "notes", Value: "text"}, {Name: "description-file", Value: "markdown-file"}, {Name: "assignee", Value: "email-or-gid"}, {Name: "clear-assignee"}, {Name: "due-on", Value: "YYYY-MM-DD"}, {Name: "clear-due-on"}, {Name: "priority", Value: "value"}, {Name: "component", Value: "value"}}),
	}
	for i := range commands {
		commands[i].RequiredScopes = requiredScopes(commands[i])
	}
	return commands
}

func requiredScopes(command Command) []string {
	if !command.ReadsRemote && !command.MutatesRemote {
		return nil
	}
	values := []string{}
	name := command.Name
	switch {
	case strings.HasPrefix(name, "project ") || strings.HasPrefix(name, "context ") || strings.HasPrefix(name, "workflow ") || name == "doctor":
		values = append(values, "projects:read", "tasks:read", "users:read", "workspaces:read", "custom_fields:read")
	case strings.HasPrefix(name, "field ") || strings.HasPrefix(name, "type "):
		values = append(values, "projects:read", "custom_fields:read")
	default:
		values = append(values, "projects:read", "tasks:read", "users:read")
	}
	if command.MutatesRemote {
		if strings.HasPrefix(name, "project ") {
			values = append(values, "projects:write")
		}
		if strings.HasPrefix(name, "workflow ") {
			values = append(values, "custom_fields:write", "projects:write")
		}
		if strings.HasPrefix(name, "work ") || strings.HasPrefix(name, "plan ") || strings.HasPrefix(name, "dependency ") || strings.Contains(name, " create") {
			values = append(values, "tasks:write", "stories:write")
		}
	}
	sort.Strings(values)
	return compact(values)
}

func compact(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func cmd(name, summary string, auth, project, readsRemote, mutatesRemote, mutatesLocal bool, operation string, flags []Flag) Command {
	return Command{Name: name, Summary: summary, Flags: flags, RequiresAuth: auth, RequiresProject: project, ReadsRemote: readsRemote, MutatesRemote: mutatesRemote, MutatesLocal: mutatesLocal, OutputOperation: operation}
}

func mutatingCreate(name, summary, operation string, flags []Flag) Command {
	flags = append(flags, Flag{Name: "description-file", Value: "markdown-file"}, Flag{Name: "dry-run"}, Flag{Name: "idempotent"}, Flag{Name: "idempotency-key", Value: "key"}, Flag{Name: "json"})
	c := cmd(name, summary, true, true, true, true, false, operation, flags)
	c.Arguments = []Argument{{Name: "name", Required: true, Variadic: true}}
	c.SupportsDryRun = true
	c.SupportsIdempotency = true
	return c
}

func mutatingWork(name, summary string, remote bool, local bool, operation string, flags []Flag) Command {
	flags = append(flags, Flag{Name: "dry-run"}, Flag{Name: "json"})
	c := cmd(name, summary, true, true, true, remote, local, operation, flags)
	c.Arguments = []Argument{{Name: "ref", Required: true, Variadic: true}}
	c.SupportsDryRun = true
	c.SupportsIdempotency = true
	return c
}

func mutatingCommand(name, summary string, project bool, remote bool, local bool, operation string, flags []Flag) Command {
	flags = append(flags, Flag{Name: "dry-run"}, Flag{Name: "json"})
	c := cmd(name, summary, true, project, true, remote, local, operation, flags)
	c.SupportsDryRun = true
	c.SupportsIdempotency = true
	return c
}

func stableErrors() []ErrorCode {
	return []ErrorCode{
		{Code: "TOKEN_NOT_CONFIGURED", Meaning: "No token could be resolved.", Recoveries: []string{"dharana auth configure --token <pat> --validate --json"}},
		{Code: "INVALID_AUTH", Meaning: "Asana rejected the configured token.", Recoveries: []string{"dharana auth configure --token <pat> --validate --json"}},
		{Code: "AUTH_PROFILE_NOT_FOUND", Meaning: "The explicitly selected authentication profile does not exist.", Recoveries: []string{"dharana auth profile list --json"}},
		{Code: "AUTH_CONTEXT_MISMATCH", Meaning: "The effective profile or user does not own the active project context.", Recoveries: []string{"dharana context show --json", "dharana auth profile use <name> --json"}},
		{Code: "OAUTH_CLIENT_NOT_CONFIGURED", Meaning: "Required OAuth application configuration is missing.", Recoveries: []string{"Set the documented DHARANA_ASANA_OAUTH_* environment variables."}},
		{Code: "OAUTH_SCOPES_MISSING", Meaning: "The OAuth profile lacks scopes required by the command.", Recoveries: []string{"Reauthorize the same profile with the reported complete scope set."}},
		{Code: "OAUTH_REFRESH_FAILED", Meaning: "The OAuth credential could not be refreshed.", Recoveries: []string{"dharana auth login --profile <name> --json"}},
		{Code: "OAUTH_AUTHORIZATION_REVOKED", Meaning: "The OAuth refresh grant is no longer authorized.", Recoveries: []string{"dharana auth login --profile <name> --json"}},
		{Code: "OAUTH_AUTHORIZATION_EXPIRED", Meaning: "The OAuth authorization expired and must be granted again.", Recoveries: []string{"dharana auth login --profile <name> --json"}},
		{Code: "OAUTH_REFRESH_NETWORK_FAILED", Meaning: "The OAuth token endpoint could not be reached.", Recoveries: []string{"Retry without changing profiles after network access is restored."}},
		{Code: "MIGRATION_UNSUPPORTED", Meaning: "Local state cannot be migrated by this CLI version.", Recoveries: []string{"Install a compatible newer Dharana release."}},
		{Code: "STATE_MIGRATION_REQUIRED", Meaning: "Local state uses an older supported schema and must be migrated explicitly.", Recoveries: []string{"dharana migrate status --json", "dharana migrate apply --dry-run --json"}},
		{Code: "PROJECT_NOT_CONFIGURED", Meaning: "No project context could be resolved.", Recoveries: []string{"dharana context list --json", "dharana project adopt <name-or-gid> --apply --json"}},
		{Code: "AMBIGUOUS_PROJECT", Meaning: "A project name matched multiple projects.", Recoveries: []string{"Retry with a GID or workspace-limited exact name."}},
		{Code: "TASK_TYPES_NOT_CONFIGURED", Meaning: "Required work type mappings are missing.", Recoveries: []string{"dharana project adopt <project> --apply --json", "dharana workflow provision --mode custom-fields --dry-run --json"}},
		{Code: "ASANA_ACCESS_DENIED", Meaning: "The token cannot access or mutate the target resource.", Recoveries: []string{"Confirm Asana project/workspace permissions."}},
		{Code: "ASANA_RATE_LIMITED", Meaning: "Asana returned a rate-limit response; details may include Retry-After.", Recoveries: []string{"Wait for the returned retry_after value, then re-read current work state before retrying."}},
		{Code: "ASANA_VALIDATION_FAILED", Meaning: "Asana rejected the requested mutation as invalid.", Recoveries: []string{"Inspect the returned command result and project workflow configuration."}},
		{Code: "ASANA_CONFLICT", Meaning: "Asana reported conflicting remote state.", Recoveries: []string{"dharana work get <ref> --json", "Retry only after confirming current state."}},
		{Code: "ASANA_TRANSIENT_FAILURE", Meaning: "Asana returned a server-side transient failure.", Recoveries: []string{"Re-read current state, then retry idempotent or dry-run-verifiable operations."}},
		{Code: "UNSUPPORTED_PROVISIONING", Meaning: "The requested provisioning path is not safely supported by the current API/account.", Recoveries: []string{"Follow remediation steps returned in the result."}},
		{Code: "STALE_REFERENCE", Meaning: "A cached friendly reference no longer resolves to live Asana work.", Recoveries: []string{"dharana work reconcile <ref> --dry-run --json", "dharana refs refresh --json"}},
		{Code: "INVALID_DUE_ON", Meaning: "A due date was not in YYYY-MM-DD format.", Recoveries: []string{"Retry with --due-on YYYY-MM-DD."}},
		{Code: "INVALID_FIELD_VALUE", Meaning: "A configured field value did not match an enabled enum option.", Recoveries: []string{"dharana field list --json", "dharana workflow inspect --json"}},
		{Code: "MOVE_PARTIAL_FAILURE", Meaning: "A multi-step move partially failed after returning authoritative identifiers.", Recoveries: []string{"dharana work reconcile <ref> --dry-run --json"}},
		{Code: "PLAN_INVALID", Meaning: "The EpicPlan failed local or remote validation.", Recoveries: []string{"dharana plan validate <file> --remote --json"}},
		{Code: "PLAN_CONFLICT", Meaning: "Remote, bound, and desired plan state cannot be reconciled safely without direction.", Recoveries: []string{"dharana plan diff <file> --json", "Resolve the reported conflict, then retry."}},
		{Code: "PLAN_PARTIAL_APPLY", Meaning: "Some plan operations succeeded before a later operation failed.", Recoveries: []string{"dharana plan status <file> --json", "dharana plan reconcile <file> --dry-run --json"}},
		{Code: "PLAN_NOT_CONVERGED", Meaning: "Plan operations completed but authoritative verification still found drift.", Recoveries: []string{"dharana plan diff <file> --json"}},
		{Code: "PLAN_VERIFY_FAILED", Meaning: "Plan operations completed but authoritative convergence could not be verified.", Recoveries: []string{"dharana plan status <file> --json"}},
		{Code: "PLAN_INACCESSIBLE", Meaning: "Authoritative plan status could not be read.", Recoveries: []string{"Verify authentication, project access, and the selected plan target."}},
		{Code: "PLAN_ADOPTION_CONFLICT", Meaning: "Existing work could not be adopted without an ambiguous or stale identity decision.", Recoveries: []string{"dharana plan adopt <file> --dry-run --json", "dharana plan bind <file> --id <logical-id> --gid <gid> --dry-run --json"}},
		{Code: "BINDING_READ_FAILED", Meaning: "Durable manifest bindings could not be read or did not match the selected project.", Recoveries: []string{"Inspect the returned binding path and project identity."}},
		{Code: "BINDING_WRITE_FAILED", Meaning: "Bindings could not be committed atomically after an operation.", Recoveries: []string{"Preserve returned GIDs and retry plan reconciliation after fixing local storage."}},
		{Code: "BINDING_LOCK_FAILED", Meaning: "Another process retained the project-scoped manifest binding lock past the safe wait period.", Recoveries: []string{"Wait for the other plan operation to finish, then retry."}},
		{Code: "BINDING_NOT_FOUND", Meaning: "The requested logical ID has no durable binding.", Recoveries: []string{"dharana plan bindings <file> --json"}},
		{Code: "BINDING_TARGET_NOT_FOUND", Meaning: "The requested replacement GID is not in the selected project graph.", Recoveries: []string{"Verify the target project and GID, then preview the binding again."}},
		{Code: "BINDING_TYPE_MISMATCH", Meaning: "A replacement GID resolves to a different Dharana work type.", Recoveries: []string{"Choose an object with the manifest node's expected type."}},
		{Code: "DESCRIPTION_FILE_READ_FAILED", Meaning: "A Markdown description file could not be read.", Recoveries: []string{"Verify the path and local file permissions."}},
		{Code: "DESCRIPTION_EXPORT_FAILED", Meaning: "Provider rich text could not be parsed safely during plan export.", Recoveries: []string{"Inspect the task description in Asana and retry after correcting malformed content."}},
		{Code: "DESCRIPTION_NOTES_CONFLICT", Meaning: "One operation attempted to manage both rich description and plain notes.", Recoveries: []string{"Use --description-file or --notes, not both."}},
		{Code: "INVALID_MARKDOWN_DESCRIPTION", Meaning: "A Markdown description uses an unsupported or unsafe construct.", Recoveries: []string{"Use headings, lists, emphasis, links, blockquotes, and code without raw HTML or images."}},
	}
}
