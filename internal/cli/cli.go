package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/capabilities"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/doctor"
	"github.com/erikvoit/dharana-cli/internal/output"
	planpkg "github.com/erikvoit/dharana-cli/internal/plan"
	"github.com/erikvoit/dharana-cli/internal/project"
	"github.com/erikvoit/dharana-cli/internal/richtext"
	"github.com/erikvoit/dharana-cli/internal/work"
)

type app struct {
	auth            *auth.Service
	project         *project.Service
	doctor          *doctor.Service
	config          *config.Store
	work            *work.Service
	plan            *planpkg.Service
	projectOverride string
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
	var err error
	args, err = a.parseRootFlags(args)
	if err != nil {
		writeCLIError(stderr, false, err)
		return 2
	}
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
	case "context":
		return a.runContext(ctx, args[1:], stdout, stderr)
	case "doctor":
		return a.runDoctor(ctx, args[1:], stdout, stderr)
	case "version":
		return a.runVersion(args[1:], stdout, stderr)
	case "capabilities":
		return a.runCapabilities(args[1:], stdout, stderr)
	case "workflow":
		return a.runWorkflow(ctx, args[1:], stdout, stderr)
	case "type":
		return a.runType(ctx, args[1:], stdout, stderr)
	case "field":
		return a.runField(ctx, args[1:], stdout, stderr)
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
	case "plan":
		return a.runPlan(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		return a.runHelp(args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_COMMAND", "Unknown command. Run dharana help for usage."))
		return 2
	}
}

func (a *app) parseRootFlags(args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	var out []string
	seenCommand := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !seenCommand && arg == "--project" {
			if i+1 >= len(args) {
				return nil, output.NewError("PROJECT_OVERRIDE_REQUIRED", "Provide a project GID after --project.")
			}
			a.projectOverride = strings.TrimSpace(args[i+1])
			i++
			continue
		}
		if !seenCommand && strings.HasPrefix(arg, "--project=") {
			a.projectOverride = strings.TrimSpace(strings.TrimPrefix(arg, "--project="))
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			seenCommand = true
		}
		out = append(out, arg)
	}
	return out, nil
}

func (a *app) runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result := capabilities.Version(nil)
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "version", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "dharana %s (capability schema %s)\n", result.Version, result.CapabilitySchemaVersion)
	return 0
}

func (a *app) runCapabilities(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result := capabilities.All()
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "capabilities", result)
		return 0
	}
	for _, cmd := range result.Commands {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\n", cmd.Name, cmd.Summary)
	}
	return 0
}

func (a *app) runHelp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("help", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	if jsonOut {
		name := strings.TrimSpace(strings.Join(positional, " "))
		if name == "" {
			_ = output.WriteOperationJSON(stdout, "help", capabilities.All())
			return 0
		}
		cmd, ok := capabilities.Find(name)
		if !ok {
			writeCLIError(stderr, true, output.NewError("HELP_TOPIC_NOT_FOUND", "No command capability matched the requested help topic."))
			return 2
		}
		_ = output.WriteOperationJSON(stdout, "help", cmd)
		return 0
	}
	if len(positional) == 0 {
		printUsage(stdout)
		return 0
	}
	switch positional[0] {
	case "auth":
		printAuthUsage(stdout)
	case "project":
		printProjectUsage(stdout)
	case "context":
		printContextUsage(stdout)
	case "workflow":
		printWorkflowUsage(stdout)
	case "work":
		printWorkUsage(stdout)
	case "refs":
		printRefsUsage(stdout)
	case "plan":
		printPlanUsage(stdout)
	default:
		printUsage(stdout)
	}
	return 0
}

func (a *app) runPlan(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printPlanUsage(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printPlanUsage(stdout)
		return 0
	case "validate":
		return a.runPlanValidate(ctx, args[1:], stdout, stderr)
	case "schema":
		return a.runPlanSchema(args[1:], stdout, stderr)
	case "diff":
		return a.runPlanDiff(ctx, args[1:], stdout, stderr)
	case "apply":
		return a.runPlanApply(ctx, args[1:], stdout, stderr, false)
	case "reconcile":
		return a.runPlanApply(ctx, args[1:], stdout, stderr, true)
	case "status":
		return a.runPlanStatus(ctx, args[1:], stdout, stderr)
	case "adopt":
		return a.runPlanAdopt(ctx, args[1:], stdout, stderr)
	case "export":
		return a.runPlanExport(ctx, args[1:], stdout, stderr)
	case "bindings":
		return a.runPlanBindings(args[1:], stdout, stderr)
	case "bind":
		return a.runPlanBindingChange(ctx, args[1:], stdout, stderr, false)
	case "unbind":
		return a.runPlanBindingChange(ctx, args[1:], stdout, stderr, true)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_PLAN_COMMAND", "Unknown plan command. Run dharana plan help for usage."))
		return 2
	}
}

func (a *app) runPlanSchema(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan schema", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.schema", planpkg.Schema())
	} else {
		_ = output.WriteJSON(stdout, planpkg.Schema())
	}
	return 0
}

func (a *app) runPlanValidate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var remote, jsonOut bool
	fs.BoolVar(&remote, "remote", false, "Also validate users, fields, context, and project capabilities remotely")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	result, err := a.planService(manifest).Validate(ctx, manifest, remote)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.validate", result)
	} else if result.Valid {
		_, _ = fmt.Fprintln(stdout, "Plan is valid.")
	} else {
		_, _ = fmt.Fprintf(stdout, "Plan is invalid with %d finding(s).\n", len(result.LocalFindings)+len(result.RemoteFindings))
	}
	if !result.Valid {
		return 2
	}
	return 0
}

func (a *app) runPlanDiff(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	result, err := a.planService(manifest).Diff(ctx, manifest)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.diff", result)
	} else if result.Converged {
		_, _ = fmt.Fprintln(stdout, "Plan is converged.")
	} else {
		for _, operation := range result.Operations {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", operation.Kind, operation.LogicalID, operation.Reason)
			if len(operation.Current) > 0 {
				_, _ = fmt.Fprintf(stdout, "  current: %v\n", operation.Current)
			}
			if len(operation.LastApplied) > 0 {
				_, _ = fmt.Fprintf(stdout, "  last applied: %v\n", operation.LastApplied)
			}
			if len(operation.Desired) > 0 {
				_, _ = fmt.Fprintf(stdout, "  desired: %v\n", operation.Desired)
			}
			if len(operation.Prerequisites) > 0 {
				_, _ = fmt.Fprintf(stdout, "  prerequisites: %s\n", strings.Join(operation.Prerequisites, ", "))
			}
		}
	}
	if !result.Validation.Valid {
		return 2
	}
	if result.Conflicted {
		return 4
	}
	return 0
}

