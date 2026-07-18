package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/doctor"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/project"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type app struct {
	auth    *auth.Service
	project *project.Service
	doctor  *doctor.Service
	config  *config.Store
	work    *work.Service
}

func Run(args []string, stdout, stderr io.Writer) int {
	authService := auth.NewService()
	return (&app{
		auth:    authService,
		project: project.NewService(authService),
		doctor:  doctor.NewService(authService),
		config:  config.NewStore(),
		work:    work.NewService(authService),
	}).run(context.Background(), args, stdout, stderr)
}

func (a *app) run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "auth":
		return a.runAuth(ctx, args[1:], stdout, stderr)
	case "project":
		return a.runProject(ctx, args[1:], stdout, stderr)
	case "config":
		return a.runConfig(args[1:], stdout, stderr)
	case "doctor":
		return a.runDoctor(ctx, args[1:], stdout, stderr)
	case "epic":
		return a.runEpic(ctx, args[1:], stdout, stderr)
	case "story":
		return a.runStory(ctx, args[1:], stdout, stderr)
	case "bug":
		return a.runBug(ctx, args[1:], stdout, stderr)
	case "spike":
		return a.runSpike(ctx, args[1:], stdout, stderr)
	case "task":
		return a.runTask(ctx, args[1:], stdout, stderr)
	case "dependency":
		return a.runDependency(ctx, args[1:], stdout, stderr)
	case "work":
		return a.runWork(ctx, args[1:], stdout, stderr)
	case "refs":
		return a.runRefs(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_COMMAND", "Unknown command. Run dharana help for usage."))
		return 2
	}
}

func (a *app) runDependency(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printDependencyUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printDependencyUsage(stdout)
		return 0
	case "add":
		return a.runDependencyAdd(ctx, args[1:], stdout, stderr)
	case "remove":
		return a.runDependencyRemove(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_DEPENDENCY_COMMAND", "Unknown dependency command. Run dharana dependency help for usage."))
		return 2
	}
}

func (a *app) runDependencyAdd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dependency add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var blockedBy string
	var dryRun bool
	var jsonOut bool
	fs.StringVar(&blockedBy, "blocked-by", "", "Reference or GID that must finish before this work can proceed")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview the dependency without mutating Asana")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	var blocked string
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		blocked = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return 2
	}
	if blocked == "" {
		blocked = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if blocked == "" {
		writeCLIError(stderr, jsonOut, output.NewError("BLOCKED_REFERENCE_REQUIRED", "Provide the blocked work reference."))
		return 2
	}
	if strings.TrimSpace(blockedBy) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("BLOCKER_REFERENCE_REQUIRED", "Provide the blocking work reference with --blocked-by."))
		return 2
	}

	result, err := a.workService().AddDependency(ctx, work.AddDependencyOptions{
		BlockedRef:   blocked,
		BlockedByRef: blockedBy,
		DryRun:       dryRun,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "dependency.add", result)
		return 0
	}
	if result.IdempotentExisting {
		_, _ = fmt.Fprintf(stdout, "%s is already blocked by %s.\n", result.Blocked.Ref, result.BlockedBy.Ref)
		return 0
	}
	if result.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would block %s by %s.\n", result.Blocked.Ref, result.BlockedBy.Ref)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Blocked %s by %s.\n", result.Blocked.Ref, result.BlockedBy.Ref)
	return 0
}

func (a *app) runDependencyRemove(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dependency remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var blockedBy string
	var dryRun bool
	var jsonOut bool
	fs.StringVar(&blockedBy, "blocked-by", "", "Reference or GID to remove as a blocker")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview the removal without mutating Asana")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	var blocked string
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		blocked = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := fs.Parse(parseArgs); err != nil {
		return 2
	}
	if blocked == "" {
		blocked = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if blocked == "" {
		writeCLIError(stderr, jsonOut, output.NewError("BLOCKED_REFERENCE_REQUIRED", "Provide the blocked work reference."))
		return 2
	}
	if strings.TrimSpace(blockedBy) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("BLOCKER_REFERENCE_REQUIRED", "Provide the blocking work reference with --blocked-by."))
		return 2
	}

	result, err := a.workService().RemoveDependency(ctx, work.RemoveDependencyOptions{
		BlockedRef:   blocked,
		BlockedByRef: blockedBy,
		DryRun:       dryRun,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "dependency.remove", result)
		return 0
	}
	if !result.Found {
		_, _ = fmt.Fprintf(stdout, "%s was not blocked by %s.\n", result.Blocked.Ref, result.BlockedBy.Ref)
		return 0
	}
	if result.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would remove blocker %s from %s.\n", result.BlockedBy.Ref, result.Blocked.Ref)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Removed blocker %s from %s.\n", result.BlockedBy.Ref, result.Blocked.Ref)
	return 0
}

