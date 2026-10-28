# Development and diagnostics

For consumer installation, start with [Setup](setup.md). This guide covers the
manual verification helper, SDK acceptance checks and contributor commands.

- [Manual demo and SDK-only checks](#manual-demo-with-your-existing-vault)
- [Build, test, and package responsibilities](#development)

## Manual demo with your existing vault

From a normal terminal in the Vault Bridge checkout, run:

```sh
make manual-test
# Equivalent: python3 scripts/manual-test.py
```

The script defaults to the sibling `../DevVault` directory and its `vault-bridge`
project. The existing `agent-rig` project supplies a real index note that must be
denied. Both projects must already exist; the script never initializes or edits
vault notes.

It builds the binary, validates a temporary configuration granting only the
selected project, and runs the official SDK acceptance check. You should see
successful context/list/read/search/features/decisions checks and
`permission_denied` for the other project's index. The SDK check also verifies
that the selected project's files remain unchanged.

After that, the script launches an interactive Codex session with a read-only
sandbox and a starting prompt to repeat those operations. The MCP server is
named `vault_bridge_manual`. Use `/mcp` to inspect it, and review the actual tool
calls and source paths in the conversation. A completed SDK preflight or a normal
Codex exit alone does not establish that Codex connected successfully.

Interactive mode needs an installed, authenticated Codex CLI and terminal
stdin/stdout. Run `codex login` first if needed. Review any normal Codex trust or
tool approval prompts. Existing model and authentication settings apply; the
MCP launch settings use session-only
[Codex configuration overrides](https://learn.chatgpt.com/docs/config-file/config-basic).
Existing Codex configuration files are left unchanged, and the temporary vault
configuration is removed when the session ends. Run the script again for a new
session rather than resuming one that refers to an expired temporary file.

For the projects created in the setup guide, use:

```sh
python3 "$BRIDGE_REPO/scripts/manual-test.py" --vault "$VAULT_PATH" \
  --project "$PROJECT_ID" --denied-project isolation-check
```

You can also supply literal paths and IDs, including a vault path with spaces:

```sh
python3 scripts/manual-test.py --vault "/absolute/path/My Vault" \
  --project my-project --denied-project another-project
```

For SDK checks without launching or requiring Codex:

```sh
python3 scripts/manual-test.py --check-only
```

The script locates this repository from its own file location, so it can also be
invoked by absolute path from another working directory. Project IDs must be
distinct lowercase kebab-case names. Each project needs an `index.md`; the active
project also needs `architecture.md` and `roadmap.md` for the acceptance check.
Failures stop the walkthrough and keep the underlying diagnostics visible. If
Codex startup is blocked by an environment permission error, run from your own
terminal; `--check-only` still tests the server independently.


## Development

From the Vault Bridge checkout, run `go mod download` and `make check` to execute
the Go and Python tests, run vet, and build the binary. `make check` needs Python
3.9 or later in addition to Go 1.25+ and `make`.

The Go module path is `github.com/jsainthilaire/vault-bridge`. Internal packages
and embedded templates use that import prefix. The CLI executable remains
`vault-bridge`.

| Package | Responsibility |
| --- | --- |
| `cmd/vault-bridge` | Command dispatch, launch identity and stdio lifecycle |
| `internal/config` | Strict YAML or explicit target selection, limits, and root validation |
| `internal/install` | Client connection installation and preservation of existing settings |
| `internal/scope` | Path authorization and filesystem access checks |
| `internal/vault` | Bounded project reads, revision checks, and opt-in atomic Markdown writes |
| `internal/mcpserver` | Official SDK tool schemas, error mapping and output limits |
| `internal/scaffold`, `templates` | Embedded templates and safe initialization |
| `internal/extensions` | Interface-only boundaries for future adapters |

The runtime uses the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk),
the [YAML organization's maintained v3 parser](https://github.com/yaml/go-yaml),
and [go-toml](https://github.com/pelletier/go-toml) for Codex configuration.
Dependencies are pinned in `go.mod` and `go.sum`. No network calls are part of
vault retrieval. Future external review adapters, lexical indexes, audit sinks and MCP
resources have interface boundaries only; no such adapter is enabled. Opt-in
project writes are implemented in the scoped vault store with revision checks.

```sh
go test ./...
go test -race ./...
go vet ./...
make build
```

An optional SDK acceptance check reads an existing vault through the built binary
and checks that its project files stay unchanged. This check uses the YAML
configuration from [Configuration](reference.md#configuration). Set an ungranted document path
to demonstrate isolation; it may name another project's index:

```sh
cd "$BRIDGE_REPO"
VAULT_BRIDGE_ACCEPTANCE_CONFIG="$BRIDGE_CONFIG" \
VAULT_BRIDGE_ACCEPTANCE_BINARY="$BRIDGE_PATH" \
VAULT_BRIDGE_ACCEPTANCE_PROJECT="$PROJECT_ID" \
VAULT_BRIDGE_ACCEPTANCE_DENIED=03-Projects/isolation-check/index.md \
  go test ./cmd/vault-bridge -run '^TestExistingVaultStdioAcceptance$' -v
```

Tests use disposable vaults for repeated initialization, metadata and links,
two-project isolation, shared grants, symlinks, traversal, hidden content,
pagination, resource bounds, cancellation, and SDK stdio calls. A real Codex
or Claude connection additionally depends on client installation, workspace
trust and local settings; SDK tests do not substitute for that client check.

