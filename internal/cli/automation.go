package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/automation"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/syncer"
)

func (a *app) runSync(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printSyncUsage(stderr)
		return 2
	}
	command := args[0]
	fs := flag.NewFlagSet("sync "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var contextName string
	var jsonOut, dryRun, apply bool
	fs.StringVar(&contextName, "context", "", "Named context to synchronize")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if command == "reset" {
		fs.BoolVar(&dryRun, "dry-run", false, "Preview cursor reset without changing state")
		fs.BoolVar(&apply, "apply", false, "Reset the cursor so the next pull performs a bounded rebuild")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	service, err := a.syncServiceForContext(contextName)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	var result any
	switch command {
	case "status":
		result, err = service.Status(ctx)
	case "pull":
		result, err = service.Pull(ctx)
	case "reset":
		if dryRun && apply {
			err = output.NewError("SYNC_RESET_MODE_CONFLICT", "Use --dry-run or --apply, not both.")
		} else if !dryRun && !apply {
			err = output.NewError("SYNC_RESET_APPLY_REQUIRED", "Use --dry-run to preview or --apply to reset synchronization state.")
		} else {
			result, err = service.Reset(ctx, dryRun)
		}
	default:
		err = output.NewError("UNKNOWN_SYNC_COMMAND", "Use sync status, sync pull, or sync reset.")
	}
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "sync."+command, result)
		return 0
	}
	switch value := result.(type) {
	case *syncer.StatusResult:
		_, _ = fmt.Fprintf(stdout, "%s\t%s\tlast success %s\n", value.Scope.Context, value.CursorState, value.LastSuccessAt)
	case *syncer.PullResult:
		_, _ = fmt.Fprintf(stdout, "%s: %d event(s), %d resource(s) refreshed, rebuilt=%t.\n", value.Scope.Context, value.EventsObserved, len(value.ResourcesRefreshed), value.Rebuilt)
	case *syncer.ResetResult:
		_, _ = fmt.Fprintf(stdout, "%s: reset=%t dry-run=%t.\n", value.Scope.Context, value.Reset, value.DryRun)
	}
	return 0
}

func (a *app) runWatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var contextName, format string
	var interval, maxBackoff time.Duration
	var once bool
	fs.StringVar(&contextName, "context", "", "Named context to watch")
	fs.StringVar(&format, "format", "jsonl", "Output format: jsonl or human")
	fs.DurationVar(&interval, "interval", 30*time.Second, "Polling interval")
	fs.DurationVar(&maxBackoff, "max-backoff", 2*time.Minute, "Maximum bounded retry backoff")
	fs.BoolVar(&once, "once", false, "Pull one event batch and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if format != "jsonl" && format != "human" {
		writeCLIError(stderr, false, output.NewError("WATCH_FORMAT_INVALID", "Use --format jsonl or --format human."))
		return 2
	}
	service, err := a.syncServiceForContext(contextName)
	if err != nil {
		writeCLIError(stderr, format == "jsonl", err)
		return exitCodeForError(err)
	}
	encoder := json.NewEncoder(stdout)
	err = service.Watch(ctx, syncer.WatchOptions{Interval: interval, MaxBackoff: maxBackoff, Once: once}, func(record syncer.WatchRecord) error {
		if format == "jsonl" {
			return encoder.Encode(record)
		}
		switch {
		case record.Event != nil:
			_, err := fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", record.Event.ObservedAt, record.Event.Type, record.Event.ResourceType, record.Event.ResourceGID)
			return err
		default:
			_, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", record.ObservedAt, record.Type, record.Message)
			return err
		}
	})
	if err != nil {
		writeCLIError(stderr, format == "jsonl", err)
		return exitCodeForError(err)
	}
	return 0
}

func (a *app) runAutomation(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAutomationUsage(stderr)
		return 2
	}
	switch args[0] {
	case "validate":
		return a.runAutomationValidate(args[1:], stdout, stderr)
	case "capabilities":
		return a.runAutomationCapabilities(args[1:], stdout, stderr)
	case "run":
		return a.runAutomationRun(ctx, args[1:], stdout, stderr)
	case "history":
		return a.runAutomationHistory(args[1:], stdout, stderr)
	case "explain":
		return a.runAutomationExplain(args[1:], stdout, stderr)
	case "retry":
		return a.runAutomationRetry(ctx, args[1:], stdout, stderr)
	case "status":
		return a.runAutomationStatus(ctx, args[1:], stdout, stderr)
	case "doctor":
		return a.runAutomationDoctor(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_AUTOMATION_COMMAND", "Run dharana automation help for supported commands."))
		return 2
	}
}