func (a *app) runRefs(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printRefsUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printRefsUsage(stdout)
		return 0
	case "refresh":
		return a.runRefsRefresh(ctx, args[1:], stdout, stderr)
	case "resolve":
		return a.runRefsResolve(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_REFS_COMMAND", "Unknown refs command. Run dharana refs help for usage."))
		return 2
	}
}

func (a *app) runRefsRefresh(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("refs refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var limit int
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.IntVar(&limit, "limit", 100, "Page size used while refreshing, max 100")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.workService().RefreshRefs(ctx, work.RefreshRefsOptions{Limit: limit})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "refs.refresh", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Ref cache refreshed with %d items.\n", result.Count)
	return 0
}

func (a *app) runRefsResolve(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("refs resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}

	result, err := a.workService().ResolveRef(ctx, ref)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "refs.resolve", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", result.Entry.Ref, result.Entry.GID, result.Entry.Name)
	return 0
}

func (a *app) runWork(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printWorkUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printWorkUsage(stdout)
		return 0
	case "list":
		return a.runWorkList(ctx, args[1:], stdout, stderr)
	case "tree":
		return a.runWorkTree(ctx, args[1:], stdout, stderr)
	case "blocked":
		return a.runWorkBlocked(ctx, args[1:], stdout, stderr)
	case "ready":
		return a.runWorkReady(ctx, args[1:], stdout, stderr)
	case "graph":
		return a.runWorkGraph(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_WORK_COMMAND", "Unknown work command. Run dharana work help for usage."))
		return 2
	}
}

func (a *app) runWorkGraph(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var epicRef string
	var format string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&epicRef, "epic", "", "Scope to one epic by GID, EPIC:<name>, or exact name")
	fs.StringVar(&format, "format", "json", "Output format: json or mermaid")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "json" && format != "mermaid" {
		writeCLIError(stderr, jsonOut, output.NewError("INVALID_GRAPH_FORMAT", "Graph format must be json or mermaid."))
		return 2
	}

	result, err := a.workService().WorkGraph(ctx, work.WorkGraphOptions{EpicRef: epicRef})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut || format == "json" {
		_ = output.WriteOperationJSON(stdout, "work.graph", result)
		return 0
	}
	_, _ = fmt.Fprint(stdout, result.Mermaid)
	return 0
}

func (a *app) runWorkReady(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work ready", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var types csvFlag
	var priorities csvFlag
	var components csvFlag
	var epicRef string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.Var(&types, "type", "Filter by work type; repeat or use comma-separated values")
	fs.Var(&priorities, "priority", "Filter by priority; repeat or use comma-separated values")
	fs.Var(&components, "component", "Filter by component; repeat or use comma-separated values")
	fs.StringVar(&epicRef, "epic", "", "Scope to one epic by GID, EPIC:<name>, or exact name")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.workService().ReadyWork(ctx, work.ReadyWorkOptions{
		Types:      types,
		EpicRef:    epicRef,
		Priorities: priorities,
		Components: components,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.ready", result)
		return 0
	}
	for _, item := range result.Items {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.Type, item.Status, item.GID, item.Name)
	}
	return 0
}

func (a *app) runWorkBlocked(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work blocked", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var types csvFlag
	var epicRef string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.Var(&types, "type", "Filter by work type; repeat or use comma-separated values")
	fs.StringVar(&epicRef, "epic", "", "Scope to one epic by GID, EPIC:<name>, or exact name")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.workService().BlockedWork(ctx, work.BlockedWorkOptions{Types: types, EpicRef: epicRef})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.blocked", result)
		return 0
	}
	for _, item := range result.Items {
		var blockers []string
		for _, blocker := range item.Blockers {
			blockers = append(blockers, blocker.Ref)
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\tblocked by %s\n", item.Item.Type, item.Item.GID, item.Item.Name, strings.Join(blockers, ", "))
	}
	return 0
}

func (a *app) runWorkTree(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work tree", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var epicRef string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&epicRef, "epic", "", "Scope to one epic by GID, EPIC:<name>, or exact name")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.workService().WorkTree(ctx, work.WorkTreeOptions{EpicRef: epicRef})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.tree", result)
		return 0
	}
	_, _ = fmt.Fprint(stdout, work.FormatWorkTree(result))
	return 0
}

