# Reference

- [Commands](#command-reference)
- [Flags and defaults](#flags-and-defaults)
- [Valid connection combinations](#valid-connection-combinations)
- [Vault and project layout](#vault-layout)
- [YAML configuration and shared grants](#configuration)
- [MCP tool arguments, paths, and errors](#mcp-tools)
- [Security boundaries](#security-boundaries)

For first-time installation, follow [Setup](setup.md). For the request flow and
daily examples, see [How it works](how-it-works.md).

## Command reference

These examples use `vault-bridge` for readability. From this checkout, run
`./.bin/vault-bridge` after `make build`. Alternatively, `make install` copies the
executable to `$HOME/.local/bin`; add that directory to your shell's `PATH`.
Use `make install PREFIX=/your/prefix` to choose another installation prefix.
Run the installed executable's `install` command if connections should use that
path instead of the checkout's local binary.

Prefix each command below with `vault-bridge`. Initialization and installation
are one-time setup operations that you can rerun; the client invokes `serve`
when opening its MCP connection.

| Command | Purpose and effect | Starts an MCP server? | Example arguments |
| --- | --- | --- | --- |
| `vault init` | Adds vault structure, templates, dashboard and a selected project, preserving existing notes. | No | `--path ../DevVault --id my-app --title "My App"` |
| `project init` | Adds planning documents and links for one project in an existing vault. | No | `--vault ../DevVault --id my-app --title "My App"` |
| `config check` | Validates one project's folder, scope and limits; leaves files unchanged. | No | `--target ../DevVault/03-Projects/my-app` |
| `install` | Saves one `devvault` connection in a code repository's client settings. | No | `/path/to/code --client codex --target ../DevVault/03-Projects/my-app` |
| `install --global` | Saves one `devvault` connection in user settings, available across repositories. | No | `--client codex --target ../DevVault/03-Projects/my-app` |
| `install --global` with `--projects-dir` | Saves one `devvault-<id>` connection per immediate project folder in one atomic settings update. | No | `--client codex --projects-dir ../DevVault/03-Projects` |
| `serve` | Runs the selected project's MCP tools over stdin/stdout until the connection ends. The client normally starts it. | Yes | `--target ../DevVault/03-Projects/my-app --transport stdio` |
| `--help` or `-h` | Prints top-level help; use `<command> --help` for that command's flags. | No | `install --help` |
| `--version` | Prints the bridge version and exits. | No | No additional arguments |

`make build` compiles the executable. `make install` copies it to your installation
prefix. Neither registers an MCP connection; that is the role of the executable's
`install` command. See [automatic startup](how-it-works.md#automatic-startup-and-tool-discovery)
for what happens after registration.

## Flags and defaults

Flags apply only to the commands listed. Values such as `<id>` and `<path>` are
placeholders; replace them with your own values and quote paths containing spaces.

| Flag or argument | Commands | Default / requirement | Meaning and constraints |
| --- | --- | --- | --- |
| `--path <vault>` | `vault init` | Required | Vault directory to create or extend. |
| `--vault <vault>` | `project init` | Required | Existing vault to extend with one project. |
| `--id <id>` | `vault init`, `project init` | Required for `project init`; paired with `--title` for `vault init` | Project ID for generated notes. Use lowercase letters/digits separated by single hyphens, at most 64 bytes; reserved directory names are excluded. |
| `--title <text>` | `vault init`, `project init` | Required for `project init`; paired with `--id` for `vault init` | Human-readable project title. Omit both project flags on `vault init` to seed the bridge's own planning project. |
| `--target <project-folder>` | `config check`, `install`, `serve` | Alternative to `--config` | One existing project directory directly inside `<vault>/03-Projects`. Defaults to no shared grants and standard resource limits. |
| `--config <absolute-yaml>` | `config check`, `install`, `serve` | Alternative to `--target`; requires `--project` | YAML project mapping with optional shared grants and resource limits. Both the config path and its `vault_root` must be absolute. |
| `--project <id>` | `config check`, `install`, `serve` | Required with `--config`; defaults to target folder name with `--target` | Fixed project identity for this launch. With `--target`, an explicit valid ID can alias a differently named folder. |
| `--client codex` or `--client claude` | `install` | Required | Which client's settings to update. Run installation once for each client if you use both. |
| `--repo <code-directory>` | `install` | Current directory (`.`) | Existing code repository receiving project-local settings; not the vault. Cannot be used with `--global`. |
| `<code-directory>` positional argument | `install` | Alternative to `--repo` | Shorthand for the repository path. Put the flags before or after this path; do not also pass `--repo`. |
| `--global` | `install` | `false` | Write user-level client settings. Cannot be combined with any repository argument. Required for bulk setup. |
| `--projects-dir <directory>` | `install` | Unset; requires `--global` | Register all immediate project folders under `<vault>/03-Projects`. Cannot be combined with `--target`, `--config` or `--project`. |
| `--allow-writes` | `install`, `serve` | `false` | Enable project Markdown creation and revision-checked updates. On `install`, persist this flag in the saved `serve` arguments; on bulk install, apply it to every project. Shared grants stay read-only. |
| `--transport stdio` | `serve` | `stdio` | Only supported transport. Installation saves it automatically; `install` itself does not accept this flag. |
| `--help` or `-h` | Every command | Unset | Print help and exit. For example, `project init --help`. |
| `--version` | Top level only | Unset | Print the version; use it alone. |

Relative vault, repository and target paths resolve from the invoking process's
working directory. Installation saves absolute executable and target paths, so
later client launches do not depend on where you ran the installer. Vault Bridge
does not expand `~` or environment references in stored paths. Your shell can
expand variables in commands such as `--target "$VAULT_PATH/03-Projects/my-app"`.

## Valid connection combinations

For `install`, `serve`, and `config check`, choose one connection form:

| Options | When to use them |
| --- | --- |
| `--target <project-docs-folder>` | Connect directly to one existing folder under `03-Projects`. The folder name supplies the project ID; default limits apply and shared access is empty. |
| `--config <absolute-yaml-path> --project <id>` | Use an explicit YAML mapping with custom limits or optional shared knowledge grants. See [Configuration](#configuration). |

`--target` and `--config` are mutually exclusive. With `--target`, use
`--project <id>` if the folder name is not a lowercase kebab-case ID or you need
an explicit alias. Read tool paths remain vault-relative, such as
`03-Projects/my-app/architecture.md`.

For bulk installation only, use `--global --projects-dir <vault/03-Projects>`
instead of either connection form. It cannot be combined with `--target`,
`--config`, `--project`, `--repo`, or a positional repository. `--allow-writes`
applies to each registered project. No prior global installation is required.
Discovery is limited to 1024 total entries; immediate non-hidden directories
must have valid project IDs. Regular files and hidden entries are ignored;
non-hidden symlinks and special files are rejected. Empty project sets fail.
All registered project tools are globally available, each with a separate fixed
scope and no shared grants. See [bulk setup](setup.md#install-every-project-globally).

| Task | Required selection | Optional additions |
| --- | --- | --- |
| Validate one direct target | `config check --target <project-folder>` | `--project <alias>` |
| Validate a YAML mapping | `config check --config <absolute-yaml> --project <id>` | None |
| Install one project locally | `install --client <client>` with either connection form above | Repository path or `--repo`; `--allow-writes` |
| Install one project globally | `install --global --client <client>` with either connection form above | `--allow-writes` |
| Install every project globally | `install --global --client <client> --projects-dir <projects-folder>` | `--allow-writes` |
| Serve one project | `serve` with either single-project connection form above | `--transport stdio`; `--allow-writes` |

There is no bulk `serve` command. Bulk installation saves separate single-project
launch commands. `config check` validates access configuration; it does not test
a live client connection or enable write tools. Use [client verification](setup.md#6-verify-retrieval-and-project-isolation)
to check actual retrieval.


## Vault layout

Initialization adds missing files and appends missing project links to
`00-Dashboard/Home.md` without replacing authored content. No unrelated sample
projects are created. New directories use mode `0700` and notes `0600`; existing
permissions are preserved. Initialization rejects unexpected symlinks. Obsidian
can create its own `.obsidian` settings; Vault Bridge does not manage them.

The numbered sections include Inbox, Ideas, Projects, Architecture, Knowledge,
Agent Workflows, Research, Templates and Archive, plus `assets/`. Knowledge has
Go, AI-Agents, Security and Infrastructure subfolders. Eleven reusable templates
cover Project, Vision, Requirements, Architecture, Roadmap, Tasks, Feature, ADR,
Research, Session and Idea notes. Their [embedded sources](../templates/README.md)
live in `templates/` in this repository.

`vault init --path <folder> --id <id> --title <title>` accepts an empty or missing
folder and initializes the chosen project in one command. Both project flags
must be supplied together. It creates no additional Vault Bridge project when
you choose another ID. The original `vault init --path <folder>` command still
seeds Vault Bridge. Both forms can extend an existing vault without overwriting
notes.

Every project gets `index.md`, `vision.md`, `requirements.md`, `architecture.md`,
`roadmap.md`, `tasks.md`, and `features/`, `decisions/`, `research/`, `sessions/`.
Project IDs use lowercase letters/digits separated by single hyphens, up to
64 bytes. Excluded directory names are reserved IDs: `assets`, `attachment`,
`attachments`, `secret`, `secrets`, `vendor`, `credentials` and `private-keys`.
Vault Bridge additionally gets its initial feature, ADR and integration research
notes. Generated planning checklists do not claim implementation or test results.


## Configuration

YAML is optional. Use it for custom resource limits or explicit shared knowledge
grants; `--target` uses the defaults and grants only the selected project.
These commands use the variables from [setup step 2](setup.md#2-build-the-bridge-and-choose-your-paths).

```sh
BRIDGE_CONFIG="$BRIDGE_REPO/vault-bridge.local.yaml"
if [ ! -e "$BRIDGE_CONFIG" ]; then
  cp "$BRIDGE_REPO/examples/config.yaml" "$BRIDGE_CONFIG"
fi
```

Edit that local file to match your vault and project. The example file uses
`my-project`; change the mapping if you chose another ID:

```yaml
version: 1
vault_root: /absolute/path/to/DevVault
projects:
  my-project:
    folder: 03-Projects/my-project
    shared_read_paths: []
limits:
  max_document_bytes: 262144
  max_search_results: 25
  max_search_scan_files: 5000
```

Replace `vault_root` with your absolute vault path. If the file already contains
projects, add the mapping beneath the existing `projects` key. Keep this
machine-specific file out of commits. Validate and install it:

```sh
"$BRIDGE_PATH" config check --config "$BRIDGE_CONFIG" --project "$PROJECT_ID"
"$BRIDGE_PATH" install "$AGENT_REPO" --client codex \
  --config "$BRIDGE_CONFIG" --project "$PROJECT_ID"
```

Use `--client claude` for Claude. If another `devvault` connection is already
installed, follow [Existing connections](setup.md#existing-connections) before switching
forms. Launch and verify the client as in setup steps 5–6.

Configuration paths and `vault_root` must be absolute. The program does not expand
`~`, `$HOME`, or other variables inside files; shell variables in the commands
above are expanded by your terminal. Project folders and shared paths are
vault-relative. All mapped directories must exist and be valid, including
mappings other than the selected one. Project folder mappings must not overlap.
Unknown YAML fields, duplicate keys, unsupported versions, and invalid limits
fail validation.

Omitted limits use the defaults shown above. Explicit limits must be positive
and cannot exceed 1,048,576 document bytes, 100 results, or 50,000 scanned entries.
The scan budget counts every encountered entry, including directories and skipped
non-Markdown files. Frontmatter has a 32 KiB bound; paths and queries have a
1,024-byte bound.

Projects map explicitly beneath `03-Projects/`. Shared grants default to empty;
to permit Go references, add `shared_read_paths: [05-Knowledge/Go]`. A grant permits
only that subtree and its descendants. Broad vault-wide grants are rejected.
Hidden files, secrets, attachments, and non-Markdown files remain excluded even
inside a grant.

The trusted launch path and project ID bind the process. Tool arguments, note
metadata, client roots, and Git remotes cannot change that scope. With YAML,
`--project` is required. With `--target`, the folder name supplies the ID unless
an explicit `--project` is given. The installer saves the resolved target and ID
in the client connection so later launches do not depend on its working directory.


## MCP tools

| Tool | Arguments | Result |
| --- | --- | --- |
| `project_context` | none | Active project title, summary, status, source paths, links, index excerpt |
| `document_list` | optional `section`, `limit`, `cursor` | Ordered document metadata and optional `next_cursor` |
| `document_read` | `path`, optional `max_chars` | Markdown, frontmatter, source metadata, full-source `revision`, `truncated` |
| `document_search` | `query`, optional `section`, `limit` | Ordered matching paths, titles and bounded snippets |
| `decision_list` | optional `limit` | ADR metadata from the active project's `decisions/` |
| `feature_list` | optional `status`, `limit` | Feature metadata with optional frontmatter status filter |
| `document_create` (opt-in) | `path`, `markdown` | Creates a new project note; returns path and revision |
| `document_update` (opt-in) | `path`, `markdown`, `expected_revision` | Replaces an existing project note if its revision matches; returns path and new revision |

Read paths are always vault-relative, for example
`03-Projects/my-project/architecture.md`. There is no project selector or
project enumeration tool. Omit `section` or use `section: "project"` to select
the active project; `section: "features"` or `section: "decisions"` selects a
project-relative subtree. To list/search
shared knowledge, use `section: "shared:05-Knowledge/Go"` matching an explicit
grant. Default search covers only the active project.

Search is case-insensitive: it accepts a full substring or a match containing
every query token. Results use deterministic ordering. Use `next_cursor` with
the same section to continue `document_list`; cursors are opaque and scoped.
Pagination is a live view rather than a filesystem snapshot.
Cursors are signed per process and expire when the server restarts. A `limit`
or `max_chars` value of zero selects its default, as does omitting it.

Documents may have YAML frontmatter bounded by `---` lines. Missing frontmatter
is allowed; malformed frontmatter returns a deterministic error, including when
encountered during listing/search. The error does not expose the malformed text.
Document size is checked before parsing; oversized files fail rather than being
silently read. `max_chars` truncates returned Markdown by Unicode characters and
sets `truncated`; it does not bypass the file byte limit.

Limits bound documents, scan entries, results and JSON output. Tool payloads have
a 1 MiB cap before the SDK adds its text fallback; stdio input frames have a
64 KiB cap for read-only sessions or an 8 MiB cap when writes are enabled to allow
a maximum-size document escaped as JSON. The store still enforces its document
byte limit. Reduce `limit` or `max_chars` if a request exceeds output bounds.
Request cancellation is honored during store operations.

Tool failures set MCP `isError: true` and carry JSON text shaped as
`{"error":{"code":"permission_denied","message":"..."}}`. Codes are
`invalid_argument`, `not_found`, `permission_denied`, `resource_limit`, `conflict`,
and `internal_error`. `conflict` means a create destination already exists or an
update revision is stale. Cancellations use `resource_limit`. Unknown methods/tools use
protocol errors. Schema validation errors use `invalid_argument` without
echoing the offending argument. Nothing in a note can execute code or change a
grant.


## Security boundaries

Every read validates canonical containment using filesystem-relative paths and
rejects absolute paths, traversal, encoded paths, NULs and symlink components.
Only regular `.md` files in the active scope can be served. Anchored `os.Root`
handles and access-time checks reduce path substitution risks. The vault must
still be controlled by a trusted local user: concurrent in-scope edits, renamed
directories, hard links and hostile filesystem mutation are not a content
snapshot or a complete OS sandbox. Do not place hard links to private content
inside an authorized scope. Keep credentials out of Markdown grants.

The server's restrictions apply only to its MCP tools. They do not restrict an
agent's independently available shell or filesystem tools. Use read-only OS
permissions and client sandbox boundaries when stronger isolation is needed.
Markdown is untrusted data, including frontmatter and embedded instructions.

Initialization is an explicit filesystem-writing CLI operation, separate from
MCP. It adds missing notes and dashboard links while preserving authored notes.
Review setup paths before invoking it.

The MCP server is read-only by default. With `--allow-writes`, it can create and
replace the active project's visible Markdown notes using revision checks.
Shared grants and other projects remain outside its write scope.

