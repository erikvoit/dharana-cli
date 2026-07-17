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
)

type app struct {
	auth    *auth.Service
	project *project.Service
	doctor  *doctor.Service
	config  *config.Store
}

func Run(args []string, stdout, stderr io.Writer) int {
	authService := auth.NewService()
	return (&app{
		auth:    authService,
		project: project.NewService(authService),
		doctor:  doctor.NewService(authService),
		config:  config.NewStore(),
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
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_COMMAND", "Unknown command. Run dharana help for usage."))
		return 2
	}
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
		_ = output.WriteJSON(stdout, result)
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
		_ = output.WriteJSON(stdout, result)
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
		_ = output.WriteJSON(stdout, cfg)
		return 0
	}
	if cfg.ActiveProject == nil {
		_, _ = fmt.Fprintln(stdout, "Active project: not configured")
	} else {
		_, _ = fmt.Fprintf(stdout, "Active project: %s (%s)\n", cfg.ActiveProject.Name, cfg.ActiveProject.GID)
	}
	_, _ = fmt.Fprintf(stdout, "Task types: epic=%q story=%q bug=%q spike=%q\n", cfg.TaskTypes.Epic, cfg.TaskTypes.Story, cfg.TaskTypes.Bug, cfg.TaskTypes.Spike)
	return 0
}

func (a *app) runConfigSetTaskTypes(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config set-task-types", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var jsonOut bool
	var epic, story, bug, spike string
	fs.BoolVar(&jsonOut, "json", false, "Return JSON output")
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
		_ = output.WriteJSON(stdout, cfg)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, "Task type mappings updated.")
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
		_ = output.WriteJSON(stdout, result)
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
		_ = output.WriteJSON(stdout, result)
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
		_ = output.WriteJSON(stdout, result)
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
		_ = output.WriteJSON(stdout, result)
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
  dharana config set-task-types --epic <value> --story <value> --bug <value> --spike <value> [--json]
  dharana doctor [--json]
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
  dharana config set-task-types --epic <value> --story <value> --bug <value> --spike <value> [--json]
`)+"\n")
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
