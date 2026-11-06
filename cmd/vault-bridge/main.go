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
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/config"
	"github.com/jsainthilaire/vault-bridge/internal/install"
	"github.com/jsainthilaire/vault-bridge/internal/mcpserver"
	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
	"github.com/jsainthilaire/vault-bridge/internal/vault"
)

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
	f.Usage = func() {
		fmt.Fprint(f.Output(), commandHelp(command))
		fmt.Fprintln(f.Output(), "\nFlags:")
		f.PrintDefaults()
	}
	var configPath, projectID, vaultPath, title, transport, client, repo, target, projectsDir, sourceDir string
	var global, allowWrites bool
	var sharedPaths []string
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
		f.Func("shared", "Vault-relative shared folder (05-Knowledge or a subfolder); repeat to add grants; writable with --allow-writes", func(value string) error {
			if len(sharedPaths) >= config.MaxSharedPaths {
				return errors.New("at most 32 --shared flags are allowed")
			}
			sharedPaths = append(sharedPaths, value)
			return nil
		})
		f.StringVar(&configPath, "config", "", "Absolute YAML configuration path (requires --project; alternative to --target)")
		f.StringVar(&projectID, "project", "", "Explicit trusted project ID (defaults to target folder name with --target)")
		f.StringVar(&target, "target", "", "Project docs folder directly under a vault's 03-Projects directory (alternative to --config)")
		if command == "serve" || command == "install" {
			f.BoolVar(&allowWrites, "allow-writes", false, "Enable Markdown create/update in the project and explicit --shared folders; YAML shared_read_paths remain read-only")
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
		return errors.New("choose --target <vault/03-Projects/project-folder> or --config <absolute-config.yaml> with --project <id>")
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
				Global: global, AllowWrites: allowWrites, SharedPaths: sharedPaths,
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
				if err := finishInstall(ctx, stdout, project, "devvault-"+project.Project); err != nil {
					return err
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
			Global: global, AllowWrites: allowWrites, SharedPaths: sharedPaths,
		})
		if err != nil {
			return err
		}
		state := "Installed"
		if !result.Changed {
			state = "Already installed"
		}
		if err := finishInstall(ctx, stdout, result, "devvault"); err != nil {
			return err
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
	if err := config.AddShared(scope, sharedPaths); err != nil {
		return err
	}
	store, err := vault.New(scope)
	if err != nil {
		return err
	}
	defer store.Close()
	if command == "config check" {
		_, err := fmt.Fprintf(stdout, "Configuration valid for project %s (%d shared grants).\n", scope.ProjectID, len(scope.SharedGrants()))
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

func finishInstall(ctx context.Context, out io.Writer, result install.Result, serverName string) error {
	if result.InstructionsPath != "" {
		state := "Agent instructions already current"
		if result.InstructionsChanged {
			state = "Updated agent instructions"
		}
		if _, err := fmt.Fprintf(out, "%s: %s.\n", state, result.InstructionsPath); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(out, "No source repository selected; agent instruction files unchanged."); err != nil {
		return err
	}
	indexRead, err := install.Verify(ctx, result)
	if err != nil {
		return fmt.Errorf("connection saved in %s, but verification failed for %s; fix the error and rerun install: %w", result.Path, result.Project, err)
	}
	access := "six read tools"
	if result.AllowWrites {
		access += " plus document_create and document_update"
	}
	if _, err := fmt.Fprintf(out, "Verified MCP stdio startup and %s for %s (SDK check; no vault writes).\n", access, result.Project); err != nil {
		return err
	}
	for _, shared := range result.SharedPaths {
		mode := "read-only"
		for _, writable := range result.SharedWritablePaths {
			if shared == writable || strings.HasPrefix(shared, writable+"/") {
				mode = "read and write"
				break
			}
		}
		if _, err := fmt.Fprintf(out, "Verified shared listing and available note retrieval: %s (%s; writes not exercised).\n", shared, mode); err != nil {
			return err
		}
	}
	if !indexRead {
		_, err = fmt.Fprintln(out, "Index missing: project identity and retrieval could not be verified. Add index.md and rerun install.")
	} else {
		if _, err = fmt.Fprintf(out, "Verified project identity and read %s.\n", result.IndexPath); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "\nAfter starting a new client session, paste this into the agent conversation:\n  Use only the %s MCP tools for this check. Call project_context and confirm\n  the project is %s, then read %s.\n  Report the source path and actual result. Do not edit files or use shell or\n  filesystem tools as a fallback if MCP is unavailable.\n\n", serverName, result.Project, result.IndexPath)
	}
	return err
}

func reportSkipped(out io.Writer, skipped []install.SkippedProject) error {
	for _, project := range skipped {
		if _, err := fmt.Fprintf(out, "Skipped %q: %s (source=%q vault=%q).\n", project.Project, project.Reason, project.Source, project.Target); err != nil {
			return err
		}
	}
	return nil
}