func (a *app) runAutomationValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("automation validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		writeCLIError(stderr, jsonOut, output.NewError("AUTOMATION_POLICY_REQUIRED", "Provide one automation policy file."))
		return 2
	}
	policy, err := automation.ParseFile(positional[0])
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result := automation.Validate(policy)
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.validate", result)
	} else if result.Valid {
		_, _ = fmt.Fprintf(stdout, "Policy %s is valid.\n", result.PolicyID)
	} else {
		_, _ = fmt.Fprintf(stdout, "Policy is invalid with %d finding(s).\n", len(result.Findings))
	}
	if !result.Valid {
		return 2
	}
	return 0
}

func (a *app) runAutomationCapabilities(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("automation capabilities", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result := automation.Capabilities()
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.capabilities", result)
	} else {
		_, _ = fmt.Fprintf(stdout, "events: %s\nactions: %s\n", strings.Join(result.Events, ", "), strings.Join(result.Actions, ", "))
	}
	return 0
}

func (a *app) runAutomationRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("automation run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var policyFiles csvFlag
	var policyDir, format string
	var once, dryRun, apply, jsonOut bool
	var interval time.Duration
	fs.Var(&policyFiles, "policy", "Policy file; repeat for multiple policies")
	fs.StringVar(&policyDir, "policy-dir", "", "Directory containing policy YAML or JSON files")
	fs.StringVar(&format, "format", "", "Streaming output format: jsonl")
	fs.BoolVar(&once, "once", false, "Run one synchronization and evaluation cycle")
	fs.BoolVar(&dryRun, "dry-run", false, "Use authoritative action validation without mutation")
	fs.BoolVar(&apply, "apply", false, "Explicitly authorize apply-mode policies")
	fs.BoolVar(&jsonOut, "json", false, "Return one JSON result envelope")
	fs.DurationVar(&interval, "interval", 30*time.Second, "Long-running polling interval")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if jsonOut && format != "" {
		writeCLIError(stderr, true, output.NewError("AUTOMATION_OUTPUT_MODE_CONFLICT", "Use --json or --format jsonl, not both."))
		return 2
	}
	if dryRun && apply {
		writeCLIError(stderr, jsonOut || format == "jsonl", output.NewError("AUTOMATION_MODE_CONFLICT", "Use --dry-run or --apply, not both."))
		return 2
	}
	policies, err := loadPolicies(policyFiles, policyDir)
	if err != nil {
		writeCLIError(stderr, jsonOut || format == "jsonl", err)
		return 2
	}
	service, runtime, err := a.automationServiceForPolicies(policies)
	if err != nil {
		writeCLIError(stderr, jsonOut || format == "jsonl", err)
		return exitCodeForError(err)
	}
	preflight, err := service.Doctor(ctx, policies)
	if err != nil {
		writeCLIError(stderr, jsonOut || format == "jsonl", err)
		return exitCodeForError(err)
	}
	if !preflight.Healthy {
		err = output.NewErrorWithDetails("AUTOMATION_PREFLIGHT_FAILED", "Automation startup validation failed.", preflight)
		writeCLIError(stderr, jsonOut || format == "jsonl", err)
		return 2
	}
	encoder := json.NewEncoder(stdout)
	var emit func(any) error
	if format == "jsonl" {
		emit = func(record any) error { return encoder.Encode(record) }
	}
	if !once && format != "jsonl" {
		writeCLIError(stderr, jsonOut, output.NewError("AUTOMATION_STREAM_FORMAT_REQUIRED", "Long-running automation requires --format jsonl."))
		return 2
	}
	result, err := runtime.Run(ctx, policies, automation.RunOptions{DryRun: dryRun, Apply: apply, Once: once, Interval: interval}, emit)
	if err != nil {
		writeCLIError(stderr, jsonOut || format == "jsonl", err)
		return exitCodeForError(err)
	}
	if format == "jsonl" {
		if err := encoder.Encode(map[string]any{"schema_version": "1", "record_type": "run.summary", "result": result}); err != nil {
			return 2
		}
		return automationResultExitCode(result)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.run", result)
	} else {
		_, _ = fmt.Fprintf(stdout, "%d event(s), %d evaluation(s), %d action(s).\n", result.Events, result.Evaluations, result.ActionsObserved)
	}
	return automationResultExitCode(result)
}