func (a *app) runWorkList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var types csvFlag
	var status string
	var epicRef string
	var limit int
	var offset string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.Var(&types, "type", "Filter by work type; repeat or use comma-separated values")
	fs.StringVar(&status, "status", "all", "Filter by status: all, incomplete, or completed")
	fs.StringVar(&epicRef, "epic", "", "Scope to one epic by GID, EPIC:<name>, or exact name")
	fs.IntVar(&limit, "limit", 50, "Page size, max 100")
	fs.StringVar(&offset, "offset", "", "Asana pagination offset")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.workService().ListWork(ctx, work.ListWorkOptions{
		Types:   types,
		Status:  status,
		EpicRef: epicRef,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.list", result)
		return 0
	}
	for _, item := range result.Items {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.Type, item.Status, item.GID, item.Name)
	}
	return 0
}

func (a *app) runTask(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printTaskUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printTaskUsage(stdout)
		return 0
	case "create":
		return a.runTaskCreate(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_TASK_COMMAND", "Unknown task command. Run dharana task help for usage."))
		return 2
	}
}

func (a *app) runTaskCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var dryRun bool
	var idempotent bool
	var parentRef string
	var assignee string
	var dueOn string
	var estimate string
	var notes string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name task instead of failing")
	fs.StringVar(&parentRef, "parent", "", "Parent story, bug, spike, or task reference")
	fs.StringVar(&assignee, "assignee", "", "Optional assignee identifier or email")
	fs.StringVar(&dueOn, "due-on", "", "Optional due date")
	fs.StringVar(&estimate, "estimate", "", "Optional estimate")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	nameArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(nameArgs, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("TASK_NAME_REQUIRED", "Provide an implementation task name."))
		return 2
	}
	if strings.TrimSpace(parentRef) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("PARENT_REFERENCE_REQUIRED", "Provide a parent reference with --parent."))
		return 2
	}

	result, err := a.workService().CreateImplementationTask(ctx, work.CreateTaskOptions{
		Name:       name,
		ParentRef:  parentRef,
		Assignee:   assignee,
		DueOn:      dueOn,
		Estimate:   estimate,
		Notes:      notes,
		DryRun:     dryRun,
		Idempotent: idempotent,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "task.create", result)
		return 0
	}
	if result.Task.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would create task %q beneath %s.\n", result.Task.Name, result.Task.Parent.Name)
		return 0
	}
	if result.Task.IdempotentExisting {
		_, _ = fmt.Fprintf(stdout, "Task already exists: %s (%s).\n", result.Task.Name, result.Task.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Created task %s (%s).\n", result.Task.Name, result.Task.GID)
	return 0
}

func (a *app) runSpike(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printSpikeUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printSpikeUsage(stdout)
		return 0
	case "create":
		return a.runSpikeCreate(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_SPIKE_COMMAND", "Unknown spike command. Run dharana spike help for usage."))
		return 2
	}
}

func (a *app) runSpikeCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spike create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var dryRun bool
	var idempotent bool
	var epicRef string
	var timebox string
	var notes string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name spike instead of failing")
	fs.StringVar(&epicRef, "epic", "", "Epic reference by GID, EPIC:<name>, or exact name")
	fs.StringVar(&timebox, "timebox", "", "Optional investigation time-box")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	nameArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(nameArgs, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("SPIKE_NAME_REQUIRED", "Provide a spike name."))
		return 2
	}
	if strings.TrimSpace(epicRef) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("EPIC_REFERENCE_REQUIRED", "Provide an epic reference with --epic."))
		return 2
	}

	result, err := a.workService().CreateSpike(ctx, work.CreateSpikeOptions{
		Name:       name,
		EpicRef:    epicRef,
		Timebox:    timebox,
		Notes:      notes,
		DryRun:     dryRun,
		Idempotent: idempotent,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "spike.create", result)
		return 0
	}
	if result.Spike.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would create spike %q beneath %s.\n", result.Spike.Name, result.Spike.Epic.Name)
		return 0
	}
	if result.Spike.IdempotentExisting {
		_, _ = fmt.Fprintf(stdout, "Spike already exists: %s (%s).\n", result.Spike.Name, result.Spike.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Created spike %s (%s).\n", result.Spike.Name, result.Spike.GID)
	return 0
}

func (a *app) runBug(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printBugUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printBugUsage(stdout)
		return 0
	case "create":
		return a.runBugCreate(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_BUG_COMMAND", "Unknown bug command. Run dharana bug help for usage."))
		return 2
	}
}

func (a *app) runBugCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bug create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var dryRun bool
	var idempotent bool
	var epicRef string
	var priority string
	var environment string
	var notes string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name bug instead of failing")
	fs.StringVar(&epicRef, "epic", "", "Epic reference by GID, EPIC:<name>, or exact name")
	fs.StringVar(&priority, "priority", "", "Bug priority")
	fs.StringVar(&environment, "environment", "", "Bug environment")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	nameArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(nameArgs, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("BUG_NAME_REQUIRED", "Provide a bug name."))
		return 2
	}
	if strings.TrimSpace(epicRef) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("EPIC_REFERENCE_REQUIRED", "Provide an epic reference with --epic."))
		return 2
	}

	result, err := a.workService().CreateBug(ctx, work.CreateBugOptions{
		Name:        name,
		EpicRef:     epicRef,
		Priority:    priority,
		Environment: environment,
		Notes:       notes,
		DryRun:      dryRun,
		Idempotent:  idempotent,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "bug.create", result)
		return 0
	}
	if result.Bug.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would create bug %q beneath %s.\n", result.Bug.Name, result.Bug.Epic.Name)
		return 0
	}
	if result.Bug.IdempotentExisting {
		_, _ = fmt.Fprintf(stdout, "Bug already exists: %s (%s).\n", result.Bug.Name, result.Bug.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Created bug %s (%s).\n", result.Bug.Name, result.Bug.GID)
	return 0
}

func (a *app) runStory(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printStoryUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printStoryUsage(stdout)
		return 0
	case "create":
		return a.runStoryCreate(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_STORY_COMMAND", "Unknown story command. Run dharana story help for usage."))
		return 2
	}
}

func (a *app) runStoryCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("story create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var dryRun bool
	var idempotent bool
	var epicRef string
	var notes string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name story instead of failing")
	fs.StringVar(&epicRef, "epic", "", "Epic reference by GID, EPIC:<name>, or exact name")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	nameArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(nameArgs, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("STORY_NAME_REQUIRED", "Provide a story name."))
		return 2
	}
	if strings.TrimSpace(epicRef) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("EPIC_REFERENCE_REQUIRED", "Provide an epic reference with --epic."))
		return 2
	}

	result, err := a.workService().CreateStory(ctx, work.CreateStoryOptions{
		Name:       name,
		EpicRef:    epicRef,
		Notes:      notes,
		DryRun:     dryRun,
		Idempotent: idempotent,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "story.create", result)
		return 0
	}
	if result.Story.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would create story %q beneath %s.\n", result.Story.Name, result.Story.Epic.Name)
		return 0
	}
	if result.Story.IdempotentExisting {
		_, _ = fmt.Fprintf(stdout, "Story already exists: %s (%s).\n", result.Story.Name, result.Story.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Created story %s (%s).\n", result.Story.Name, result.Story.GID)
	return 0
}

func (a *app) runEpic(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printEpicUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printEpicUsage(stdout)
		return 0
	case "create":
		return a.runEpicCreate(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_EPIC_COMMAND", "Unknown epic command. Run dharana epic help for usage."))
		return 2
	}
}

func (a *app) runEpicCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("epic create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var dryRun bool
	var idempotent bool
	var notes string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name epic instead of failing")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	nameArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(nameArgs, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("EPIC_NAME_REQUIRED", "Provide an epic name."))
		return 2
	}

	result, err := a.workService().CreateEpic(ctx, work.CreateEpicOptions{
		Name:       name,
		Notes:      notes,
		DryRun:     dryRun,
		Idempotent: idempotent,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "epic.create", result)
		return 0
	}
	if result.Epic.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would create epic %q in %s.\n", result.Epic.Name, result.Epic.ProjectName)
		return 0
	}
	if result.Epic.IdempotentExisting {
		_, _ = fmt.Fprintf(stdout, "Epic already exists: %s (%s).\n", result.Epic.Name, result.Epic.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Created epic %s (%s).\n", result.Epic.Name, result.Epic.GID)
	return 0
}

func parseInterspersedFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flagArgs = append(flagArgs, arg)
			if strings.Contains(arg, "=") {
				continue
			}
			name := strings.TrimLeft(arg, "-")
			if flagValueRequired(fs, name) && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positional, nil
}

