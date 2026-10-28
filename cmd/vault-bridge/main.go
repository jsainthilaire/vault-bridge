// Vault Bridge is a local, project-scoped Markdown MCP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/config"
	"github.com/jsainthilaire/vault-bridge/internal/install"
	"github.com/jsainthilaire/vault-bridge/internal/mcpserver"
	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
	"github.com/jsainthilaire/vault-bridge/internal/vault"
)

const usage = `Vault Bridge — local project knowledge over scoped MCP

Usage:
  vault-bridge vault init --path <vault>
  vault-bridge vault init --path <vault> --id <id> --title <title>
  vault-bridge project init --vault <vault> --id <id> --title <title>
  vault-bridge config check --config <absolute-config.yaml> --project <id>
  vault-bridge config check --target <vault/03-Projects/project-folder>
  vault-bridge install --client codex|claude --repo <code-repository> --config <absolute-config.yaml> --project <id>
  vault-bridge install <code-repository> --client codex|claude --target <vault/03-Projects/project-folder>
  vault-bridge install --global --client codex|claude --target <vault/03-Projects/project-folder>
  vault-bridge install --global --client codex|claude --projects-dir <vault/03-Projects>
  vault-bridge install --global --client codex|claude --projects-dir <vault/03-Projects> --source-dir <source-projects>
  vault-bridge install --client codex|claude --target <vault/03-Projects/project-folder> --allow-writes
  vault-bridge serve --config <absolute-config.yaml> --project <id> --transport stdio
  vault-bridge serve --target <vault/03-Projects/project-folder> --transport stdio
  vault-bridge --version

Initialization adds missing files and links and preserves authored notes.
Use vault init --id and --title to create a vault for your chosen project.
Serve binds exactly one configured project. Shared knowledge requires explicit grants.
Serving is read-only unless --allow-writes enables project Markdown creation and revision-checked updates.
Configuration and vault_root must use absolute paths; ~ and variables are not expanded.
Use a command's --help for flags. Stdout is reserved for MCP when serving.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		// Errors originate from bounded diagnostics, never Markdown contents.
		message := err.Error()
		if len(message) > 1024 {
			message = message[:1024] + "…"
		}
		fmt.Fprintf(os.Stderr, "vault-bridge: %s\n", message)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help")) {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(stdout, "vault-bridge "+mcpserver.Version)
		return err
	}
	command := args[0]
	tail := args[1:]
	if command == "vault" || command == "project" || command == "config" {
		if len(tail) == 1 && (tail[0] == "--help" || tail[0] == "-h") {
			_, err := io.WriteString(stdout, usage)
			return err
		}
		if len(tail) == 0 {
			return errors.New("missing subcommand; use --help")
		}
		command += " " + tail[0]
		tail = tail[1:]
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(stderr)
	var configPath, projectID, vaultPath, title, transport, client, repo, target, projectsDir, sourceDir string
	var global, allowWrites bool
	var projectsSelected bool
	var positionalRepo string
	// flag stops at the first positional argument. Accept the repository before
	// the flags as well as after them for the install shorthand.
	if command == "install" && len(tail) > 0 && tail[0] != "" && tail[0][0] != '-' {
		positionalRepo, tail = tail[0], tail[1:]
	}
	switch command {
	case "vault init":
		f.StringVar(&vaultPath, "path", "", "Vault directory to create or extend (required)")
		f.StringVar(&projectID, "id", "", "Selected project ID (requires --title; defaults to vault-bridge when both omitted)")
		f.StringVar(&title, "title", "", "Selected project title (requires --id)")
	case "project init":
		f.StringVar(&vaultPath, "vault", "", "Existing vault directory (required)")
		f.StringVar(&projectID, "id", "", "Stable lowercase-kebab-case project ID (required)")
		f.StringVar(&title, "title", "", "Project title (required)")
	case "config check", "serve", "install":
		f.StringVar(&configPath, "config", "", "Absolute YAML configuration path (requires --project; alternative to --target)")
		f.StringVar(&projectID, "project", "", "Explicit trusted project ID (defaults to target folder name with --target)")
		f.StringVar(&target, "target", "", "Project docs folder directly under a vault's 03-Projects directory (alternative to --config)")
		if command == "serve" || command == "install" {
			f.BoolVar(&allowWrites, "allow-writes", false, "Enable project-only Markdown create/update tools; shared knowledge stays read-only")
		}
		if command == "serve" {
			f.StringVar(&transport, "transport", "stdio", "MCP transport (only stdio supported)")
		}
		if command == "install" {
			f.StringVar(&client, "client", "", "Client to configure: codex or claude (required)")
			f.StringVar(&repo, "repo", ".", "Existing code repository to configure")
			f.BoolVar(&global, "global", false, "Register MCP tools in the client's user-level configuration")
			f.StringVar(&projectsDir, "projects-dir", "", "Register every immediate project folder in a vault's 03-Projects directory (requires --global)")
			f.StringVar(&sourceDir, "source-dir", "", "Match immediate source folders by exact name to vault projects with index.md (requires --global and --projects-dir)")
		}
	default:
		return errors.New("unknown command; use --help")
	}
	if err := f.Parse(tail); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid command flags; use --help")
	}
	if command == "install" {
		if f.NArg() == 1 && positionalRepo == "" {
			positionalRepo = f.Arg(0)
		} else if f.NArg() != 0 {
			return errors.New("install accepts one code repository path; put flags before or after that path")
		}
		explicitRepo, explicitConnection, sourceSelected := false, false, false
		f.Visit(func(option *flag.Flag) {
			explicitRepo = explicitRepo || option.Name == "repo"
			projectsSelected = projectsSelected || option.Name == "projects-dir"
			sourceSelected = sourceSelected || option.Name == "source-dir"
			explicitConnection = explicitConnection || option.Name == "target" || option.Name == "config" || option.Name == "project"
		})
		if sourceSelected && (!global || !projectsSelected || projectsDir == "") {
			return errors.New("--source-dir requires --global and --projects-dir")
		}
		if sourceSelected && sourceDir == "" {
			return errors.New("--source-dir must name an existing source-projects directory")
		}
		if projectsSelected && !global {
			return errors.New("--projects-dir requires --global")
		}
		if projectsSelected && explicitConnection {
			return errors.New("--projects-dir cannot be combined with --target, --config or --project")
		}
		if global && (positionalRepo != "" || explicitRepo) {
			return errors.New("--global cannot be combined with a code repository path or --repo")
		}
		if positionalRepo != "" {
			if explicitRepo {
				return errors.New("use a positional code repository path or --repo, not both")
			}
			repo = positionalRepo
		}
		if global {
			repo = ""
		}
	} else if f.NArg() != 0 {
		return errors.New("unexpected positional arguments; use --help")
	}
	switch command {
	case "vault init":
		if vaultPath == "" {
			return errors.New("--path is required")
		}
		selectedProject := false
		f.Visit(func(option *flag.Flag) {
			selectedProject = selectedProject || option.Name == "id" || option.Name == "title"
		})
		var err error
		if selectedProject {
			if projectID == "" || title == "" {
				return errors.New("--id and --title must both be provided and nonempty")
			}
			err = scaffold.VaultInitProject(vaultPath, projectID, title)
		} else {
			err = scaffold.VaultInit(vaultPath)
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, "Vault initialized; existing notes preserved.")
		return err
	case "project init":
		if vaultPath == "" || projectID == "" || title == "" {
			return errors.New("--vault, --id and --title are required")
		}
		if err := scaffold.ProjectInit(vaultPath, projectID, title); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "Project initialized; existing notes preserved.")
		return err
	}
	if target != "" && configPath != "" {
		return errors.New("--target and --config are mutually exclusive")
	}
	if !projectsSelected && target == "" && (configPath == "" || projectID == "") {
		return errors.New("--config and --project are required")
	}
	if command == "serve" && transport != "stdio" {
		return errors.New("unsupported transport: only stdio is available")
	}
	if command == "install" {
		binary, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate vault-bridge executable: %w", err)
		}
		if projectsSelected {
			result, err := install.Projects(install.Options{
				Client: client, Repo: repo, ProjectsDir: projectsDir, SourceDir: sourceDir, Binary: binary,
				Global: global, AllowWrites: allowWrites,
			})
			if err != nil {
				if errors.Is(err, install.ErrNoMatches) {
					if reportErr := reportSkipped(stderr, result.Skipped); reportErr != nil {
						return reportErr
					}
				}
				return err
			}
			for _, project := range result.Projects {
				state := "Installed"
				if !project.Changed {
					state = "Already installed"
				}
				if _, err := fmt.Fprintf(stdout, "%s devvault-%s globally for %s project %s in %s.\n", state, project.Project, client, project.Project, project.Path); err != nil {
					return err
				}
				if project.Source != "" {
					if _, err := fmt.Fprintf(stdout, "  source=%q vault=%q\n", project.Source, project.Target); err != nil {
						return err
					}
				}
			}
			if err := reportSkipped(stdout, result.Skipped); err != nil {
				return err
			}
			_, err = fmt.Fprintf(stdout, "Start a new %s session and check /mcp. All %d registered projects are available in every repository; each server keeps its own project scope.\n", client, len(result.Projects))
			return err
		}
		result, err := install.Project(install.Options{
			Client: client, Repo: repo, Config: configPath, Project: projectID, Target: target, Binary: binary,
			Global: global, AllowWrites: allowWrites,
		})
		if err != nil {
			return err
		}
		state := "Installed"
		if !result.Changed {
			state = "Already installed"
		}
		if global {
			_, err = fmt.Fprintf(stdout, "%s devvault globally for %s project %s in %s.\nStart a new %s session and check /mcp. The same project scope applies in every repository.\n", state, client, result.Project, result.Path, client)
		} else {
			_, err = fmt.Fprintf(stdout, "%s devvault for %s project %s in %s.\nStart a new %s session in that repository and check /mcp.\n", state, client, result.Project, result.Path, client)
		}
		return err
	}
	var scope *config.Scope
	var err error
	if target != "" {
		scope, err = config.FromTarget(target, projectID)
	} else {
		scope, err = config.Load(configPath, projectID)
	}
	if err != nil {
		return err
	}
	scope.AllowWrites = allowWrites
	store, err := vault.New(scope)
	if err != nil {
		return err
	}
	defer store.Close()
	if command == "config check" {
		_, err := fmt.Fprintf(stdout, "Configuration valid for project %s (%d shared grants).\n", scope.ProjectID, len(scope.SharedReadPaths))
		return err
	}
	server, maxFrameBytes := mcpserver.New(store), mcpserver.MaxFrameBytes
	if allowWrites {
		server, maxFrameBytes = mcpserver.NewWritable(store, store, stderr), mcpserver.MaxWriteFrameBytes
	}
	err = server.Run(ctx, &mcp.StdioTransport{MaxLineLength: maxFrameBytes})
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return errors.New("MCP stdio session failed")
	}
	return nil
}

func reportSkipped(out io.Writer, skipped []install.SkippedProject) error {
	for _, project := range skipped {
		if _, err := fmt.Fprintf(out, "Skipped %q: %s (source=%q vault=%q).\n", project.Project, project.Reason, project.Source, project.Target); err != nil {
			return err
		}
	}
	return nil
}
