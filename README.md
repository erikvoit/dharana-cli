# dharana-cli

Dharana is an agent-native work graph CLI for Asana: small, scriptable, JSON-first, and deliberately shaped around delivery work instead of general Asana administration.

[![CLI 0.3.0](https://img.shields.io/badge/CLI-0.3.0-2f6fed)](#)
[![Capability Schema mvp-plus-2](https://img.shields.io/badge/capabilities-mvp--plus--2-6f42c1)](#)
[![Config Schema v1](https://img.shields.io/badge/config-v1-0a7f42)](#)
[![Cache Schema v1](https://img.shields.io/badge/cache-v1-0a7f42)](#)
[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00add8)](https://go.dev/)
[![Asana API](https://img.shields.io/badge/Asana-work%20graph-f06a6a)](https://developers.asana.com/)

## Why Dharana Is Opinionated

Dharana treats Asana as a focused execution graph for agents, not as a blank canvas. The CLI assumes work should have a predictable shape:

```text
Epic
  Story | Bug | Spike
    Implementation task
```

That shape is intentionally narrower than Asana itself. Agents need stable references, clear hierarchy, deterministic JSON, and safe lifecycle commands more than they need every possible workspace feature. By using epics as top-level Asana tasks, stories/bugs/spikes as first-level subtasks, and implementation tasks beneath executable work, Dharana can answer practical delivery questions consistently:

- What is ready to pick up?
- What is blocked, and by what?
- What changed during execution?
- Which parent or dependency relationship explains this item?
- Can a partial mutation be reconciled safely?

Friendly references such as `EPIC:Payment recovery`, `STORY:Customer can recover from failed provisioning`, and `TASK:Normalize provisioning-state persistence` are cached locally for ergonomics, but Asana GIDs remain authoritative. Commands that read or mutate work validate cached references against live Asana state before treating them as current.

Dharana also prefers explicit, previewable mutations. Creation, lifecycle updates, dependency changes, moves, membership changes, and reconciliation paths support dry-runs where a meaningful preview is possible. Ambiguous names, stale references, unsupported workflow shapes, and unsafe repairs return stable error codes instead of guessing.

## Quick Start

From a fresh checkout, configure authentication, inspect capabilities, select or adopt a project, validate readiness, and create your first dry-run epic:

```bash
# Build or run from source.
go run ./cmd/dharana version --json
go run ./cmd/dharana capabilities --json

# Configure your token without putting it in shell history.
read -s ASANA_PAT
go run ./cmd/dharana auth configure --token "$ASANA_PAT" --validate --json
unset ASANA_PAT

# Find a project and adopt it as a named context.
go run ./cmd/dharana project list --json
go run ./cmd/dharana project adopt "$ASANA_PROJECT_GID" --dry-run --json
go run ./cmd/dharana project adopt "$ASANA_PROJECT_GID" --apply --context default --json

# Confirm this repo resolves to the intended project, then validate readiness.
go run ./cmd/dharana context show --json
go run ./cmd/dharana doctor --json

# Try the work graph without mutating Asana.
go run ./cmd/dharana epic create "Payment recovery" --dry-run --json
go run ./cmd/dharana work ready --json
```

For repository-specific work, add a local context file after adoption:

```bash
go run ./cmd/dharana context create default --project "$ASANA_PROJECT_GID" --local --json
```

Project resolution precedence is explicit selector, repository-local context, named user context, then default active project. For a one-command override:

```bash
go run ./cmd/dharana --project "$ASANA_PROJECT_GID" work ready --json
```

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

### Set Up a Project for Dharana

A Dharana-ready Asana project needs three things:

- A selected project context, either user-level or repository-local.
- A compatible work-type mapping for `Epic`, `Story`, `Bug`, and `Spike`.
- Optional field mappings for filters and updates such as Priority and Component.

Dharana can adopt an existing project when its fields or native types already match the expected work model. It can also inspect a blank or partially configured project and return exact remediation steps.

The recommended path is:

1. Configure and validate authentication.
2. Inspect the project.
3. Dry-run adoption.
4. Apply adoption with a named context.
5. Optionally write a repository-local context.
6. Run `doctor`.
7. Refresh friendly references.

```bash
go run ./cmd/dharana project inspect "$ASANA_PROJECT_GID" --json
go run ./cmd/dharana workflow inspect --json
go run ./cmd/dharana project adopt "$ASANA_PROJECT_GID" --dry-run --json
go run ./cmd/dharana project adopt "$ASANA_PROJECT_GID" --apply --context payments --json
go run ./cmd/dharana context create payments --project "$ASANA_PROJECT_GID" --local --json
go run ./cmd/dharana doctor --json
go run ./cmd/dharana refs refresh --json
```

If your project does not yet expose the expected mappings, configure them explicitly:

```bash
go run ./cmd/dharana config set-task-types \
  --field-gid "$ASANA_TASK_TYPE_FIELD_GID" \
  --epic Epic \
  --story Story \
  --bug Bug \
  --spike Spike \
  --json

go run ./cmd/dharana config set-fields \
  --priority-gid "$ASANA_PRIORITY_FIELD_GID" \
  --component-gid "$ASANA_COMPONENT_FIELD_GID" \
  --json
```

Provisioning is conservative by design. Dharana will describe supported mutations in dry-run output and return structured remediation for account or API paths it cannot safely perform automatically:

```bash
go run ./cmd/dharana workflow provision --mode custom-fields --dry-run --json
go run ./cmd/dharana workflow bind --mode native-types --json
```

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

Use `--description-file` to author a formatted task description in Markdown without placing multiline content on the command line:

```bash
go run ./cmd/dharana story create \
  --epic "$ASANA_EPIC_GID" \
  --description-file story.md \
  "Customer can recover from failed provisioning" \
  --dry-run \
  --json

go run ./cmd/dharana work update "STORY:Customer can recover from failed provisioning" \
  --description-file story.md \
  --dry-run \
  --json
```

Markdown descriptions support level-one and level-two headings, paragraphs, emphasis, absolute links, bulleted and numbered lists, blockquotes, inline code, fenced code blocks, and horizontal rules. Dharana renders this deterministic subset to Asana rich text. Raw HTML, images, relative or unsafe links, and using `--notes` with `--description-file` are rejected locally. Existing `--notes` behavior remains available for plain text.

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

### Execute Work

Retrieve one authoritative work item before mutating it:

```bash
go run ./cmd/dharana work get "STORY:Customer can recover from failed provisioning" --json
```

Update only the fields you supply. Dry-run returns the current values and proposed values without mutating Asana:

```bash
go run ./cmd/dharana work update "STORY:Customer can recover from failed provisioning" \
  --assignee developer@example.com \
  --due-on 2026-08-01 \
  --dry-run \
  --json
```

Focused assignment and scheduling commands are available for common lifecycle changes:

```bash
go run ./cmd/dharana work assign "STORY:Customer can recover from failed provisioning" --assignee developer@example.com --json
go run ./cmd/dharana work unassign "STORY:Customer can recover from failed provisioning" --json
go run ./cmd/dharana work schedule "STORY:Customer can recover from failed provisioning" --due-on 2026-08-01 --json
go run ./cmd/dharana work schedule "STORY:Customer can recover from failed provisioning" --clear-due-on --json
```

Inspect dependencies for one item without exporting the full graph:

```bash
go run ./cmd/dharana dependency list "STORY:Customer can recover from failed provisioning" --json
```

Record a concise execution or handoff note:

```bash
go run ./cmd/dharana work comment "STORY:Customer can recover from failed provisioning" \
  --body "Implementation complete; validation passed." \
  --json
```

Complete or reopen supported executable work:

```bash
go run ./cmd/dharana work complete "STORY:Customer can recover from failed provisioning" --json
go run ./cmd/dharana work reopen "STORY:Customer can recover from failed provisioning" --dry-run --json
```

Move supported work under a valid parent:

```bash
go run ./cmd/dharana work move "TASK:Normalize provisioning-state persistence" \
  --parent "BUG:Existing card displays failed-to-provision after refresh" \
  --dry-run \
  --json
```

Reconcile stale local references after partial mutations or external Asana edits:

```bash
go run ./cmd/dharana work reconcile "STORY:Customer can recover from failed provisioning" --dry-run --json
go run ./cmd/dharana context reconcile --dry-run --json
```

### Manage an Epic as Desired State

Dharana accepts versioned YAML or JSON `EpicPlan` manifests. Logical IDs remain stable when names change and are bound locally to authoritative, project-scoped Asana GIDs.

Target a project with either `metadata.context` or `spec.project`; when `spec.project` is used, its Asana project GID overrides the active project. Omitted optional fields remain unmanaged. For managed string fields, use `""` to explicitly clear the remote value; YAML `null` has the same unmanaged meaning as omission.

Epic, work, and task nodes can manage a Markdown description. `description` and legacy plain-text `notes` are mutually exclusive on one node:

```yaml
description:
  format: markdown
  content: |
    ## Acceptance criteria

    - Retry is **idempotent**.
    - Failures include actionable diagnostics.
```

Description diffs compare normalized Asana rich text so provider-added link metadata does not create drift. Export converts the supported rich-text subset back to Markdown and emits a warning when provider formatting cannot be represented losslessly.

Inspect the canonical manifest schema and validate a plan without authentication:

```bash
go run ./cmd/dharana plan schema --json
go run ./cmd/dharana plan validate examples/payment-recovery.epic-plan.yaml --json
```

Add `--remote` to validate the manifest context, project access, users, dates, and configured field values:

```bash
go run ./cmd/dharana plan validate \
  examples/payment-recovery.epic-plan.yaml \
  --remote \
  --json
```

Review and apply the deterministic desired-state diff:

```bash
go run ./cmd/dharana plan diff examples/payment-recovery.epic-plan.yaml --json
go run ./cmd/dharana plan apply examples/payment-recovery.epic-plan.yaml --dry-run --json
go run ./cmd/dharana plan apply examples/payment-recovery.epic-plan.yaml --json
go run ./cmd/dharana plan status examples/payment-recovery.epic-plan.yaml --json
```

Apply is dependency-aware: it creates or binds parents before children, updates supported fields, applies completion state, and adds dependencies only after both endpoints exist. Successful creates are bound immediately, so a retry after `PLAN_PARTIAL_APPLY` does not duplicate completed operations.

Adopt exact-match existing work or export an authoritative epic graph:

```bash
go run ./cmd/dharana plan adopt epic-plan.yaml --dry-run --json
go run ./cmd/dharana plan adopt epic-plan.yaml --apply --json

go run ./cmd/dharana plan export \
  --epic "EPIC:Payment recovery" \
  --output payment-recovery.yaml \
  --json
```

Bindings are stored under `$XDG_CONFIG_HOME/dharana/plans/<project-gid>/` or `~/.config/dharana/plans/<project-gid>/`. Writes are atomic and project-scoped live operations are locked across CLI processes. Bindings can be inspected and changed explicitly without deleting remote work:

```bash
go run ./cmd/dharana plan bindings payment-recovery.yaml --json
go run ./cmd/dharana plan bind payment-recovery.yaml \
  --id persist-state \
  --gid "$ASANA_BUG_GID" \
  --dry-run \
  --json
go run ./cmd/dharana plan unbind payment-recovery.yaml \
  --id persist-state \
  --dry-run \
  --json
```

`removalPolicy: preserve` is the default and never changes previously managed work omitted from a manifest. `removalPolicy: complete` completes omitted executable work but does not delete it. Reconciliation is a dry-run unless `--apply` is explicit:

```bash
go run ./cmd/dharana plan reconcile payment-recovery.yaml --json
go run ./cmd/dharana plan reconcile payment-recovery.yaml --apply --json
```

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
6 partial plan application or failed convergence verification
```

### Dry Runs

Mutation commands that create or change Asana work support `--dry-run`. Dry-run responses include the resolved entities and intended change in the same JSON envelope, but skip the mutating Asana request.