func (a *app) runPlanApply(ctx context.Context, args []string, stdout, stderr io.Writer, reconcile bool) int {
	name := "plan apply"
	operation := "plan.apply"
	if reconcile {
		name = "plan reconcile"
		operation = "plan.reconcile"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var dryRun, apply, jsonOut bool
	fs.BoolVar(&dryRun, "dry-run", false, "Preview operations without mutating Asana or bindings")
	if reconcile {
		fs.BoolVar(&apply, "apply", false, "Apply reconciliation operations; default is a dry-run")
	}
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	if reconcile && !apply {
		dryRun = true
	}
	service := a.planService(manifest)
	var result *planpkg.ApplyResult
	var err error
	if reconcile {
		result, err = service.Reconcile(ctx, manifest, planpkg.ApplyOptions{DryRun: dryRun})
	} else {
		result, err = service.Apply(ctx, manifest, planpkg.ApplyOptions{DryRun: dryRun})
	}
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, operation, result)
	} else if result.Converged {
		_, _ = fmt.Fprintln(stdout, "Plan is converged.")
	} else if dryRun {
		_, _ = fmt.Fprintf(stdout, "Would apply %d operation(s).\n", len(result.Results))
	} else {
		_, _ = fmt.Fprintf(stdout, "Applied %d operation(s).\n", len(result.Results))
	}
	return 0
}

func (a *app) runPlanStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	result, err := a.planService(manifest).Status(ctx, manifest)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.status", result)
	} else {
		_, _ = fmt.Fprintln(stdout, result.State)
	}
	return 0
}

func (a *app) runPlanAdopt(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan adopt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var dryRun, apply, jsonOut bool
	fs.BoolVar(&dryRun, "dry-run", false, "Preview exact-match bindings without saving them")
	fs.BoolVar(&apply, "apply", false, "Save unambiguous exact-match bindings")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	if dryRun && apply {
		err := output.NewError("PLAN_ADOPT_MODE_CONFLICT", "Use --dry-run or --apply, not both.")
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.planService(manifest).Adopt(ctx, manifest, planpkg.AdoptOptions{DryRun: dryRun || !apply, Apply: apply})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.adopt", result)
	} else {
		_, _ = fmt.Fprintf(stdout, "%d binding(s) discovered.\n", len(result.Bindings))
	}
	return 0
}

func (a *app) runPlanExport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var epicRef, destination string
	var jsonOut bool
	fs.StringVar(&epicRef, "epic", "", "Epic GID or friendly reference to export")
	fs.StringVar(&destination, "output", "", "Destination YAML file")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(epicRef) == "" || strings.TrimSpace(destination) == "" {
		err := output.NewError("PLAN_EXPORT_ARGUMENTS_REQUIRED", "Provide --epic <ref> and --output <path>.")
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.planService(nil).Export(ctx, epicRef)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	data, err := planpkg.MarshalYAML(result.Manifest)
	if err != nil {
		err = output.NewError("PLAN_ENCODE_FAILED", "Could not encode the exported plan manifest.")
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	if err := writeFileAtomic(destination, data, 0o644); err != nil {
		err = output.NewErrorWithDetails("PLAN_WRITE_FAILED", "Could not write the exported plan manifest.", err.Error())
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.export", map[string]any{"output": destination, "result": result})
	} else {
		_, _ = fmt.Fprintf(stdout, "Exported plan to %s.\n", destination)
	}
	return 0
}

func (a *app) runPlanBindings(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plan bindings", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	result, err := a.planService(manifest).InspectBindings(manifest)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "plan.bindings", result)
	} else {
		for _, binding := range result.Bindings {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", binding.LogicalID, binding.GID, binding.Type, binding.LastKnownName)
		}
	}
	return 0
}

func (a *app) runPlanBindingChange(ctx context.Context, args []string, stdout, stderr io.Writer, unbind bool) int {
	name := "plan bind"
	operation := "plan.bind"
	if unbind {
		name = "plan unbind"
		operation = "plan.unbind"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var logicalID, gid string
	var dryRun, apply, jsonOut bool
	fs.StringVar(&logicalID, "id", "", "Manifest logical ID")
	if !unbind {
		fs.StringVar(&gid, "gid", "", "Replacement Asana GID")
	}
	fs.BoolVar(&dryRun, "dry-run", false, "Preview the local binding change")
	fs.BoolVar(&apply, "apply", false, "Apply the local binding change")
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	manifest, _, code := parsePlanManifest(fs, args, stderr, jsonOut)
	if code != 0 {
		return code
	}
	if strings.TrimSpace(logicalID) == "" || (!unbind && strings.TrimSpace(gid) == "") {
		err := output.NewError("PLAN_BINDING_ARGUMENTS_REQUIRED", "Provide --id <logical-id> and, for bind, --gid <asana-gid>.")
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	if dryRun && apply {
		err := output.NewError("PLAN_BINDING_MODE_CONFLICT", "Use --dry-run or --apply, not both.")
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	var result *planpkg.BindingChangeResult
	var err error
	if unbind {
		result, err = a.planService(manifest).Unbind(manifest, logicalID, apply)
	} else {
		result, err = a.planService(manifest).ReplaceBinding(ctx, manifest, logicalID, gid, apply)
	}
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, operation, result)
	} else if result.Applied {
		_, _ = fmt.Fprintf(stdout, "Updated binding %s.\n", logicalID)
	} else {
		_, _ = fmt.Fprintf(stdout, "Would update binding %s.\n", logicalID)
	}
	return 0
}

func parsePlanManifest(fs *flag.FlagSet, args []string, stderr io.Writer, jsonOut bool) (*planpkg.Manifest, string, int) {
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return nil, "", 2
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		err := output.NewError("PLAN_PATH_REQUIRED", "Provide exactly one plan manifest path.")
		writeCLIError(stderr, jsonOut, err)
		return nil, "", 2
	}
	path := strings.TrimSpace(positional[0])
	manifest, err := planpkg.ParseFile(path)
	if err != nil {
		appErr := output.NewErrorWithDetails("PLAN_PARSE_FAILED", "Could not parse the plan manifest.", err.Error())
		writeCLIError(stderr, jsonOut, appErr)
		return nil, path, 2
	}
	return manifest, path, 0
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (a *app) runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printContextUsage(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printContextUsage(stdout)
		return 0
	case "list":
		return a.runContextList(args[1:], stdout, stderr)
	case "show":
		return a.runContextShow(args[1:], stdout, stderr)
	case "use":
		return a.runContextUse(args[1:], stdout, stderr)
	case "create":
		return a.runContextCreate(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return a.runContextReconcile(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_CONTEXT_COMMAND", "Unknown context command. Run dharana context help for usage."))
		return 2
	}
}

func (a *app) runContextList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := a.configStore().Load()
	if err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration."))
		return 2
	}
	if cfg == nil {
		cfg = &config.File{}
	}
	result := map[string]any{"active_context": cfg.ActiveContext, "contexts": cfg.Contexts}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "context.list", result)
		return 0
	}
	for _, contextValue := range cfg.Contexts {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", contextValue.Name, contextValue.Project.GID, contextValue.Project.Name)
	}
	return 0
}

