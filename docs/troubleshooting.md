# Troubleshooting

Start with a new client session after configuration changes. Confirm the active
project with a real `project_context` call; a settings file alone is not evidence
that the client connected. See [Setup](setup.md) for the verification prompt.


| Symptom | What to check |
| --- | --- |
| `devvault` is absent in Codex | Start in the intended code repository, trust the workspace, confirm `.codex/config.toml` is there, and restart the session. Inspect `codex mcp list`. |
| Claude shows pending approval | Open an interactive session in that repository and review the project server approval through `/mcp`. |
| Server fails to start | Rebuild the binary, check literal absolute paths in the client settings, run `config check`, and inspect the client's MCP diagnostics. |
| Unknown project ID or missing path | With `--target`, check the project folder under `03-Projects`. With YAML, check its explicit mapping; all mapped folders must exist. |
| `project_context` returns the wrong project | Align the launch target or YAML mapping, `--project` ID, and agent instruction file; then restart the session. |
| A document read returns `permission_denied` | Use a vault-relative path within the active project or an explicit shared grant. Symlinks, hidden files, and attachments remain excluded. |
| A note fails with `invalid_argument` | Check its YAML frontmatter and the tool arguments. Note contents are omitted from diagnostics. |
| Agent does not consult notes automatically | Merge the workflow into the correct `AGENTS.md` or `CLAUDE.md`, start a new session, and explicitly request `project_context` to verify access. |
| Manual demo cannot find `agent-rig` | Supply your own `--project` and `--denied-project` IDs, as shown in the setup guide. |
| Paths stopped working after moving folders | Update the client's executable and target paths. With YAML, also update `vault_root` and the config-file path. Restart the client. |
| `install` reports that `devvault` already exists | Preserve other servers, remove only the old `devvault` entry, and reinstall with your chosen connection form. |
| `--target` is rejected | Select one existing folder directly under `03-Projects`; do not select the vault root, `03-Projects` itself, or a symlink. |
| Write tools are missing | Append `--allow-writes` to the installed server arguments and restart the client. Rebuilding the executable alone does not change launch permissions. |
| A write returns `conflict` | For updates, reread and merge using the latest revision. For creates, choose a new path or read and update the existing note. |
| A write returns `permission_denied` | Enable project writes at launch and use an existing parent inside the active project. Shared knowledge and symlink paths cannot be written. |
| Manual `serve` appears to hang | It is waiting for MCP input over stdin. Use `install` and let the client launch it automatically for normal use. |

Connection-file inspection and SDK checks are useful diagnostics. Verify actual
tool calls in each agent before treating that client's setup as complete.
