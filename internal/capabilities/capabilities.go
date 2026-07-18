package capabilities

import "sort"

const SchemaVersion = "mvp-plus-1"
const CLIVersion = "0.2.0"

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
	return []Command{
		cmd("auth configure", "Store an Asana personal access token.", true, false, false, false, true, "auth.configure", []Flag{{Name: "token", Value: "pat"}, {Name: "stdin"}, {Name: "validate"}, {Name: "json"}}),
		cmd("auth status", "Report configured token source without exposing secrets.", false, false, false, false, false, "auth.status", []Flag{{Name: "json"}}),
		cmd("auth validate", "Validate configured authentication against Asana.", true, false, true, false, false, "auth.validate", []Flag{{Name: "json"}}),
		cmd("capabilities", "Return machine-readable command metadata.", false, false, false, false, false, "capabilities", []Flag{{Name: "json"}}),
		cmd("config show", "Show local configuration.", false, false, false, false, false, "config.show", []Flag{{Name: "json"}}),
		cmd("config set-fields", "Configure optional field mappings.", false, false, false, false, true, "config.set_fields", []Flag{{Name: "priority-gid", Value: "gid"}, {Name: "component-gid", Value: "gid"}, {Name: "json"}}),
		cmd("config set-task-types", "Configure Epic, Story, Bug, and Spike type mappings.", false, false, false, false, true, "config.set_task_types", []Flag{{Name: "field-gid", Value: "gid"}, {Name: "epic", Value: "value", Required: true}, {Name: "story", Value: "value", Required: true}, {Name: "bug", Value: "value", Required: true}, {Name: "spike", Value: "value", Required: true}, {Name: "json"}}),
		cmd("context create", "Create or update a named project context.", true, false, true, false, true, "context.create", []Flag{{Name: "project", Value: "gid", Required: true}, {Name: "json"}}),
		cmd("context list", "List named project contexts.", false, false, false, false, false, "context.list", []Flag{{Name: "json"}}),
		cmd("context show", "Show effective project context resolution.", false, false, false, false, false, "context.show", []Flag{{Name: "json"}}),
		cmd("context use", "Select a named project context.", false, false, false, false, true, "context.use", []Flag{{Name: "json"}}),
		cmd("doctor", "Run configuration diagnostics.", true, false, true, false, false, "doctor", []Flag{{Name: "repair-plan"}, {Name: "repair"}, {Name: "dry-run"}, {Name: "json"}}),
		mutatingCreate("epic create", "Create an epic as a top-level project task.", "epic.create", []Flag{{Name: "notes", Value: "text"}}),
		mutatingCreate("story create", "Create a story under an epic.", "story.create", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "notes", Value: "text"}}),
		mutatingCreate("bug create", "Create a bug under an epic.", "bug.create", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "priority", Value: "value"}, {Name: "environment", Value: "value"}, {Name: "notes", Value: "text"}}),
		mutatingCreate("spike create", "Create a time-boxed spike under an epic.", "spike.create", []Flag{{Name: "epic", Value: "ref", Required: true}, {Name: "timebox", Value: "value"}, {Name: "notes", Value: "text"}}),
		mutatingCreate("task create", "Create an implementation task under story, bug, or spike work.", "task.create", []Flag{{Name: "parent", Value: "ref", Required: true}, {Name: "assignee", Value: "value"}, {Name: "due-on", Value: "date"}, {Name: "estimate", Value: "value"}, {Name: "notes", Value: "text"}}),
		cmd("dependency add", "Mark one item as blocked by another.", true, true, true, true, false, "dependency.add", []Flag{{Name: "blocked-by", Value: "ref", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("dependency remove", "Remove a blocker relationship.", true, true, true, true, false, "dependency.remove", []Flag{{Name: "blocked-by", Value: "ref", Required: true}, {Name: "dry-run"}, {Name: "json"}}),
		cmd("field list", "List fields attached to the selected project.", true, true, true, false, false, "field.list", []Flag{{Name: "json"}}),
		cmd("help", "Return human or JSON command help.", false, false, false, false, false, "help", []Flag{{Name: "json"}}),
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
		cmd("version", "Return CLI and capability schema version.", false, false, false, false, false, "version", []Flag{{Name: "json"}}),
		cmd("workflow bind", "Bind an existing workflow mode where supported.", true, true, true, false, true, "workflow.bind", []Flag{{Name: "mode", Value: "native-types|custom-fields", Required: true}, {Name: "json"}}),
		cmd("workflow inspect", "Inspect selected-project workflow configuration.", true, true, true, false, false, "workflow.inspect", []Flag{{Name: "json"}}),
		cmd("workflow provision", "Preview or apply safe workflow provisioning.", true, true, true, true, true, "workflow.provision", []Flag{{Name: "mode", Value: "custom-fields|native-types", Required: true}, {Name: "dry-run"}, {Name: "apply"}, {Name: "json"}}),
		cmd("work blocked", "List blocked work.", true, true, true, false, false, "work.blocked", []Flag{{Name: "type", Value: "type", Repeat: true}, {Name: "epic", Value: "ref"}, {Name: "json"}}),
		cmd("work graph", "Export dependency graph.", true, true, true, false, false, "work.graph", []Flag{{Name: "epic", Value: "ref"}, {Name: "format", Value: "json|mermaid"}, {Name: "json"}}),
		cmd("work list", "List project work.", true, true, true, false, false, "work.list", []Flag{{Name: "type", Value: "type", Repeat: true}, {Name: "status", Value: "status"}, {Name: "epic", Value: "ref"}, {Name: "limit", Value: "n"}, {Name: "offset", Value: "offset"}, {Name: "json"}}),
		cmd("work ready", "List actionable unblocked work.", true, true, true, false, false, "work.ready", []Flag{{Name: "type", Value: "type", Repeat: true}, {Name: "priority", Value: "value", Repeat: true}, {Name: "component", Value: "value", Repeat: true}, {Name: "epic", Value: "ref"}, {Name: "json"}}),
		cmd("work tree", "Show hierarchy tree.", true, true, true, false, false, "work.tree", []Flag{{Name: "epic", Value: "ref"}, {Name: "json"}}),
	}
}

func cmd(name, summary string, auth, project, readsRemote, mutatesRemote, mutatesLocal bool, operation string, flags []Flag) Command {
	return Command{Name: name, Summary: summary, Flags: flags, RequiresAuth: auth, RequiresProject: project, ReadsRemote: readsRemote, MutatesRemote: mutatesRemote, MutatesLocal: mutatesLocal, OutputOperation: operation}
}

func mutatingCreate(name, summary, operation string, flags []Flag) Command {
	flags = append(flags, Flag{Name: "dry-run"}, Flag{Name: "idempotent"}, Flag{Name: "idempotency-key", Value: "key"}, Flag{Name: "json"})
	c := cmd(name, summary, true, true, true, true, false, operation, flags)
	c.Arguments = []Argument{{Name: "name", Required: true, Variadic: true}}
	c.SupportsDryRun = true
	c.SupportsIdempotency = true
	return c
}

func stableErrors() []ErrorCode {
	return []ErrorCode{
		{Code: "TOKEN_NOT_CONFIGURED", Meaning: "No token could be resolved.", Recoveries: []string{"dharana auth configure --token <pat> --validate --json"}},
		{Code: "INVALID_AUTH", Meaning: "Asana rejected the configured token.", Recoveries: []string{"dharana auth configure --token <pat> --validate --json"}},
		{Code: "PROJECT_NOT_CONFIGURED", Meaning: "No project context could be resolved.", Recoveries: []string{"dharana context list --json", "dharana project adopt <name-or-gid> --apply --json"}},
		{Code: "AMBIGUOUS_PROJECT", Meaning: "A project name matched multiple projects.", Recoveries: []string{"Retry with a GID or workspace-limited exact name."}},
		{Code: "TASK_TYPES_NOT_CONFIGURED", Meaning: "Required work type mappings are missing.", Recoveries: []string{"dharana project adopt <project> --apply --json", "dharana workflow provision --mode custom-fields --dry-run --json"}},
		{Code: "ASANA_ACCESS_DENIED", Meaning: "The token cannot access or mutate the target resource.", Recoveries: []string{"Confirm Asana project/workspace permissions."}},
		{Code: "UNSUPPORTED_PROVISIONING", Meaning: "The requested provisioning path is not safely supported by the current API/account.", Recoveries: []string{"Follow remediation steps returned in the result."}},
	}
}