func (a *app) runContextShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := a.effectiveConfigStore().Load()
	if err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration."))
		return 2
	}
	if cfg == nil {
		cfg = &config.File{}
	}
	source := "active_project"
	if a.projectOverride != "" {
		source = "explicit"
	} else if cfg.ActiveContext != "" {
		source = "active_context"
	}
	result := map[string]any{"source": source, "active_context": cfg.ActiveContext, "project": cfg.ActiveProject}
	if local, err := config.LoadRepoContext(""); err == nil && local != nil && a.projectOverride == "" {
		result["repository_context"] = local
		if cfg.ActiveContext == local.Name {
			result["source"] = "repository_local"
		}
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "context.show", result)
		return 0
	}
	if cfg.ActiveProject == nil {
		_, _ = fmt.Fprintln(stdout, "No project context resolved.")
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", source, cfg.ActiveProject.GID, cfg.ActiveProject.Name)
	return 0
}

func (a *app) runContextUse(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context use", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(positional, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("CONTEXT_NAME_REQUIRED", "Provide a context name."))
		return 2
	}
	cfg, err := a.configStore().Load()
	if err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration."))
		return 2
	}
	if cfg == nil {
		cfg = &config.File{}
	}
	contextValue, ok := cfg.ContextByName(name)
	if !ok {
		if cfg.ActiveContext == name && cfg.ActiveProject != nil {
			contextValue = &config.Context{Name: name, Project: *cfg.ActiveProject}
		} else {
			writeCLIError(stderr, jsonOut, output.NewError("CONTEXT_NOT_FOUND", "No named context matched."))
			return 2
		}
	}
	cfg.ActiveContext = name
	projectValue := contextValue.Project
	cfg.ActiveProject = &projectValue
	if err := a.configStore().Save(cfg); err != nil {
		writeCLIError(stderr, jsonOut, output.NewError("CONFIG_WRITE_FAILED", "Could not save local configuration."))
		return 2
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "context.use", contextValue)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Using context %s.\n", name)
	return 0
}

func (a *app) runContextCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var projectGID string
	var local bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&local, "local", false, "Write repository-local context instead of user-level context")
	fs.StringVar(&projectGID, "project", "", "Asana project GID")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(positional, " "))
	if name == "" || strings.TrimSpace(projectGID) == "" {
		writeCLIError(stderr, jsonOut, output.NewError("CONTEXT_CREATE_ARGUMENTS_REQUIRED", "Provide context name and --project <gid>."))
		return 2
	}
	adopted, err := a.projectService().Adopt(ctx, project.AdoptOptions{Ref: projectGID, Context: name, Apply: true})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if local {
		contextValue := config.Context{Name: name, Project: config.ProjectConfig{GID: adopted.Project.GID, Name: adopted.Project.Name, WorkspaceGID: adopted.Project.WorkspaceGID, WorkspaceName: adopted.Project.WorkspaceName}}
		if err := config.SaveRepoContext("", contextValue); err != nil {
			writeCLIError(stderr, jsonOut, output.NewError("LOCAL_CONTEXT_WRITE_FAILED", "Could not save repository-local context."))
			return 2
		}
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "context.create", adopted)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Context %s created for %s.\n", name, adopted.Project.Name)
	return 0
}

func (a *app) runContextReconcile(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("context reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var dryRun bool
	var apply bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview reconciliation without mutation")
	fs.BoolVar(&apply, "apply", false, "Apply safe reconciliation operations")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := a.workService().ReconcileContext(ctx, work.ReconcileOptions{DryRun: dryRun, Apply: apply})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "context.reconcile", result)
		return 0
	}
	if result.Applied {
		_, _ = fmt.Fprintln(stdout, "Context reconciliation applied.")
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Context reconciliation found %d proposed operation(s).\n", len(result.Operations))
	return 0
}

func (a *app) runWorkflow(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printWorkflowUsage(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printWorkflowUsage(stdout)
		return 0
	case "inspect":
		result, err := a.projectService().InspectActive(ctx)
		return writeJSONOnly(stdout, stderr, "workflow.inspect", result, err)
	case "provision":
		return a.runWorkflowProvision(ctx, args[1:], stdout, stderr)
	case "bind":
		return a.runWorkflowBind(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_WORKFLOW_COMMAND", "Unknown workflow command. Run dharana workflow help for usage."))
		return 2
	}
}

func (a *app) runWorkflowProvision(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("workflow provision", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun, apply bool
	var mode string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview provisioning")
	fs.BoolVar(&apply, "apply", false, "Apply supported provisioning")
	fs.StringVar(&mode, "mode", "", "Workflow mode: custom-fields or native-types")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := a.projectService().Provision(ctx, project.ProvisionOptions{Mode: mode, DryRun: dryRun, Apply: apply})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "workflow.provision", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Workflow provision mode=%s supported=%t applied=%t.\n", result.Mode, result.Supported, result.Applied)
	return 0
}

func (a *app) runWorkflowBind(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("workflow bind", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var mode string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.StringVar(&mode, "mode", "", "Workflow mode: native-types or custom-fields")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := a.projectService().Provision(ctx, project.ProvisionOptions{Mode: mode, DryRun: true})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "workflow.bind", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Workflow bind inspected mode=%s supported=%t.\n", result.Mode, result.Supported)
	return 0
}

func (a *app) runType(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		writeCLIError(stderr, false, output.NewError("UNKNOWN_TYPE_COMMAND", "Run dharana type list --json."))
		return 2
	}
	fs := flag.NewFlagSet("type list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	inspect, err := a.projectService().InspectActive(ctx)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	result := inspect.Mappings.TaskTypes
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "type.list", result)
		return 0
	}
	for _, item := range result {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Name, item.Status, item.Configured)
	}
	return 0
}