func automationResultExitCode(result *automation.RunResult) int {
	code := 0
	for _, action := range result.Actions {
		switch action.Disposition {
		case "quarantined":
			return 6
		case "failed":
			code = 5
		case "conflicted":
			if code == 0 {
				code = 4
			}
		}
	}
	return code
}

func (a *app) runAutomationHistory(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("automation history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var limit int
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.IntVar(&limit, "limit", 100, "Maximum journal records")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := a.automationJournal().History(limit)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.history", result)
	} else {
		for _, entry := range result.Entries {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", entry.RecordedAt, entry.Kind, entry.PolicyID, entry.ID)
		}
	}
	return 0
}

func (a *app) runAutomationExplain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("automation explain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		writeCLIError(stderr, jsonOut, output.NewError("AUTOMATION_EVALUATION_ID_REQUIRED", "Provide one evaluation ID."))
		return 2
	}
	result, err := a.automationJournal().Explain(positional[0])
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.explain", result)
	} else if result.Evaluation.Evaluation != nil {
		for _, line := range result.Evaluation.Evaluation.Explanation {
			_, _ = fmt.Fprintln(stdout, line)
		}
	}
	return 0
}

func (a *app) runAutomationRetry(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("automation retry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun, apply bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Re-evaluate the action without mutation")
	fs.BoolVar(&apply, "apply", false, "Explicitly authorize the retry mutation")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || dryRun == apply {
		writeCLIError(stderr, jsonOut, output.NewError("AUTOMATION_RETRY_MODE_REQUIRED", "Provide one action ID and exactly one of --dry-run or --apply."))
		return 2
	}
	_, runtime, err := a.automationServiceForContext("")
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	result, err := runtime.Retry(ctx, positional[0], dryRun)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.retry", result)
	} else {
		_, _ = fmt.Fprintf(stdout, "%s: %s.\n", result.OriginalActionID, result.Action.Disposition)
	}
	return 0
}

func (a *app) runAutomationStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	policies, jsonOut, code := parseOptionalPolicies("automation status", args, stderr)
	if code != 0 {
		return code
	}
	service, _, err := a.automationServiceForPolicies(policies)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	result, err := service.Status(ctx, policies)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.status", result)
	} else {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%d recent outcome(s)\n", result.Context, result.Health, len(result.RecentOutcomes))
	}
	return 0
}

func (a *app) runAutomationDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	policies, jsonOut, code := parseOptionalPolicies("automation doctor", args, stderr)
	if code != 0 {
		return code
	}
	service, _, err := a.automationServiceForPolicies(policies)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	result, err := service.Doctor(ctx, policies)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "automation.doctor", result)
	} else {
		_, _ = fmt.Fprintf(stdout, "healthy=%t; %d finding(s).\n", result.Healthy, len(result.Findings))
	}
	if !result.Healthy {
		return 2
	}
	return 0
}

func (a *app) syncServiceForContext(contextName string) (*syncer.Service, error) {
	if a.sync != nil && contextName == "" {
		return a.sync, nil
	}
	effective, err := a.configForContext(contextName)
	if err != nil {
		return nil, err
	}
	workService := *a.workService()
	workService.Config = effective
	root := filepath.Dir(a.configStore().Path)
	if root == "." || root == "" {
		root = config.DefaultDir()
	}
	return &syncer.Service{Auth: a.auth, Config: effective, Events: asana.NewClient(""), Projection: &workService, Store: &syncer.Store{Root: root}}, nil
}

