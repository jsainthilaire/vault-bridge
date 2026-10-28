# Vault Bridge

Give your coding agent access to your project's Markdown plans, requirements and
decisions. Keep those notes in an Obsidian vault and your implementation in its
own code repository. Vault Bridge connects them through a local MCP server.

Each server process serves one selected project. Reads are enabled by default;
project Markdown writes require `--allow-writes`. Obsidian is optional, and the
bridge needs no plugin, database, network listener or separate API key.

## Start here

| You want to… | Read |
| --- | --- |
| Set up a vault or connect your first project | [Setup](docs/setup.md) |
| Understand the connection and use it day to day | [How it works](docs/how-it-works.md) |
| Check commands, configuration, tools or security limits | [Reference](docs/reference.md) |
| Fix a connection, path or write error | [Troubleshooting](docs/troubleshooting.md) |
| Run tests or the SDK verification helper | [Development and diagnostics](docs/development.md) |

## Quick start

To create a vault for a new project in an empty or missing folder, build the CLI
with `make build`, then run:

```sh
./.bin/vault-bridge vault init --path /absolute/path/to/MyVault \
  --id my-project --title "My Project"
```

This adds the project's planning notes under `03-Projects/my-project`, a dashboard,
and reusable templates. Template sources are kept in [`templates/`](templates/README.md).
Follow the [setup guide](docs/setup.md#step-by-step-setup) to connect your agent.

Use this path when your project's notes already exist under
`<vault>/03-Projects/my-project`. For a new vault or project, follow the
[full setup guide](docs/setup.md#step-by-step-setup), which includes initialization.

You need Go 1.25+, `make`, this source checkout, and an installed, signed-in Codex
or Claude Code client. The documented setup and project writes target macOS and
Linux. Python is only needed for contributor tests and the optional SDK helper.

**1. Build and register your project.** Run from the Vault Bridge checkout and
replace the example paths with your own:

```sh
cd /absolute/path/to/vault-bridge
make build

BRIDGE_PATH="$PWD/.bin/vault-bridge"
VAULT_PATH="/absolute/path/to/DevVault"
AGENT_REPO="/absolute/path/to/my-project"
PROJECT_ID="my-project"

"$BRIDGE_PATH" config check --target "$VAULT_PATH/03-Projects/$PROJECT_ID"
"$BRIDGE_PATH" install "$AGENT_REPO" --client codex \
  --target "$VAULT_PATH/03-Projects/$PROJECT_ID"
```

Use `--client claude` for Claude Code. Your code repository must already exist.
The target is the project's docs folder, directly inside `03-Projects`.
Validation should report your project ID and `0 shared grants`.

**2. Tell the agent when to consult the vault.** Merge the
[project knowledge workflow](docs/setup.md#agent-knowledge-workflow) into your
code repository's `AGENTS.md` for Codex or `CLAUDE.md` for Claude Code. Set its
project ID to `my-project`, or the ID you chose. Preserve existing instructions.

**3. Start a new client session from your code repository.**

```sh
cd "$AGENT_REPO"
codex
# Or: claude
```

Complete normal workspace trust and MCP approval steps. The client starts the
bridge automatically. In the conversation, inspect `/mcp` and ask:

```text
Use only the devvault MCP tools. Call project_context and confirm the active
project is my-project, then read 03-Projects/my-project/roadmap.md.
Report the source path and actual result. Do not edit files for this check.
```

Use your chosen ID in that prompt. Successful tool results establish that the
connection works. Follow the [full retrieval and isolation check](docs/setup.md#6-verify-retrieval-and-project-isolation)
for a more complete verification.

## What gets connected

| Location | What it holds |
| --- | --- |
| Vault Bridge checkout or installed executable | The Go program your client launches |
| Your vault, such as `DevVault/03-Projects/my-project` | The project's Markdown knowledge |
| Your project's code repository | Implementation, MCP connection settings and agent instructions |

The installer registers a server named `devvault` in your code repository:
`.codex/config.toml` for Codex or `.mcp.json` for Claude Code. It records absolute
paths to the binary and selected notes. Keep that binary available at the
registered path. You can also [install the executable](docs/setup.md#2-build-the-bridge-and-choose-your-paths)
in a stable location.

When the agent calls a tool, the client sends the request to its bridge process.
The bridge checks the project's scope and returns bounded Markdown or search
results from the vault. Edits in Obsidian are available on subsequent requests;
there is no synchronization step. The client controls which returned content is
sent to its model provider.

The [how-it-works guide](docs/how-it-works.md) explains project isolation, shared
knowledge, tool paths, daily prompts and write behavior.

## Let the agent update project notes

Add `--allow-writes` to a **new** connection's install command to enable
`document_create` and `document_update`. For an existing connection, add the flag
to the server's saved arguments and restart the client; rerunning the installer
with different arguments reports a conflict rather than replacing settings.

Ask the agent to update a milestone only after checking implementation and test
evidence. It must read the complete note and its revision, preserve unrelated
content, and reread/merge if another update changed the revision. Shared
knowledge remains read-only. See [write setup and conflict handling](docs/setup.md#enable-project-writes-and-update-milestones).

## Connect more projects

Run the installer for each code repository with that project's docs target.
Each client process keeps its own selected scope while using the same binary.
Use [YAML configuration](docs/reference.md#configuration) for custom limits or
explicit read access to shared knowledge subtrees.

Use [global installation](docs/setup.md#global-installation) when you want the
**same selected project** available across repositories. Global registration
keeps that identity fixed; it does not choose a project from the working directory.

Vault Bridge's scope applies to its own MCP tools. Your agent's separate shell
or filesystem access follows its client and OS permissions. Read the
[security boundaries](docs/reference.md#security-boundaries) when choosing grants.
