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
	"github.com/erikvoit/dharana-cli/internal/output"
)

type app struct {
	auth *auth.Service
}

func Run(args []string, stdout, stderr io.Writer) int {
	return (&app{auth: auth.NewService()}).run(context.Background(), args, stdout, stderr)
}

func (a *app) run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "auth":
		return a.runAuth(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		writeCLIError(stderr, false, output.NewError("UNKNOWN_COMMAND", "Unknown command. Run dharana help for usage."))
		return 2
	}
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
