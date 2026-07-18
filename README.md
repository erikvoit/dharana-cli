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

### Onboard a Project

Agents can discover Dharana's machine-readable command surface without authentication:

```bash
go run ./cmd/dharana version --json
go run ./cmd/dharana capabilities --json
go run ./cmd/dharana help "work ready" --json
```

Adopt an existing project and let Dharana discover compatible field mappings:

```bash
go run ./cmd/dharana project adopt "$ASANA_PROJECT_GID" --dry-run --json
go run ./cmd/dharana project adopt "$ASANA_PROJECT_GID" --apply --context payments --json
```

Named contexts prevent agents working in different projects from sharing hidden global state:

```bash
go run ./cmd/dharana context list --json
go run ./cmd/dharana context use payments --json
go run ./cmd/dharana context show --json
```

For repository-local context, write `.dharana/context.json` from the repo worktree:

```bash
go run ./cmd/dharana context create payments --project "$ASANA_PROJECT_GID" --local --json
```

An explicit root project override wins for one invocation:

```bash
go run ./cmd/dharana --project "$ASANA_PROJECT_GID" work ready --json
```

Inspect workflow readiness and membership:

```bash
go run ./cmd/dharana project inspect "$ASANA_PROJECT_GID" --json
go run ./cmd/dharana workflow inspect --json
go run ./cmd/dharana type list --json
go run ./cmd/dharana field list --json
go run ./cmd/dharana project member list --json
```

Provisioning support is intentionally conservative. Dry-runs describe remote and local mutations; unsupported account/API paths return structured remediation:

```bash
go run ./cmd/dharana workflow provision --mode custom-fields --dry-run --json
go run ./cmd/dharana workflow bind --mode native-types --json
go run ./cmd/dharana project create "Payments" --workspace "$ASANA_WORKSPACE_GID" --dry-run --json
go run ./cmd/dharana project create-from-template "$TEMPLATE_GID" --name "Payments" --dry-run --json
```

### Run Diagnostics

Run `doctor` to verify authentication, project access, and required workflow mappings:

```bash
go run ./cmd/dharana doctor --json
```

For agent setup flows, `doctor` can also return repair guidance:

```bash
go run ./cmd/dharana doctor --repair-plan --json
go run ./cmd/dharana doctor --repair --dry-run --json
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

Configure optional custom fields used for filtering:

```bash
go run ./cmd/dharana config set-fields \
  --priority-gid "$ASANA_PRIORITY_FIELD_GID" \
  --component-gid "$ASANA_COMPONENT_FIELD_GID" \
  --json
```

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

Create commands also accept `--idempotency-key <key>`, which enables idempotent exact-match creation and echoes the key in JSON output. For implementation tasks, the exact-match check is scoped to the requested parent.

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

Preview creating a bug beneath an epic:

```bash
go run ./cmd/dharana bug create \
  --epic "$ASANA_EPIC_GID" \
  --priority P1 \
  --environment 1841 \
  "Existing card displays failed-to-provision after refresh" \
  --dry-run \
  --json
```

Create the bug:

```bash
go run ./cmd/dharana bug create \
  --epic "$ASANA_EPIC_GID" \
  --priority P1 \
  --environment 1841 \
  "Existing card displays failed-to-provision after refresh" \
  --json
```

Preview creating a spike beneath an epic:

```bash
go run ./cmd/dharana spike create \
  --epic "$ASANA_EPIC_GID" \
  --timebox 4h \
  "Determine why provisioning differs between Evo and 1841" \
  --dry-run \
  --json
```

Create the spike:

```bash
go run ./cmd/dharana spike create \
  --epic "$ASANA_EPIC_GID" \
  --timebox 4h \
  "Determine why provisioning differs between Evo and 1841" \
  --json
```

Preview creating an implementation task beneath a story, bug, or spike:

```bash
go run ./cmd/dharana task create \
  --parent "$ASANA_PARENT_TASK_GID" \
  --assignee dev@example.com \
  --due-on 2026-07-18 \
  --estimate 2h \
  "Normalize provisioning-state persistence" \
  --dry-run \
  --json