func (a *app) automationServiceForPolicies(policies []*automation.Policy) (*automation.AutomationService, *automation.Runtime, error) {
	contextName := ""
	for _, policy := range policies {
		if contextName == "" {
			contextName = policy.Spec.Context
		} else if policy.Spec.Context != contextName {
			return nil, nil, output.NewError("AUTOMATION_CONTEXT_MIXED", "The initial runtime supports one explicit context; split policies by context.")
		}
	}
	return a.automationServiceForContext(contextName)
}

func (a *app) automationServiceForContext(contextName string) (*automation.AutomationService, *automation.Runtime, error) {
	syncService, err := a.syncServiceForContext(contextName)
	if err != nil {
		return nil, nil, err
	}
	effective, err := a.configForContext(contextName)
	if err != nil {
		return nil, nil, err
	}
	workService := *a.workService()
	workService.Config = effective
	journal := a.automationJournal()
	runtime := &automation.Runtime{Sync: syncService, Work: &workService, Auth: a.auth, Journal: journal, LeaseRoot: filepath.Join(filepath.Dir(journal.Path), "leases")}
	service := &automation.AutomationService{Runtime: runtime, Sync: syncService, Journal: journal, Auth: a.auth, Config: effective}
	return service, runtime, nil
}

func (a *app) automationJournal() *automation.Journal {
	root := filepath.Dir(a.configStore().Path)
	if root == "." || root == "" {
		root = config.DefaultDir()
	}
	return &automation.Journal{Path: filepath.Join(root, "automation", "journal.jsonl")}
}

func (a *app) configForContext(name string) (interface {
	Load() (*config.File, error)
	Save(*config.File) error
}, error) {
	base := a.effectiveConfigStore()
	if strings.TrimSpace(name) == "" {
		return base, nil
	}
	cfg, err := base.Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read configured contexts.")
	}
	contextValue, ok := cfg.ContextByName(strings.TrimSpace(name))
	if !ok {
		return nil, output.NewError("CONTEXT_NOT_FOUND", "The requested context does not exist.")
	}
	copyValue := *cfg
	project := contextValue.Project
	copyValue.ActiveProject = &project
	copyValue.ActiveContext = contextValue.Name
	return &staticConfigStore{file: &copyValue}, nil
}

func loadPolicies(files []string, dir string) ([]*automation.Policy, error) {
	paths := append([]string(nil), files...)
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, output.NewError("AUTOMATION_POLICY_DIR_READ_FAILED", "Could not read the policy directory.")
		}
		for _, entry := range entries {
			extension := strings.ToLower(filepath.Ext(entry.Name()))
			if entry.IsDir() || (extension != ".yaml" && extension != ".yml" && extension != ".json") {
				continue
			}
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, output.NewError("AUTOMATION_POLICY_REQUIRED", "Provide --policy or --policy-dir.")
	}
	policies := make([]*automation.Policy, 0, len(paths))
	for _, path := range paths {
		policy, err := automation.ParseFile(path)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

func parseOptionalPolicies(name string, args []string, stderr io.Writer) ([]*automation.Policy, bool, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var files csvFlag
	var dir string
	var jsonOut bool
	fs.Var(&files, "policy", "Policy file; repeat for multiple policies")
	fs.StringVar(&dir, "policy-dir", "", "Directory containing policies")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return nil, jsonOut, 2
	}
	if len(files) == 0 && dir == "" {
		return []*automation.Policy{}, jsonOut, 0
	}
	policies, err := loadPolicies(files, dir)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return nil, jsonOut, 2
	}
	return policies, jsonOut, 0
}

func printSyncUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:\n  dharana sync status [--context <name>] [--json]\n  dharana sync pull [--context <name>] [--json]\n  dharana sync reset [--context <name>] (--dry-run|--apply) [--json]")
}

func printAutomationUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:\n  dharana automation validate <policy> [--json]\n  dharana automation capabilities [--json]\n  dharana automation run --policy <file> [--once] [--dry-run] [--apply] [--json|--format jsonl]\n  dharana automation history [--limit <n>] [--json]\n  dharana automation explain <evaluation-id> [--json]\n  dharana automation retry <action-id> (--dry-run|--apply) [--json]\n  dharana automation status [--policy <file>] [--json]\n  dharana automation doctor [--policy <file>] [--json]")
}
