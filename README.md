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