func (a *app) runField(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		writeCLIError(stderr, false, output.NewError("UNKNOWN_FIELD_COMMAND", "Run dharana field list --json."))
		return 2
	}
	fs := flag.NewFlagSet("field list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	inspect, err := a.projectService().InspectActive(ctx)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "field.list", inspect.Fields)
		return 0
	}
	for _, field := range inspect.Fields {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", field.GID, field.Type, field.Name)
	}
	return 0
}

func writeJSONOnly(stdout, stderr io.Writer, operation string, result any, err error) int {
	if err != nil {
		writeCLIError(stderr, true, err)
		return exitCodeForError(err)
	}
	_ = output.WriteOperationJSON(stdout, operation, result)
	return 0
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
	case "list":
		return a.runDependencyList(ctx, args[1:], stdout, stderr)
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
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	blocked := strings.TrimSpace(strings.Join(positional, " "))
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
		return exitCodeForError(err)
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
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	blocked := strings.TrimSpace(strings.Join(positional, " "))
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
		return exitCodeForError(err)
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

func (a *app) runDependencyList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dependency list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	result, err := a.workService().DependencyList(ctx, ref)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "dependency.list", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%d blocker(s), %d direct dependent(s).\n", len(result.Blockers), len(result.DirectDependents))
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
		return exitCodeForError(err)
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
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}

	result, err := a.workService().ResolveRef(ctx, ref)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
	case "get":
		return a.runWorkGet(ctx, args[1:], stdout, stderr)
	case "update":
		return a.runWorkUpdate(ctx, args[1:], stdout, stderr)
	case "complete":
		return a.runWorkComplete(ctx, args[1:], stdout, stderr, false)
	case "reopen":
		return a.runWorkComplete(ctx, args[1:], stdout, stderr, true)
	case "assign":
		return a.runWorkAssign(ctx, args[1:], stdout, stderr, false)
	case "unassign":
		return a.runWorkAssign(ctx, args[1:], stdout, stderr, true)
	case "schedule":
		return a.runWorkSchedule(ctx, args[1:], stdout, stderr)
	case "move":
		return a.runWorkMove(ctx, args[1:], stdout, stderr)
	case "comment":
		return a.runWorkComment(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return a.runWorkReconcile(ctx, args[1:], stdout, stderr)
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

func (a *app) runWorkGet(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	result, err := a.workService().GetWork(ctx, ref)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.get", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", result.Item.Type, result.Item.Status, result.Item.GID, result.Item.Name)
	return 0
}

func (a *app) runWorkUpdate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun, clearAssignee, clearDueOn bool
	var name, notes, descriptionFile, assignee, dueOn, priority, component string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutating Asana")
	fs.BoolVar(&clearAssignee, "clear-assignee", false, "Clear the current assignee")
	fs.BoolVar(&clearDueOn, "clear-due-on", false, "Clear the current due date")
	fs.StringVar(&name, "name", "", "New work name")
	fs.StringVar(&notes, "notes", "", "New plain-text notes")
	fs.StringVar(&descriptionFile, "description-file", "", "Read a Markdown description from a file")
	fs.StringVar(&assignee, "assignee", "", "Assignee GID or exact email")
	fs.StringVar(&dueOn, "due-on", "", "Due date in YYYY-MM-DD format")
	fs.StringVar(&priority, "priority", "", "Priority enum value")
	fs.StringVar(&component, "component", "", "Component enum value")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	opts := work.UpdateWorkOptions{Ref: ref, DryRun: dryRun, ClearAssignee: clearAssignee, ClearDueOn: clearDueOn}
	description, descriptionErr := loadMarkdownDescription(descriptionFile)
	if descriptionErr != nil {
		writeCLIError(stderr, jsonOut, descriptionErr)
		return 2
	}
	opts.Description = description
	if flagWasSet(fs, "name") {
		opts.Name = &name
	}
	if flagWasSet(fs, "notes") {
		opts.Notes = &notes
	}
	if flagWasSet(fs, "assignee") {
		opts.Assignee = &assignee
	}
	if flagWasSet(fs, "due-on") {
		opts.DueOn = &dueOn
	}
	if flagWasSet(fs, "priority") {
		opts.Priority = &priority
	}
	if flagWasSet(fs, "component") {
		opts.Component = &component
	}
	result, err := a.workService().UpdateWork(ctx, opts)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.update", result)
		return 0
	}
	if result.Noop {
		_, _ = fmt.Fprintf(stdout, "No changes for %s.\n", result.Target.Ref)
		return 0
	}
	if result.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would update %s with %d change(s).\n", result.Target.Ref, len(result.Changes))
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Updated %s with %d change(s).\n", result.Target.Ref, len(result.Changes))
	return 0
}

func (a *app) runWorkComplete(ctx context.Context, args []string, stdout, stderr io.Writer, reopen bool) int {
	fs := flag.NewFlagSet("work complete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutating Asana")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	op := "work.complete"
	if reopen {
		op = "work.reopen"
	}
	result, err := a.workService().CompleteWork(ctx, work.CompleteWorkOptions{Ref: ref, DryRun: dryRun, Reopen: reopen})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, op, result)
		return 0
	}
	if result.Noop {
		_, _ = fmt.Fprintf(stdout, "No completion-state change for %s.\n", result.Target.Ref)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Updated completion state for %s.\n", result.Target.Ref)
	return 0
}

func (a *app) runWorkAssign(ctx context.Context, args []string, stdout, stderr io.Writer, clear bool) int {
	fs := flag.NewFlagSet("work assign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	var assignee string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutating Asana")
	fs.StringVar(&assignee, "assignee", "", "Assignee GID or exact email")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	opts := work.UpdateWorkOptions{Ref: ref, DryRun: dryRun, ClearAssignee: clear}
	if !clear {
		if strings.TrimSpace(assignee) == "" {
			writeCLIError(stderr, jsonOut, output.NewError("USER_REQUIRED", "Provide --assignee with an Asana user GID or exact email."))
			return 2
		}
		opts.Assignee = &assignee
	}
	result, err := a.workService().UpdateWork(ctx, opts)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	op := "work.assign"
	if clear {
		op = "work.unassign"
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, op, result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Assignment change for %s: %d change(s).\n", result.Target.Ref, len(result.Changes))
	return 0
}

func (a *app) runWorkSchedule(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work schedule", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun, clearDueOn bool
	var dueOn string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutating Asana")
	fs.BoolVar(&clearDueOn, "clear-due-on", false, "Clear the current due date")
	fs.StringVar(&dueOn, "due-on", "", "Due date in YYYY-MM-DD format")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	opts := work.UpdateWorkOptions{Ref: ref, DryRun: dryRun, ClearDueOn: clearDueOn}
	if !clearDueOn {
		if !flagWasSet(fs, "due-on") {
			writeCLIError(stderr, jsonOut, output.NewError("DUE_ON_REQUIRED", "Provide --due-on or --clear-due-on."))
			return 2
		}
		opts.DueOn = &dueOn
	}
	result, err := a.workService().UpdateWork(ctx, opts)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.schedule", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Schedule change for %s: %d change(s).\n", result.Target.Ref, len(result.Changes))
	return 0
}