func flagValueRequired(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	_, isBool := f.Value.(interface{ IsBoolFlag() bool })
	return !isBool
}

func (a *app) runProject(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printProjectUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printProjectUsage(stdout)
		return 0
	case "list":
		return a.runProjectList(ctx, args[1:], stdout, stderr)
	case "select":
		return a.runProjectSelect(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_PROJECT_COMMAND", "Unknown project command. Run dharana project help for usage."))
		return 2
	}
}

func (a *app) runProjectList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var workspaceGID string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&workspaceGID, "workspace-gid", "", "Limit projects to one Asana workspace GID")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.projectService().List(ctx, project.ListOptions{WorkspaceGID: workspaceGID})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.list", result)
		return 0
	}
	for _, p := range result.Projects {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", p.GID, p.Name, p.WorkspaceName)
	}
	return 0
}

func (a *app) runProjectSelect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project select", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var gid string
	var name string
	var workspaceGID string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&gid, "gid", "", "Select project by Asana project GID")
	fs.StringVar(&name, "name", "", "Select project by exact Asana project name")
	fs.StringVar(&workspaceGID, "workspace-gid", "", "Limit exact-name selection to one Asana workspace GID")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.projectService().Select(ctx, project.SelectOptions{GID: gid, Name: name, WorkspaceGID: workspaceGID})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.select", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Active project set to %s (%s).\n", result.ActiveProject.Name, result.ActiveProject.GID)
	return 0
}

