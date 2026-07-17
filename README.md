# dharana-cli

Dharana is an agent-native work graph CLI for Asana.

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