func (a *app) runWorkMove(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work move", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	var parentRef string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutating Asana")
	fs.StringVar(&parentRef, "parent", "", "New parent reference")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	result, err := a.workService().MoveWork(ctx, work.MoveWorkOptions{Ref: ref, ParentRef: parentRef, DryRun: dryRun})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.move", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Move for %s: %d operation(s).\n", result.Target.Ref, len(result.Operations))
	return 0
}

func (a *app) runWorkComment(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work comment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	var body string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutating Asana")
	fs.StringVar(&body, "body", "", "Plain-text comment body")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	if ref == "" {
		writeCLIError(stderr, jsonOut, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID."))
		return 2
	}
	result, err := a.workService().CommentWork(ctx, work.CommentWorkOptions{Ref: ref, Body: body, DryRun: dryRun})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.comment", result)
		return 0
	}
	if result.DryRun {
		_, _ = fmt.Fprintf(stdout, "Would comment on %s.\n", result.Target.Ref)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Commented on %s.\n", result.Target.Ref)
	return 0
}

func (a *app) runWorkReconcile(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("work reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun, apply bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without mutation")
	fs.BoolVar(&apply, "apply", false, "Apply safe reconciliation operations")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	result, err := a.workService().ReconcileWork(ctx, work.ReconcileOptions{Ref: ref, DryRun: dryRun, Apply: apply})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "work.reconcile", result)
		return 0
	}
	if result.Applied {
		_, _ = fmt.Fprintln(stdout, "Work reconciliation applied.")
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Work reconciliation found %d proposed operation(s).\n", len(result.Operations))
	return 0
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
	var idempotencyKey string
	var parentRef string
	var assignee string
	var dueOn string
	var estimate string
	var notes, descriptionFile string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name task instead of failing")
	fs.StringVar(&idempotencyKey, "idempotency-key", "", "Optional retry key that enables idempotent exact-match creation")
	fs.StringVar(&parentRef, "parent", "", "Parent story, bug, spike, or task reference")
	fs.StringVar(&assignee, "assignee", "", "Optional assignee identifier or email")
	fs.StringVar(&dueOn, "due-on", "", "Optional due date")
	fs.StringVar(&estimate, "estimate", "", "Optional estimate")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	fs.StringVar(&descriptionFile, "description-file", "", "Read a Markdown description from a file")
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

	description, err := loadMarkdownDescription(descriptionFile)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.workService().CreateImplementationTask(ctx, work.CreateTaskOptions{
		Name:           name,
		ParentRef:      parentRef,
		Assignee:       assignee,
		DueOn:          dueOn,
		Estimate:       estimate,
		Notes:          notes,
		Description:    description,
		DryRun:         dryRun,
		Idempotent:     idempotent,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
	var idempotencyKey string
	var epicRef string
	var timebox string
	var notes, descriptionFile string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name spike instead of failing")
	fs.StringVar(&idempotencyKey, "idempotency-key", "", "Optional retry key that enables idempotent exact-match creation")
	fs.StringVar(&epicRef, "epic", "", "Epic reference by GID, EPIC:<name>, or exact name")
	fs.StringVar(&timebox, "timebox", "", "Optional investigation time-box")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	fs.StringVar(&descriptionFile, "description-file", "", "Read a Markdown description from a file")
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

	description, err := loadMarkdownDescription(descriptionFile)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.workService().CreateSpike(ctx, work.CreateSpikeOptions{
		Name:           name,
		EpicRef:        epicRef,
		Timebox:        timebox,
		Notes:          notes,
		Description:    description,
		DryRun:         dryRun,
		Idempotent:     idempotent,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
	var idempotencyKey string
	var epicRef string
	var priority string
	var environment string
	var notes, descriptionFile string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name bug instead of failing")
	fs.StringVar(&idempotencyKey, "idempotency-key", "", "Optional retry key that enables idempotent exact-match creation")
	fs.StringVar(&epicRef, "epic", "", "Epic reference by GID, EPIC:<name>, or exact name")
	fs.StringVar(&priority, "priority", "", "Bug priority")
	fs.StringVar(&environment, "environment", "", "Bug environment")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	fs.StringVar(&descriptionFile, "description-file", "", "Read a Markdown description from a file")
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

	description, err := loadMarkdownDescription(descriptionFile)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.workService().CreateBug(ctx, work.CreateBugOptions{
		Name:           name,
		EpicRef:        epicRef,
		Priority:       priority,
		Environment:    environment,
		Notes:          notes,
		Description:    description,
		DryRun:         dryRun,
		Idempotent:     idempotent,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
	var idempotencyKey string
	var epicRef string
	var notes, descriptionFile string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name story instead of failing")
	fs.StringVar(&idempotencyKey, "idempotency-key", "", "Optional retry key that enables idempotent exact-match creation")
	fs.StringVar(&epicRef, "epic", "", "Epic reference by GID, EPIC:<name>, or exact name")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	fs.StringVar(&descriptionFile, "description-file", "", "Read a Markdown description from a file")
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

	description, err := loadMarkdownDescription(descriptionFile)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.workService().CreateStory(ctx, work.CreateStoryOptions{
		Name:           name,
		EpicRef:        epicRef,
		Notes:          notes,
		Description:    description,
		DryRun:         dryRun,
		Idempotent:     idempotent,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
	var idempotencyKey string
	var notes, descriptionFile string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview without creating an Asana task")
	fs.BoolVar(&idempotent, "idempotent", false, "Return an existing exact-name epic instead of failing")
	fs.StringVar(&idempotencyKey, "idempotency-key", "", "Optional retry key that enables idempotent exact-match creation")
	fs.StringVar(&notes, "notes", "", "Optional Asana task notes")
	fs.StringVar(&descriptionFile, "description-file", "", "Read a Markdown description from a file")
	nameArgs, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(nameArgs, " "))
	if name == "" {
		writeCLIError(stderr, jsonOut, output.NewError("EPIC_NAME_REQUIRED", "Provide an epic name."))
		return 2
	}

	description, err := loadMarkdownDescription(descriptionFile)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return 2
	}
	result, err := a.workService().CreateEpic(ctx, work.CreateEpicOptions{
		Name:           name,
		Notes:          notes,
		Description:    description,
		DryRun:         dryRun,
		Idempotent:     idempotent,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func loadMarkdownDescription(path string) (*richtext.Description, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, output.NewErrorWithDetails("DESCRIPTION_FILE_READ_FAILED", "Could not read the Markdown description file.", err.Error())
	}
	description := &richtext.Description{Format: "markdown", Content: string(data)}
	if err := description.Validate(); err != nil {
		return nil, output.NewErrorWithDetails("INVALID_MARKDOWN_DESCRIPTION", "The Markdown description cannot be rendered safely.", err.Error())
	}
	return description, nil
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
	case "inspect":
		return a.runProjectInspect(ctx, args[1:], stdout, stderr)
	case "adopt":
		return a.runProjectAdopt(ctx, args[1:], stdout, stderr)
	case "create":
		return a.runProjectCreate(ctx, args[1:], stdout, stderr)
	case "create-from-template":
		return a.runProjectCreateFromTemplate(ctx, args[1:], stdout, stderr)
	case "member":
		return a.runProjectMember(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_PROJECT_COMMAND", "Unknown project command. Run dharana project help for usage."))
		return 2
	}
}

func (a *app) runProjectInspect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(fs.Args(), " "))
	result, err := a.projectService().Inspect(ctx, ref)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.inspect", result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%s\tready=%t\tproblems=%d\n", result.Project.Name, result.Ready, len(result.Problems))
	return 0
}

func (a *app) runProjectAdopt(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project adopt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun, apply bool
	var contextName string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview adoption")
	fs.BoolVar(&apply, "apply", false, "Apply local adoption configuration")
	fs.StringVar(&contextName, "context", "", "Named context to create or update")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	ref := strings.TrimSpace(strings.Join(positional, " "))
	result, err := a.projectService().Adopt(ctx, project.AdoptOptions{Ref: ref, Context: contextName, DryRun: dryRun, Apply: apply})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.adopt", result)
		return 0
	}
	if result.Applied {
		_, _ = fmt.Fprintf(stdout, "Adopted %s as context %s.\n", result.Project.Name, result.ContextName)
	} else {
		_, _ = fmt.Fprintf(stdout, "Would adopt %s as context %s.\n", result.Project.Name, result.ContextName)
	}
	return 0
}

func (a *app) runProjectCreate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	var workspace, team, privacy string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview project creation")
	fs.StringVar(&workspace, "workspace", "", "Workspace GID")
	fs.StringVar(&team, "team", "", "Team GID when required by Asana")
	fs.StringVar(&privacy, "privacy", "", "Privacy intent: private or team")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	name := strings.TrimSpace(strings.Join(positional, " "))
	result, err := a.projectService().Create(ctx, project.CreateOptions{Name: name, WorkspaceGID: workspace, TeamGID: team, Privacy: privacy, DryRun: dryRun})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.create", result)
		return 0
	}
	if result.Created && result.Project != nil {
		_, _ = fmt.Fprintf(stdout, "Created project %s (%s).\n", result.Project.Name, result.Project.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Would create project %q.\n", name)
	return 0
}

func (a *app) runProjectCreateFromTemplate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project create-from-template", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	var name string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview template instantiation")
	fs.StringVar(&name, "name", "", "Created project name")
	positional, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return 2
	}
	templateGID := strings.TrimSpace(strings.Join(positional, " "))
	result, err := a.projectService().CreateFromTemplate(ctx, project.TemplateOptions{TemplateGID: templateGID, Name: name, DryRun: dryRun})
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.create_from_template", result)
		return 0
	}
	if result.Job != nil {
		_, _ = fmt.Fprintf(stdout, "Template job started: %s.\n", result.Job.GID)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "Would instantiate template %s.\n", templateGID)
	return 0
}

