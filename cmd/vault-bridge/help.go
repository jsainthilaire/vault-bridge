package main

const usage = `Vault Bridge connects your project's Markdown notes to Codex or Claude Code.
Ask your agent to read requirements, explain decisions, or review your roadmap.
Notes stay in your vault; code stays in your repository. Access is read-only by default.

Start here (replace the example paths):
  vault-bridge install /path/to/code --client codex --target /path/to/DevVault/03-Projects/my-app
  Use --client claude for Claude Code. Start a new client session in the code
  repository, then try the verification prompt printed by the installer.

Need project notes first?
  vault-bridge vault init --path /path/to/DevVault --id my-app --title "My App"
  Fill in the generated notes, then run install. Existing notes are preserved.

Commands:
  install         Connect notes to a coding client and verify the connection
  vault init      Create a vault with templates and project notes
  project init    Add project notes to an existing vault
  config check    Validate a project's access settings without changing files
  serve           Run the MCP server (normally started by your coding client)
  --version       Print the version

More options:
  --allow-writes   Allow Markdown creation and revision-checked updates
  --shared        Grant a folder under 05-Knowledge; repeat for more folders
  --global        Install in user settings; --projects-dir connects many projects
  --config        Use YAML for custom limits and mappings (requires --project)

Use a command's --help for examples, applicable flags and defaults:
  vault-bridge install --help
`

func commandHelp(command string) string {
	switch command {
	case "install":
		return `Connect existing project notes to Codex or Claude Code.
The code repository and the vault's project-notes folder are separate paths.
Install saves client settings, adds AGENTS.md or CLAUDE.md guidance when a source
repository is selected, and checks the connection through the SDK without editing notes.

One repository (recommended first setup):
  vault-bridge install /path/to/code --client codex --target /path/to/DevVault/03-Projects/my-app
  Omit the repository path to use the current directory. Use --client claude for Claude Code.

Optional note updates and shared references:
  vault-bridge install /path/to/code --client codex --target /path/to/DevVault/03-Projects/my-app --shared 05-Knowledge/Go --allow-writes
  Read-only by default. --allow-writes covers the project and explicit --shared
  folders; YAML-only shared_read_paths remain read-only.

Many projects with matching source folder names:
  vault-bridge install --global --client codex --projects-dir /path/to/DevVault/03-Projects --source-dir /path/to/source-projects
  Omit --source-dir to register every project. Global connections are available
  in every repository; each server remains bound to its selected project.

Custom YAML configuration:
  vault-bridge install /path/to/code --client codex --config /absolute/config.yaml --project my-app

After installation, start a new client session and use the printed verification
prompt. The client starts the server automatically. Installation creates no vault notes.
`
	case "vault init":
		return `Create or extend a Markdown vault with templates and project notes.
  vault-bridge vault init --path /path/to/DevVault --id my-app --title "My App"

Existing notes are preserved. Supply both --id and --title for your project;
omitting both creates the vault-bridge sample project. Fill in the generated
notes, then connect a code repository with vault-bridge install --help.
`
	case "project init":
		return `Add planning notes for one project to an existing vault.
  vault-bridge project init --vault /path/to/DevVault --id my-app --title "My App"

Creates missing notes under 03-Projects/my-app and preserves authored content.
Use vault init to create a new vault. Use install to connect the notes to a client.
`
	case "config check":
		return `Validate one project's access settings without changing files.
  vault-bridge config check --target /path/to/DevVault/03-Projects/my-app
  vault-bridge config check --config /absolute/config.yaml --project my-app

Choose a direct target or a YAML mapping. This checks paths and scope, not a live
client connection. Install also verifies MCP startup, tools and index retrieval.
`
	case "serve":
		return `Run one project's MCP server over stdin/stdout.
Your coding client normally starts this command after you run install.
If you start it manually, it waits for MCP messages; it is not an interactive prompt.

  vault-bridge serve --target /path/to/DevVault/03-Projects/my-app
  vault-bridge serve --config /absolute/config.yaml --project my-app

Access is read-only unless --allow-writes is set. Each process is fixed to one
project and its shared grants. Stdout is reserved for MCP; diagnostics use stderr.
`
	default:
		return usage
	}
}