func (a *app) runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printConfigUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printConfigUsage(stdout)
		return 0
	case "show":
		return a.runConfigShow(args[1:], stdout, stderr)
	case "set-task-types":
		return a.runConfigSetTaskTypes(args[1:], stdout, stderr)
	case "set-fields":
		return a.runConfigSetFields(args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_CONFIG_COMMAND", "Unknown config command. Run dharana config help for usage."))
		return 2
	}
}

func (a *app) runConfigShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := a.projectService().ShowConfig()
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "config.show", cfg)
		return 0
	}
	if cfg.ActiveProject == nil {
		_, _ = fmt.Fprintln(stdout, "Active project: not configured")
	} else {
		_, _ = fmt.Fprintf(stdout, "Active project: %s (%s)\n", cfg.ActiveProject.Name, cfg.ActiveProject.GID)
	}
	_, _ = fmt.Fprintf(stdout, "Task types: epic=%q story=%q bug=%q spike=%q\n", cfg.TaskTypes.Epic, cfg.TaskTypes.Story, cfg.TaskTypes.Bug, cfg.TaskTypes.Spike)
	_, _ = fmt.Fprintf(stdout, "Fields: priority_gid=%q component_gid=%q\n", cfg.Fields.PriorityGID, cfg.Fields.ComponentGID)
	return 0
}

func (a *app) runConfigSetTaskTypes(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config set-task-types", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var fieldGID, epic, story, bug, spike string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&fieldGID, "field-gid", "", "Asana custom field GID for task type or work type")
	fs.StringVar(&epic, "epic", "", "Configured Epic type or work-type value")
	fs.StringVar(&story, "story", "", "Configured Story type or work-type value")
	fs.StringVar(&bug, "bug", "", "Configured Bug type or work-type value")
	fs.StringVar(&spike, "spike", "", "Configured Spike type or work-type value")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := a.configStore().Load()
	if err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration."))
		return 1
	}
	if fieldGID != "" {
		cfg.TaskTypes.FieldGID = fieldGID
	}
	if epic != "" {
		cfg.TaskTypes.Epic = epic
	}
	if story != "" {
		cfg.TaskTypes.Story = story
	}
	if bug != "" {
		cfg.TaskTypes.Bug = bug
	}
	if spike != "" {
		cfg.TaskTypes.Spike = spike
	}
	if err := a.configStore().Save(cfg); err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_WRITE_FAILED", "Could not save local configuration."))
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "config.set_task_types", cfg)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "Task type mappings updated.")
	return 0
}

