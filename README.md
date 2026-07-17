# dharana-cli

Dharana is an agent-native work graph CLI for Asana.

## Setup

### Prerequisites

- Go 1.24 or newer
- macOS Keychain access for persisted local authentication
- An Asana personal access token

### Configure Authentication

The recommended local setup is to store your Asana personal access token in macOS Keychain. Dharana stores only the token secret there; project and workflow settings are stored separately in local config.

To avoid putting the token directly in your shell history, paste it into a temporary prompt:

```bash
read -s ASANA_PAT
go run ./cmd/dharana auth configure --token "$ASANA_PAT" --validate --json
unset ASANA_PAT
```

You can confirm the CLI can find the token without printing it:

```bash
go run ./cmd/dharana auth status --json
```

For one-off use, you can pass a token through an environment variable instead of storing it:

```bash
DHARANA_ASANA_PAT="$ASANA_PAT" go run ./cmd/dharana auth validate --json
```

Environment variables take precedence over Keychain, which is useful for temporary overrides and automation.

### Select a Project

List projects visible to the configured token:

```bash
go run ./cmd/dharana project list --json
```

Select the active project by GID:

```bash
go run ./cmd/dharana project select --gid "$ASANA_PROJECT_GID" --json
```

Or select by exact name:

```bash
go run ./cmd/dharana project select --name "Personal software agile board" --json
```

Show the saved local config:

```bash
go run ./cmd/dharana config show --json
```

Local configuration is saved at `$XDG_CONFIG_HOME/dharana/config.json` or `~/.config/dharana/config.json`.

### Run Diagnostics

Run `doctor` to verify authentication, project access, and required workflow mappings:

```bash
go run ./cmd/dharana doctor --json
```

Configure task type or work-type mappings once you know the Asana values this project should use:

```bash
go run ./cmd/dharana config set-task-types \
  --epic Epic \
  --story Story \
  --bug Bug \
  --spike Spike \
  --json
```

## Story 1.1: Configure an Asana Personal Access Token

The first MVP slice supports configuring and validating an Asana personal access token without storing it in plaintext project configuration.

Tokens are resolved in this order:

1. `DHARANA_ASANA_PAT`
2. `ASANA_ACCESS_TOKEN`
3. macOS Keychain item `dharana-cli/asana-pat`

Configure a token in the operating-system keychain:

```bash
go run ./cmd/dharana auth configure --token "$ASANA_PAT" --json
```

Configure and validate the token with Asana:

```bash
go run ./cmd/dharana auth configure --token "$ASANA_PAT" --validate --json
```

Validate the currently resolved token:

```bash
go run ./cmd/dharana auth validate --json
```

Check whether a token is configured:

```bash
go run ./cmd/dharana auth status --json
```

## Story 1.2: Select an Active Project

List Asana projects visible to the configured token:

```bash
go run ./cmd/dharana project list --json
```

Select an active project by GID:

```bash
go run ./cmd/dharana project select --gid "$ASANA_PROJECT_GID" --json
```

Select an active project by exact name:

```bash
go run ./cmd/dharana project select --name "Personal software agile board" --json
```

If multiple projects share the same exact name, the command fails with `AMBIGUOUS_PROJECT` and returns candidates.

Show local configuration:

```bash
go run ./cmd/dharana config show --json
```

Local configuration is saved at `$XDG_CONFIG_HOME/dharana/config.json` or `~/.config/dharana/config.json`.

## Story 1.3: Validate Workspace Configuration

Run diagnostics:

```bash
go run ./cmd/dharana doctor --json
```

`doctor` checks:

- Asana authentication
- active project access
- required Epic, Story, Bug, and Spike type/work-type mappings

Configure local task type mappings:

```bash
go run ./cmd/dharana config set-task-types \
  --epic Epic \
  --story Story \
  --bug Bug \
  --spike Spike \
  --json
```

All JSON responses use a stable envelope:

```json
{
  "ok": true,
  "data": {}
}
```

Errors use stable codes:

```json
{
  "ok": false,
  "error": {
    "code": "INVALID_AUTH",
    "message": "Asana rejected the configured token."
  }
}
```

The CLI masks tokens in all command output.
