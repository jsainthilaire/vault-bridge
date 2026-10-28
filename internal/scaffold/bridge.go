package scaffold

// These are project-specific seed data, rendered by the same generic note
// template and exclusive creation path as ordinary project planning notes.
const bridgeOverview = `Vault Bridge connects a Markdown-first Obsidian vault to project-scoped coding
agents through a local Go MCP server with read-only access by default. The vault holds plans and
decisions; a separate Go repository holds source code, tests and client setup.

## Goals

- Retrieve source-attributed context for exactly one explicitly configured project.
- Keep shared knowledge opt-in at explicit subtree boundaries and read-only.
- Enable project note and milestone updates only with explicit write authorization.
- Preserve authored notes and remain independent of Obsidian plugins.

## Features

- [[03-Projects/vault-bridge/features/vault-scaffolding|Vault scaffolding]]
- [[03-Projects/vault-bridge/features/scoped-file-access|Scoped file access]]
- [[03-Projects/vault-bridge/features/mcp-read-tools|MCP read tools]]
- [[03-Projects/vault-bridge/features/agent-integration|Agent integration]]
- [[03-Projects/vault-bridge/features/project-writes|Project Markdown writes]]

## Decisions and research

- [[03-Projects/vault-bridge/decisions/ADR-001-local-stdio-mcp|Local stdio MCP]]
- [[03-Projects/vault-bridge/decisions/ADR-002-read-only-mvp|Read-only MVP]]
- [[03-Projects/vault-bridge/decisions/ADR-003-explicit-project-scoping|Explicit project scoping]]
- [[03-Projects/vault-bridge/decisions/ADR-004-opt-in-project-writes|Opt-in project writes]]
- [[03-Projects/vault-bridge/research/sdk-and-client-integration|SDK and client integration]]`

var bridgePlans = []planNote{
	{"vision", "vision", "Vision", `## Problem

Project knowledge needs a durable, human-editable home without copying notes into
every code repository or granting one MCP process unrestricted vault access.

## Goals and principles

Use ordinary Markdown and YAML frontmatter as the source of truth. Keep code in a
separate repository. Bind project identity at trusted server launch, deny access
by default, and treat tool arguments and note contents as untrusted data.

## Non-goals

Read-only access is the default. Explicitly authorized project Markdown writes
allow note and milestone updates. No document deletion, automatic synchronization,
network service, automatic project discovery, embeddings, or database is provided.`},
	{"requirements", "requirements", "Requirements", `## CLI

Provide non-destructive vault and project initialization, configuration checking,
and a stdio server that defaults to read-only access. Initialization preserves authored notes and rejects
symlink writes. Serving reserves stdout for protocol messages.

## Configuration and tools

Configure an absolute vault root and explicit project map. Read identity only from
the trusted launch argument. Shared subtree grants default to empty. Expose
project_context, document_list, document_read, document_search, decision_list and
feature_list, without a project selector in tool arguments. With --allow-writes,
also expose document_create and revision-checked document_update inside the active
project only; shared grants remain read-only.

## Security and acceptance

Reject absolute tool paths, traversal, symlinks and unauthorized roots. Serve only
regular Markdown notes. Exclude hidden/configuration files and attachments. Bound
document sizes, result counts, output and scan work. Verify two-project isolation,
malformed metadata, cancellation, non-clobbering init and SDK stdio behavior.
Record actual test results in session notes; generated checklists do not imply
that checks have run.`},
	{"architecture", "architecture", "Architecture", `## Request path

Project-local client configuration launches one Go process with an explicit
project ID. Official Go MCP SDK handlers call a scoped Markdown store, which
authorizes only the configured project and explicitly granted shared subtrees.

## Responsibilities

Keep CLI dispatch, YAML configuration, scope validation, bounded Markdown access,
MCP handlers and initialization in separate packages. Embed reusable templates in
the binary. Use filesystem-aware containment checks and reject symlink components.

## Boundaries

No HTTP transport, shell execution, database, document deletion, or shared-note
mutations are exposed. Project Markdown writes require --allow-writes, content
revision checks, and atomic publication. Notes are reference data rather than privileged instructions. MCP
restrictions do not sandbox independently available agent filesystem/shell tools.
Concurrent local filesystem mutation requires additional consideration; use
trusted directories and appropriate OS permissions.`},
	{"roadmap", "roadmap", "Roadmap", `## Milestones

- [ ] Scaffold and docs: CLI, configuration, embedded templates, safe initialization.
- [ ] Secure read layer: scope validation, Markdown metadata, bounds and isolation tests.
- [ ] MCP tools: six read tools and opt-in project create/update tools with SDK tests.
- [ ] Codex experience: trusted project-local setup, real note retrieval and denial checks.
- [ ] Extensibility preparation: interface boundaries and separate Claude Code setup.

## Evidence

This is a newly generated planning checklist. Reconcile it with actual code and
record completed work and commands in session notes before marking items complete.`},
	{"tasks", "tasks", "Tasks", `## Implementation and verification

- [ ] Verify and pin the official Go MCP SDK and maintained YAML parser.
- [ ] Check repeat initialization preserves authored notes, templates and dashboard links.
- [ ] Validate explicit scope configuration and default-empty shared grants.
- [ ] Exercise every read tool against two-project isolation fixtures.
- [ ] Test traversal, lookalike prefixes, hidden files and symlink components.
- [ ] Test malformed frontmatter, pagination, query bounds, resource limits and cancellation.
- [ ] Verify opt-in writes, stale revisions, competing writers and project-only mutation boundaries.
- [ ] Run Go test, vet and build; record the commands and results.
- [ ] Validate an SDK stdio connection and a real project-local Codex connection.
- [ ] Retrieve this project's context and deny another project's notes.
- [ ] Document setup, independent-agent-tool limitations and concurrent-mutation risks.

Generated tasks are proposed work, not claims of completed implementation or tests.`},
}