func (a *app) runConfigSetFields(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config set-fields", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var priorityGID, componentGID string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&priorityGID, "priority-gid", "", "Asana custom field GID for priority filtering")
	fs.StringVar(&componentGID, "component-gid", "", "Asana custom field GID for component filtering")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := a.configStore().Load()
	if err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration."))
		return 1
	}
	if priorityGID != "" {
		cfg.Fields.PriorityGID = priorityGID
	}
	if componentGID != "" {
		cfg.Fields.ComponentGID = componentGID
	}
	if err := a.configStore().Save(cfg); err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_WRITE_FAILED", "Could not save local configuration."))
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "config.set_fields", cfg)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "Field mappings updated.")
	return 0
}

func (a *app) runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.doctorService().Run(ctx)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "doctor", result)
	} else {
		for _, check := range result.Checks {
			status := "FAIL"
			if check.OK {
				status = "OK"
			}
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", status, check.Name, check.Message)
		}
	}
	if !result.OK {
		return 1
	}
	return 0
}

func (a *app) runAuth(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printAuthUsage(stderr)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printAuthUsage(stdout)
		return 0
	case "configure":
		return a.runAuthConfigure(ctx, args[1:], stdout, stderr)
	case "status":
		return a.runAuthStatus(args[1:], stdout, stderr)
	case "validate":
		return a.runAuthValidate(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_AUTH_COMMAND", "Unknown auth command. Run dharana auth help for usage."))
		return 2
	}
}

func (a *app) runAuthConfigure(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("auth configure", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var token string
	var tokenStdin bool
	var jsonOut bool
	var validate bool
	fs.StringVar(&token, "token", "", "Asana personal access token")
	fs.BoolVar(&tokenStdin, "stdin", false, "Read the Asana personal access token from stdin")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&validate, "validate", false, "Validate the token with Asana before returning")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if tokenStdin {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			token = scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			writeCLIError(stderr, jsonOut, output.NewError("STDIN_READ_FAILED", "Could not read token from stdin."))
			return 1
		}
	}

	result, err := a.auth.Configure(ctx, token, validate)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}

	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "auth.configure", result)
		return 0
	}

	validatedText := ""
	if result.Validated {
		validatedText = " and validated"
	}
	_, _ = fmt.Fprintf(stdout, "Asana token stored in keychain%s (%s).\n", validatedText, result.Token.Masked)
	return 0
}

func (a *app) runAuthStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.auth.Status()
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}

	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "auth.status", result)
		return 0
	}

	if !result.Configured {
		_, _ = fmt.Fprintln(stdout, "No Asana token configured.")
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Asana token configured from %s (%s).\n", result.Source, result.Token.Masked)
	return 0
}

func (a *app) runAuthValidate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("auth validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	result, err := a.auth.Validate(ctx)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 1
	}

	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "auth.validate", result)
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "Asana token valid for %s (%s).\n", result.User.Name, result.User.GID)
	return 0
}