func (a *app) runProjectMember(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printProjectUsage(stderr)
		return 2
	}
	switch args[0] {
	case "list":
		return a.runProjectMemberList(ctx, args[1:], stdout, stderr)
	case "add":
		return a.runProjectMemberAdd(ctx, args[1:], stdout, stderr)
	case "remove":
		return a.runProjectMemberRemove(ctx, args[1:], stdout, stderr)
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_PROJECT_MEMBER_COMMAND", "Unknown project member command."))
		return 2
	}
}

func (a *app) runProjectMemberList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("project member list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	result, err := a.projectService().ListMembers(ctx)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, "project.member.list", result)
		return 0
	}
	for _, member := range result.Members {
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", member.GID, member.Email, member.Name)
	}
	return 0
}

func (a *app) runProjectMemberAdd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return a.runProjectMemberMutation(ctx, args, stdout, stderr, true)
}

func (a *app) runProjectMemberRemove(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return a.runProjectMemberMutation(ctx, args, stdout, stderr, false)
}

func (a *app) runProjectMemberMutation(ctx context.Context, args []string, stdout, stderr io.Writer, add bool) int {
	fs := flag.NewFlagSet("project member mutation", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut, dryRun bool
	var user string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview membership change")
	fs.StringVar(&user, "user", "", "Asana user GID or exact email")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var result *project.MemberMutationResult
	var err error
	operation := "project.member.remove"
	if add {
		operation = "project.member.add"
		result, err = a.projectService().AddMember(ctx, project.MemberOptions{User: user, DryRun: dryRun})
	} else {
		result, err = a.projectService().RemoveMember(ctx, project.MemberOptions{User: user, DryRun: dryRun})
	}
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
	}
	if jsonOut {
		_ = output.WriteOperationJSON(stdout, operation, result)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "%s\t%s\n", operation, result.User.Name)
	return 0
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
		appErr := output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
		writeCLIError(stderr, jsonOut, appErr)
		return exitCodeForError(appErr)
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
		appErr := output.NewError("CONFIG_WRITE_FAILED", "Could not save local configuration.")
		writeCLIError(stderr, jsonOut, appErr)
		return exitCodeForError(appErr)
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
		appErr := output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
		writeCLIError(stderr, jsonOut, appErr)
		return exitCodeForError(appErr)
	}
	if priorityGID != "" {
		cfg.Fields.PriorityGID = priorityGID
	}
	if componentGID != "" {
		cfg.Fields.ComponentGID = componentGID
	}
	if err := a.configStore().Save(cfg); err != nil {
		appErr := output.NewError("CONFIG_WRITE_FAILED", "Could not save local configuration.")
		writeCLIError(stderr, jsonOut, appErr)
		return exitCodeForError(appErr)
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
	var repairPlan bool
	var repair bool
	var dryRun bool
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
	fs.BoolVar(&repairPlan, "repair-plan", false, "Return a structured repair plan")
	fs.BoolVar(&repair, "repair", false, "Return repair actions; currently supports dry-run only")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview repair actions")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if repair && !dryRun {
		writeCLIError(stderr, jsonOut, output.NewError("REPAIR_DRY_RUN_REQUIRED", "doctor --repair currently requires --dry-run."))
		return 2
	}

	result, err := a.doctorService().RunWithOptions(ctx, repairPlan || repair, dryRun)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
		return 2
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
			appErr := output.NewError("STDIN_READ_FAILED", "Could not read token from stdin.")
			writeCLIError(stderr, jsonOut, appErr)
			return exitCodeForError(appErr)
		}
	}

	result, err := a.auth.Configure(ctx, token, validate)
	if err != nil {
		writeCLIError(stderr, jsonOut, err)
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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
		return exitCodeForError(err)
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

	var appErr *output.AppError
	if !errors.As(err, &appErr) {
		_, _ = fmt.Fprintln(w, "error: An unexpected error occurred.")
		return
	}
	_, _ = fmt.Fprintf(w, "error[%s]: %s\n", appErr.Code, appErr.Message)
}

