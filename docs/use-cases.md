# Use cases and prompts

These prompts go in your coding agent's conversation after [setup](../README.md#quick-start).
They use `devvault`, the name of a single-project connection. For bulk setup,
replace it with the intended server, such as `devvault-my-app`.

Vault Bridge supplies the notes. Your coding agent reads code, runs checks and
decides what the evidence supports. The bridge does not infer requirements or
mark work complete on its own.

## Resume a project

Keep the project's purpose and links in `index.md`, and current milestones in
`roadmap.md`. At the start of a session, ask:

```text
Use devvault to read the project overview and roadmap. Summarize the goal,
the last recorded progress, and the next unfinished milestone. Cite source
paths and identify anything the notes leave unclear. Do not edit files yet.
```

Expect a summary grounded in saved notes. Treat recorded progress as something
to check against the repository before starting implementation.

## Build a feature from its requirements

Write the intended behavior and acceptance criteria in `requirements.md` or a
note under `features/`. Replace the example feature below with yours:

```text
Use devvault to find and read the password-reset requirements, relevant
architecture notes, and authentication decisions. Compare them with the
current code. Propose an implementation plan and tests, citing the notes.
Call out missing requirements or conflicts before changing files.
```

The useful output is a plan tied to both the written requirements and the
current implementation. Empty templates cannot supply missing product decisions.

## Understand why a decision was made

Store architectural decision records under `decisions/`, including the choice,
its reasons and alternatives considered.

```text
Use devvault to search the decision records for authentication and sessions.
Read the matching notes. Explain the chosen approach, its reasons and the
constraints a proposed change must respect. Cite the source paths and say
when the notes do not answer a question.
```

Search matches text; use terms that appear in the notes. An empty search result
means no matching note was found, not that the project has no relevant decision.

## Check what is actually finished

Use the roadmap as the intended outcome and the repository as implementation
evidence. Read-only access is sufficient for this review:

```text
Use devvault to read the roadmap and relevant feature acceptance criteria.
Compare them with the code and tests. List what is implemented, what was
verified, and what remains open. Cite note paths and code evidence, distinguish
checks you ran from recorded results, and leave the notes unchanged.
```

After implementation and verification, a connection installed with
`--allow-writes` can record the outcome:

```text
Use devvault to update the relevant roadmap and feature notes with the work
completed and checks actually performed in this session. Read each complete
note and its revision first, preserve unrelated content, and reread and merge
if an update conflicts. Keep unverified acceptance criteria open. Report the
updated paths, or explain any failed update.
```

The [generated write-enabled workflow](setup.md#agent-knowledge-workflow) also
asks the agent to maintain relevant notes after substantive work. This explicit
prompt is useful when you want to reconcile existing notes. See
[enabling writes on an existing connection](setup.md#enable-project-writes-and-update-milestones).

## Reuse a shared reference

Put reusable guidance in a folder such as `05-Knowledge/Go`, then
[grant that folder](setup.md#share-knowledge-folders) with `--shared 05-Knowledge/Go`.
The default project search does not include shared folders.

```text
Use devvault to search for "context cancellation" in the exact selector
shared:05-Knowledge/Go. Read the matching notes and compare their guidance
with this repository's request handlers. Suggest relevant changes with source
paths. Do not edit files yet.
```

Use the selector for your actual grant. For example, a grant of `05-Knowledge`
uses `shared:05-Knowledge`. Reading references needs no write permission. With
`--allow-writes`, explicitly granted folders can also receive reusable findings;
project-specific progress belongs in project notes.

## Start with a few useful notes

You can begin with `index.md` describing the project, `requirements.md` describing
the next feature, and `roadmap.md` listing its milestones. Add decision records
when a choice needs an explanation. Initialization creates the standard structure;
you supply the project's facts.

If the connection is unavailable, ask the agent to report that explicitly.
Reading the vault through shell tools does not verify that Vault Bridge works.
Use the [first connection check](../README.md#3-start-a-new-session-and-try-it)
or the [troubleshooting guide](troubleshooting.md) to diagnose it.