func writeCLIError(w io.Writer, jsonOut bool, err error) {
	if jsonOut {
		_ = output.WriteErrorJSON(w, err)
		return
	}

	appErr, ok := err.(*output.AppError)
	if !ok {
		_, _ = fmt.Fprintln(w, "error: An unexpected error occurred.")
		return
	}
	_, _ = fmt.Fprintf(w, "error[%s]: %s\n", appErr.Code, appErr.Message)
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
dharana is an agent-native work graph CLI for Asana.

Usage:
  dharana auth configure --token <pat> [--validate] [--json]
  dharana auth configure --stdin [--validate] [--json]
  dharana auth status [--json]
  dharana auth validate [--json]
  dharana project list [--workspace-gid <gid>] [--json]
  dharana project select --gid <gid> [--json]
  dharana project select --name <exact-name> [--workspace-gid <gid>] [--json]
  dharana config show [--json]
  dharana config set-task-types [--field-gid <gid>] --epic <value> --story <value> --bug <value> --spike <value> [--json]
  dharana config set-fields [--priority-gid <gid>] [--component-gid <gid>] [--json]
  dharana doctor [--json]
  dharana epic create <name> [--notes <text>] [--dry-run] [--idempotent] [--json]
  dharana story create --epic <ref> <name> [--notes <text>] [--dry-run] [--idempotent] [--json]
  dharana bug create --epic <ref> <name> [--priority <value>] [--environment <value>] [--notes <text>] [--dry-run] [--idempotent] [--json]
  dharana spike create --epic <ref> <name> [--timebox <value>] [--notes <text>] [--dry-run] [--idempotent] [--json]
  dharana task create --parent <ref> <name> [--assignee <value>] [--due-on <date>] [--estimate <value>] [--notes <text>] [--dry-run] [--idempotent] [--json]
  dharana dependency add <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana dependency remove <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana work list [--type <type>] [--status <status>] [--epic <ref>] [--limit <n>] [--offset <offset>] [--json]
  dharana work tree [--epic <ref>] [--json]
  dharana work blocked [--type <type>] [--epic <ref>] [--json]
  dharana work ready [--type <type>] [--epic <ref>] [--priority <value>] [--component <value>] [--json]
  dharana work graph [--epic <ref>] [--format json|mermaid] [--json]
  dharana refs refresh [--limit <n>] [--json]
  dharana refs resolve <ref> [--json]
`)+"\n")
}

func printAuthUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana auth configure --token <pat> [--validate] [--json]
  dharana auth configure --stdin [--validate] [--json]
  dharana auth status [--json]
  dharana auth validate [--json]
`)+"\n")
}

func printProjectUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana project list [--workspace-gid <gid>] [--json]
  dharana project select --gid <gid> [--json]
  dharana project select --name <exact-name> [--workspace-gid <gid>] [--json]
`)+"\n")
}

func printConfigUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana config show [--json]
  dharana config set-task-types [--field-gid <gid>] --epic <value> --story <value> --bug <value> --spike <value> [--json]
  dharana config set-fields [--priority-gid <gid>] [--component-gid <gid>] [--json]
`)+"\n")
}

func printEpicUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana epic create <name> [--notes <text>] [--dry-run] [--idempotent] [--json]
`)+"\n")
}

func printStoryUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana story create --epic <ref> <name> [--notes <text>] [--dry-run] [--idempotent] [--json]
`)+"\n")
}

func printBugUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana bug create --epic <ref> <name> [--priority <value>] [--environment <value>] [--notes <text>] [--dry-run] [--idempotent] [--json]
`)+"\n")
}

func printSpikeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana spike create --epic <ref> <name> [--timebox <value>] [--notes <text>] [--dry-run] [--idempotent] [--json]
`)+"\n")
}

func printTaskUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana task create --parent <ref> <name> [--assignee <value>] [--due-on <date>] [--estimate <value>] [--notes <text>] [--dry-run] [--idempotent] [--json]
`)+"\n")
}

func printDependencyUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana dependency add <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana dependency remove <ref> --blocked-by <ref> [--dry-run] [--json]
`)+"\n")
}

func printWorkUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana work list [--type <type>] [--status <status>] [--epic <ref>] [--limit <n>] [--offset <offset>] [--json]
  dharana work tree [--epic <ref>] [--json]
  dharana work blocked [--type <type>] [--epic <ref>] [--json]
  dharana work ready [--type <type>] [--epic <ref>] [--priority <value>] [--component <value>] [--json]
  dharana work graph [--epic <ref>] [--format json|mermaid] [--json]
`)+"\n")
}

func printRefsUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana refs refresh [--limit <n>] [--json]
  dharana refs resolve <ref> [--json]
`)+"\n")
}

type csvFlag []string

func (f *csvFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *csvFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func (a *app) projectService() *project.Service {
	if a.project != nil {
		return a.project
	}
	return project.NewService(a.auth)
}

func (a *app) doctorService() *doctor.Service {
	if a.doctor != nil {
		return a.doctor
	}
	return doctor.NewService(a.auth)
}

func (a *app) configStore() *config.Store {
	if a.config != nil {
		return a.config
	}
	return config.NewStore()
}

func (a *app) workService() *work.Service {
	if a.work != nil {
		return a.work
	}
	return work.NewService(a.auth)
}