func exitCodeForError(err error) int {
	var appErr *output.AppError
	if !errors.As(err, &appErr) {
		return 1
	}
	code := appErr.Code
	switch {
	case code == "INVALID_AUTH" || code == "TOKEN_NOT_CONFIGURED" || code == "TOKEN_READ_FAILED" || code == "MISSING_TOKEN":
		return 3
	case strings.HasPrefix(code, "AMBIGUOUS_"):
		return 4
	case code == "PLAN_CONFLICT" || code == "PLAN_ADOPTION_CONFLICT" || code == "BINDING_TYPE_MISMATCH":
		return 4
	case strings.HasPrefix(code, "ASANA_"):
		return 5
	case code == "PLAN_PARTIAL_APPLY" || code == "PLAN_NOT_CONVERGED" || code == "PLAN_VERIFY_FAILED":
		return 6
	default:
		return 2
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
dharana is an agent-native work graph CLI for Asana.

Usage:
  dharana auth configure --token <pat> [--validate] [--json]
  dharana auth configure --stdin [--validate] [--json]
  dharana auth status [--json]
  dharana auth validate [--json]
  dharana version [--json]
  dharana capabilities [--json]
  dharana help [<command>] [--json]
  dharana context list [--json]
  dharana context show [--json]
  dharana context use <name> [--json]
  dharana context create <name> --project <gid> [--local] [--json]
  dharana context reconcile [--dry-run|--apply] [--json]
  dharana project list [--workspace-gid <gid>] [--json]
  dharana project select --gid <gid> [--json]
  dharana project select --name <exact-name> [--workspace-gid <gid>] [--json]
  dharana project inspect [<project-ref>] [--json]
  dharana project adopt <project-ref> [--dry-run|--apply] [--context <name>] [--json]
  dharana project create <name> --workspace <gid> [--team <gid>] [--privacy private|team] [--dry-run] [--json]
  dharana project create-from-template <template-gid> --name <name> [--dry-run] [--json]
  dharana project member list [--json]
  dharana project member add --user <email-or-gid> [--dry-run] [--json]
  dharana project member remove --user <gid> [--dry-run] [--json]
  dharana workflow inspect [--json]
  dharana workflow provision --mode custom-fields|native-types [--dry-run|--apply] [--json]
  dharana workflow bind --mode native-types|custom-fields [--json]
  dharana type list [--json]
  dharana field list [--json]
  dharana config show [--json]
  dharana config set-task-types [--field-gid <gid>] --epic <value> --story <value> --bug <value> --spike <value> [--json]
  dharana config set-fields [--priority-gid <gid>] [--component-gid <gid>] [--json]
  dharana doctor [--json]
  dharana epic create <name> [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
  dharana story create --epic <ref> <name> [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
  dharana bug create --epic <ref> <name> [--priority <value>] [--environment <value>] [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
  dharana spike create --epic <ref> <name> [--timebox <value>] [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
  dharana task create --parent <ref> <name> [--assignee <value>] [--due-on <date>] [--estimate <value>] [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
  dharana dependency add <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana dependency remove <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana dependency list <ref> [--json]
  dharana work list [--type <type>] [--status <status>] [--epic <ref>] [--limit <n>] [--offset <offset>] [--json]
  dharana work get <ref> [--json]
  dharana work update <ref> [--name <name>] [--notes <text>|--description-file <markdown>] [--assignee <user>] [--clear-assignee] [--due-on <date>] [--clear-due-on] [--priority <value>] [--component <value>] [--dry-run] [--json]
  dharana work complete <ref> [--dry-run] [--json]
  dharana work reopen <ref> [--dry-run] [--json]
  dharana work assign <ref> --assignee <user> [--dry-run] [--json]
  dharana work unassign <ref> [--dry-run] [--json]
  dharana work schedule <ref> (--due-on <date>|--clear-due-on) [--dry-run] [--json]
  dharana work move <ref> --parent <ref> [--dry-run] [--json]
  dharana work comment <ref> --body <text> [--dry-run] [--json]
  dharana work reconcile [<ref>] [--dry-run|--apply] [--json]
  dharana work tree [--epic <ref>] [--json]
  dharana work blocked [--type <type>] [--epic <ref>] [--json]
  dharana work ready [--type <type>] [--epic <ref>] [--priority <value>] [--component <value>] [--json]
  dharana work graph [--epic <ref>] [--format json|mermaid] [--json]
  dharana plan validate <file> [--remote] [--json]
  dharana plan schema [--json]
  dharana plan diff <file> [--json]
  dharana plan adopt <file> [--dry-run|--apply] [--json]
  dharana plan apply <file> [--dry-run] [--json]
  dharana plan status <file> [--json]
  dharana plan reconcile <file> [--dry-run|--apply] [--json]
  dharana plan export --epic <ref> --output <file> [--json]
  dharana plan bindings <file> [--json]
  dharana plan bind <file> --id <logical-id> --gid <asana-gid> [--dry-run|--apply] [--json]
  dharana plan unbind <file> --id <logical-id> [--dry-run|--apply] [--json]
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
  dharana project inspect [<project-ref>] [--json]
  dharana project adopt <project-ref> [--dry-run|--apply] [--context <name>] [--json]
  dharana project create <name> --workspace <gid> [--team <gid>] [--privacy private|team] [--dry-run] [--json]
  dharana project create-from-template <template-gid> --name <name> [--dry-run] [--json]
  dharana project member list [--json]
  dharana project member add --user <email-or-gid> [--dry-run] [--json]
  dharana project member remove --user <gid> [--dry-run] [--json]
`)+"\n")
}

func printContextUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana context list [--json]
  dharana context show [--json]
  dharana context use <name> [--json]
  dharana context create <name> --project <gid> [--local] [--json]
  dharana context reconcile [--dry-run|--apply] [--json]
  dharana --project <gid> work ready --json
`)+"\n")
}

func printWorkflowUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana workflow inspect [--json]
  dharana workflow provision --mode custom-fields|native-types [--dry-run|--apply] [--json]
  dharana workflow bind --mode native-types|custom-fields [--json]
  dharana type list [--json]
  dharana field list [--json]
  dharana doctor [--repair-plan] [--json]
  dharana doctor --repair --dry-run --json
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
  dharana epic create <name> [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
`)+"\n")
}

func printStoryUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana story create --epic <ref> <name> [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
`)+"\n")
}

func printBugUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana bug create --epic <ref> <name> [--priority <value>] [--environment <value>] [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
`)+"\n")
}

func printSpikeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana spike create --epic <ref> <name> [--timebox <value>] [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
`)+"\n")
}

func printTaskUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana task create --parent <ref> <name> [--assignee <value>] [--due-on <date>] [--estimate <value>] [--notes <text>|--description-file <markdown>] [--dry-run] [--idempotent] [--idempotency-key <key>] [--json]
`)+"\n")
}

func printDependencyUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana dependency add <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana dependency remove <ref> --blocked-by <ref> [--dry-run] [--json]
  dharana dependency list <ref> [--json]
`)+"\n")
}

func printWorkUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana work list [--type <type>] [--status <status>] [--epic <ref>] [--limit <n>] [--offset <offset>] [--json]
  dharana work get <ref> [--json]
  dharana work update <ref> [--name <name>] [--notes <text>|--description-file <markdown>] [--assignee <user>] [--clear-assignee] [--due-on <date>] [--clear-due-on] [--priority <value>] [--component <value>] [--dry-run] [--json]
  dharana work complete <ref> [--dry-run] [--json]
  dharana work reopen <ref> [--dry-run] [--json]
  dharana work assign <ref> --assignee <user> [--dry-run] [--json]
  dharana work unassign <ref> [--dry-run] [--json]
  dharana work schedule <ref> (--due-on <date>|--clear-due-on) [--dry-run] [--json]
  dharana work move <ref> --parent <ref> [--dry-run] [--json]
  dharana work comment <ref> --body <text> [--dry-run] [--json]
  dharana work reconcile [<ref>] [--dry-run|--apply] [--json]
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

func printPlanUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, strings.TrimSpace(`
Usage:
  dharana plan validate <file> [--remote] [--json]
  dharana plan schema [--json]
  dharana plan diff <file> [--json]
  dharana plan adopt <file> [--dry-run|--apply] [--json]
  dharana plan apply <file> [--dry-run] [--json]
  dharana plan status <file> [--json]
  dharana plan reconcile <file> [--dry-run|--apply] [--json]
  dharana plan export --epic <ref> --output <file> [--json]
  dharana plan bindings <file> [--json]
  dharana plan bind <file> --id <logical-id> --gid <asana-gid> [--dry-run|--apply] [--json]
  dharana plan unbind <file> --id <logical-id> [--dry-run|--apply] [--json]
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
		if a.projectOverride != "" {
			a.project.Config = a.effectiveConfigStore()
		}
		return a.project
	}
	service := project.NewService(a.auth)
	service.Config = a.effectiveConfigStore()
	return service
}

func (a *app) doctorService() *doctor.Service {
	if a.doctor != nil {
		if a.projectOverride != "" {
			a.doctor.Config = a.effectiveConfigStore()
		}
		return a.doctor
	}
	service := doctor.NewService(a.auth)
	service.Config = a.effectiveConfigStore()
	return service
}

func (a *app) configStore() *config.Store {
	if a.config != nil {
		return a.config
	}
	return config.NewStore()
}

func (a *app) workService() *work.Service {
	if a.work != nil {
		if a.projectOverride != "" {
			a.work.Config = a.effectiveConfigStore()
		}
		return a.work
	}
	service := work.NewService(a.auth)
	service.Config = a.effectiveConfigStore()
	return service
}

func (a *app) planService(manifest *planpkg.Manifest) *planpkg.Service {
	if a.plan != nil {
		return a.plan
	}
	baseStore := a.effectiveConfigStore()
	effective := baseStore
	if manifest != nil && strings.TrimSpace(manifest.Metadata.Context) != "" {
		if cfg, err := baseStore.Load(); err == nil && cfg != nil {
			if contextValue, ok := cfg.ContextByName(manifest.Metadata.Context); ok {
				copyValue := *cfg
				projectValue := contextValue.Project
				copyValue.ActiveProject = &projectValue
				copyValue.ActiveContext = contextValue.Name
				effective = &staticConfigStore{file: &copyValue}
			}
		}
	}
	if manifest != nil && strings.TrimSpace(manifest.Spec.Project) != "" {
		if cfg, err := effective.Load(); err == nil && cfg != nil {
			copyValue := *cfg
			projectValue := config.ProjectConfig{GID: strings.TrimSpace(manifest.Spec.Project)}
			if cfg.ActiveProject != nil {
				projectValue = *cfg.ActiveProject
				projectValue.GID = strings.TrimSpace(manifest.Spec.Project)
				projectValue.Name = ""
			}
			copyValue.ActiveProject = &projectValue
			copyValue.ActiveContext = ""
			effective = &staticConfigStore{file: &copyValue}
		}
	}
	workService := a.workService()
	workService.Config = effective
	service := planpkg.NewService(workService, effective)
	return service
}

type staticConfigStore struct {
	file *config.File
}

func (s *staticConfigStore) Load() (*config.File, error) {
	if s == nil || s.file == nil {
		return &config.File{}, nil
	}
	copyValue := *s.file
	if s.file.ActiveProject != nil {
		projectValue := *s.file.ActiveProject
		copyValue.ActiveProject = &projectValue
	}
	copyValue.Contexts = append([]config.Context(nil), s.file.Contexts...)
	return &copyValue, nil
}

func (s *staticConfigStore) Save(cfg *config.File) error {
	if cfg == nil {
		s.file = &config.File{}
		return nil
	}
	copyValue := *cfg
	s.file = &copyValue
	return nil
}

func (a *app) effectiveConfigStore() interface {
	Load() (*config.File, error)
	Save(*config.File) error
} {
	store := a.configStore()
	if a.projectOverride == "" {
		return &config.RepoContextStore{Base: store}
	}
	return &config.OverrideStore{Base: store, Project: &config.ProjectConfig{GID: a.projectOverride}}
}