```

Create the implementation task:

```bash
go run ./cmd/dharana task create \
  --parent "$ASANA_PARENT_TASK_GID" \
  "Normalize provisioning-state persistence" \
  --json
```

Preview adding a blocked-by relationship:

```bash
go run ./cmd/dharana dependency add "$ASANA_STORY_GID" \
  --blocked-by "$ASANA_BUG_GID" \
  --dry-run \
  --json
```

Add the dependency:

```bash
go run ./cmd/dharana dependency add "$ASANA_STORY_GID" \
  --blocked-by "$ASANA_BUG_GID" \
  --json
```

After running `refs refresh`, either side can also be a friendly reference such as `STORY:Customer can recover from failed provisioning`.

Preview removing a dependency:

```bash
go run ./cmd/dharana dependency remove "$ASANA_STORY_GID" \
  --blocked-by "$ASANA_BUG_GID" \
  --dry-run \
  --json
```

Remove the dependency:

```bash
go run ./cmd/dharana dependency remove "$ASANA_STORY_GID" \
  --blocked-by "$ASANA_BUG_GID" \
  --json
```

If the dependency is already absent, the command returns `found: false` without mutating Asana.

List active-project work:

```bash
go run ./cmd/dharana work list --json
```

Filter listed work by type, status, or epic:

```bash
go run ./cmd/dharana work list \
  --type story,bug \
  --status incomplete \
  --epic "$ASANA_EPIC_GID" \
  --limit 50 \
  --json
```

Use the returned `next_offset` value to request the next page:

```bash
go run ./cmd/dharana work list --offset "$NEXT_OFFSET" --json
```

Show the active project hierarchy as a tree:

```bash
go run ./cmd/dharana work tree --json
```

Scope the tree to one epic:

```bash
go run ./cmd/dharana work tree --epic "$ASANA_EPIC_GID" --json
```

List blocked work:

```bash
go run ./cmd/dharana work blocked --json
```

Filter blocked work by type or epic:

```bash
go run ./cmd/dharana work blocked \
  --type story,bug \
  --epic "$ASANA_EPIC_GID" \
  --json
```

List ready work, excluding completed items and items with unresolved blockers:

```bash
go run ./cmd/dharana work ready --json
```

Filter ready work by type, epic, priority, or component:

```bash
go run ./cmd/dharana work ready \
  --type story,bug \
  --priority P0,P1 \
  --component Cards \
  --epic "$ASANA_EPIC_GID" \
  --json
```

Export the dependency graph as JSON:

```bash
go run ./cmd/dharana work graph --json
```

Export the dependency graph as Mermaid:

```bash
go run ./cmd/dharana work graph \
  --epic "$ASANA_EPIC_GID" \
  --format mermaid
```

Cycle detection is included in JSON output and emitted as Mermaid comments.

### Resolve Friendly References

Refresh the local reference cache from the active Asana project:

```bash
go run ./cmd/dharana refs refresh --json
```

Resolve a cached friendly reference or raw Asana GID:

```bash
go run ./cmd/dharana refs resolve "STORY:Customer can recover from failed provisioning" --json
```

The cache is stored at `$XDG_CONFIG_HOME/dharana/refs.json` or `~/.config/dharana/refs.json`. Asana GIDs remain authoritative: resolving a cached reference validates that the cached GID still exists in Asana. If it no longer resolves, the CLI returns `STALE_REFERENCE` and you should run `refs refresh`.

All JSON responses use a stable envelope:

```json
{
  "ok": true,
  "operation": "work.ready",
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

Exit codes are stable for agent harnesses:

```text
0 success
2 validation, configuration, usage, or domain error
3 authentication or token error
4 ambiguous reference or selection
5 Asana API request or access failure
```

### Dry Runs

Mutation commands that create or change Asana work support `--dry-run`. Dry-run responses include the resolved entities and intended change in the same JSON envelope, but skip the mutating Asana request.