var bridgeNotes = []planNote{
	{"features/vault-scaffolding", "feature", "Vault scaffolding", `## Problem and requirements

Create a usable numbered vault and consistent project planning notes while
preserving authored material. Keep reusable templates and a Markdown dashboard usable
without community plugins. Create Vault Bridge through the normal initializer.

## Acceptance criteria

- [ ] Repeat initialization preserves modified notes and dashboard content.
- [ ] New files have valid metadata, dates, links and safe permissions.
- [ ] Unexpected symlinks and special files are rejected.

## Rollout and testing

Use temporary fixtures before initializing an existing vault.`},
	{"features/scoped-file-access", "feature", "Scoped file access", `## Problem and requirements

An agent must retrieve only the project selected at trusted process launch, plus
explicitly granted shared subtrees. Reject traversal, absolute paths, symlinks,
hidden paths, attachments and non-Markdown files. Apply byte, result and scan
limits, deterministic ordering and cancellation.

## Acceptance criteria

- [ ] Every read/list/search route enforces two-project isolation.
- [ ] Lookalike prefixes and symlink parents cannot expand authorization.
- [ ] Shared grants permit only their exact configured subtrees.

## Rollout and testing

Use adversarial temporary fixtures and document filesystem mutation limitations.`},
	{"features/mcp-read-tools", "feature", "MCP read tools", `## Problem and requirements

Expose compact source-attributed project context, document list/read/search,
decisions and features through the official Go MCP SDK over local stdio. Tool
schemas omit project switching; application errors have stable codes. Keep stdout
protocol-only and diagnostics bounded without note contents.

## Acceptance criteria

- [ ] All six tools return bounded, vault-relative source metadata.
- [ ] SDK client/server stdio initialization and calls succeed.
- [ ] Invalid arguments, malformed notes and cancellation fail predictably.

## Rollout and testing

Verify protocol output and cross-project denial through the SDK client.`},
	{"features/agent-integration", "feature", "Agent integration", `## Problem and requirements

Give each repository a trusted project-local MCP configuration with the matching
explicit project ID. Use Codex first and document Claude Code separately with the
same Go binary. Keep developer-specific absolute paths in local configuration,
and keep AGENTS.md and CLAUDE.md instructions separate.

## Acceptance criteria

- [ ] Verify current official client syntax and trust requirements.
- [ ] Retrieve this project's index, architecture, roadmap, features and decisions.
- [ ] Deny access to another project's notes through this server.

## Rollout and testing

Do not infer a successful client connection from configuration-file creation.`},
	{"decisions/ADR-001-local-stdio-mcp", "decision", "ADR-001: Local stdio MCP", `## Context

Humans edit local Markdown notes, and coding agents need scoped reference access
without operating a hosted service.

## Decision

Use the official Go MCP SDK with one local stdio process per configured project.

## Alternatives

A remote HTTP service introduces authentication and hosting requirements beyond
the MVP. Copy-based synchronization duplicates the source of truth.

## Consequences

No network listener or cloud credentials are needed. Stdout must contain only
protocol messages, and clients need explicit launch configuration.`},
	{"decisions/ADR-002-read-only-mvp", "decision", "ADR-002: Read-only MVP", `## Context

Notes contain durable plans and decisions that must remain under human control.

## Decision

The original MVP exposed read-only retrieval tools and no mutations. This remains
the default behavior. ADR-004 extends this decision with explicitly authorized
project Markdown creation and revision-checked updates; deletion is not exposed.

## Alternatives

Write tools require explicit launch authorization, an active-project allowlist,
optimistic concurrency, and bounded audit metadata, as described in ADR-004.

## Consequences

Obsidian remains an editing interface. Read-only sessions preserve the original
MVP behavior. Initialization adds missing scaffold files through a separate
explicit CLI command and preserves authored notes.`},
	{"decisions/ADR-003-explicit-project-scoping", "decision", "ADR-003: Explicit project scoping", `## Context

One vault can hold private notes for unrelated projects. An agent must not select
its own broader authorization through tool input or inferred repository names.

## Decision

Bind identity through the trusted --project launch argument and explicit YAML
project map. Default shared grants to empty and validate exact subtree boundaries.

## Alternatives

Global vault access or project arguments in individual tools allow unintended
cross-project discovery. Automatic repository inference is ambiguous.

## Consequences

Use one scoped process per repository. MCP restrictions apply only through this
server and do not sandbox independently available agent tools.`},
	{"features/project-writes", "feature", "Project Markdown writes", `## Problem and requirements

Agents need to add project notes and update or add milestones in ordinary roadmap
Markdown. Keep six read tools by default; --allow-writes enables document_create
and document_update only inside the explicitly selected project. Shared knowledge
is always read-only. Do not expose deletion or directory creation.

## Acceptance criteria

- [ ] Create notes without replacing existing files; preserve permissions on updates.
- [ ] Return full-content revisions and reject stale updates with conflict.
- [ ] Preserve authored roadmap content while adding or updating milestone checkboxes.
- [ ] Reject sibling/shared/hidden/non-Markdown/symlink writes and malformed or oversized content.
- [ ] Coordinate cooperating writers and respect canceled lock waits.
- [ ] Verify SDK tool schemas, mutation annotations and redacted audit records.

## Testing

Use temporary vaults only. Reconcile these proposed checks with actual test results.`},
	{"decisions/ADR-004-opt-in-project-writes", "decision", "ADR-004: Opt-in project writes", `## Context

Agents need to maintain project notes and roadmap milestones without gaining
write access to unrelated projects or shared knowledge.

## Decision

Keep serving read-only by default. A trusted --allow-writes launch flag enables
Markdown create and revision-checked update operations inside the active project.
Read responses include the complete source SHA-256 revision; replacements require
that expected revision. Creates are exclusive and replacements are atomic.
Shared grants stay read-only and parent directories must already exist.

## Concurrency and audit

On macOS and Linux, a retained hidden project lock coordinates bridge writers.
Check revisions again immediately before publishing. External editors do not
honor that lock and can race the final comparison; avoid simultaneous external
edits to the same note. Emit best-effort successful-write audit metadata to
stderr without note contents.

## Consequences

This extends ADR-002 for opt-in project writes while preserving its read-only
default. Client approval policies remain in effect. No delete operation, shared
mutation, automatic synchronization, or structured milestone database is added.`},
	{"research/sdk-and-client-integration", "research", "SDK and client integration", `## Question

Which official SDK APIs, YAML parser versions and project-local client settings
are verified for this installation?

## Findings

This generated note records the research checklist, not a completed validation.
Inspect the implementation repository for pinned versions and actual test results.

## References

- [Official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)
- [YAML project-maintained Go parser](https://github.com/yaml/go-yaml)
- [Codex MCP documentation](https://developers.openai.com/codex/mcp)
- [Claude Code MCP documentation](https://code.claude.com/docs/en/mcp)

## Next checks

- [ ] Confirm stdio server/client APIs and generated tool schemas.
- [ ] Confirm Codex project-level configuration and trust behavior.
- [ ] Confirm separate Claude Code project-local configuration and approval.
- [ ] Record real note retrieval and cross-project denial results.`},
}

func seedBridge(w writer, date string) error {
	for _, note := range bridgeNotes {
		v := values{ID: "vault-bridge", Type: note.kind, Title: note.title, Body: note.body, Date: date}
		if err := w.render("03-Projects/vault-bridge/"+note.name+".md", "note.md.tmpl", v); err != nil {
			return err
		}
	}
	return nil
}
