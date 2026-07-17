# dharana-cli

Dharana is an agent-native work graph CLI for Asana.

## Setup

### Prerequisites

- Go 1.24 or newer
- An Asana personal access token
- macOS Keychain access if you want persisted local token storage

### Configure Authentication

The recommended local setup on macOS is to store your Asana personal access token in Keychain. Dharana stores only the token secret there; project and workflow settings are stored separately in local config.

Step by step:

```bash
# 1. From the repo worktree, start a silent prompt for your PAT.
read -s ASANA_PAT

# 2. Paste the PAT, then press Enter. Nothing will echo to the terminal.

# 3. Store the PAT in macOS Keychain and validate it with Asana.
go run ./cmd/dharana auth configure --token "$ASANA_PAT" --validate --json

# 4. Remove the temporary shell variable.
unset ASANA_PAT
```

This avoids putting the token directly in your shell history.

You can confirm the CLI can find the token without printing it:

```bash
go run ./cmd/dharana auth status --json
```

Keychain is not required for every use. For one-off commands, CI, non-macOS environments, or temporary overrides, pass a token through an environment variable instead:

```bash
DHARANA_ASANA_PAT="$ASANA_PAT" go run ./cmd/dharana auth validate --json
```

Environment variables take precedence over Keychain. `DHARANA_ASANA_PAT` is preferred, and `ASANA_ACCESS_TOKEN` is also supported.

Dharana intentionally does not store PATs in plaintext config files. Local config is for non-secret project and workflow settings only.

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
  --field-gid "$ASANA_TASK_TYPE_FIELD_GID" \
  --epic Epic \
  --story Story \
  --bug Bug \
  --spike Spike \
  --json
```

Omit `--field-gid` if you only want local validation for now. Include it when `--epic`, `--story`, `--bug`, and `--spike` are Asana custom-field enum GIDs that the CLI should apply to created work.

### Create Work

Preview creating an epic in the active project:

```bash
go run ./cmd/dharana epic create "Card provisioning and recovery" --dry-run --json
```

Create the epic:

```bash
go run ./cmd/dharana epic create "Card provisioning and recovery" --json
```

If an exact-name epic already exists in the active project, creation fails with `DUPLICATE_EPIC`. Use `--idempotent` to return the existing epic instead.

Preview creating a story beneath an epic:

```bash
go run ./cmd/dharana story create \
  --epic "$ASANA_EPIC_GID" \
  "Customer can recover from failed provisioning" \
  --dry-run \
  --json
```

Create the story:

```bash
go run ./cmd/dharana story create \
  --epic "$ASANA_EPIC_GID" \
  "Customer can recover from failed provisioning" \
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
