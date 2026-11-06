package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
)

// The prompt must identify the saved connection and actual note path, even
// when a launch alias differs from the folder name or installation is global.
func TestInstallPromptUsesSelectedScope(t *testing.T) {
	for _, mode := range []string{"target alias", "global yaml alias", "bulk", "missing index"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			vault, repo := filepath.Join(root, "Notes Vault"), filepath.Join(root, "Code Repo")
			if err := scaffold.VaultInitProject(vault, "notes-project", "My App"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(repo, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CODEX_HOME", filepath.Join(root, "client"))
			target := filepath.Join(vault, "03-Projects", "notes-project")
			args := []string{"install", repo, "--client", "codex", "--target", target, "--project", "app-alias"}
			server, project := "devvault", "app-alias"
			switch mode {
			case "global yaml alias":
				configPath := filepath.Join(root, "config.yaml")
				data := fmt.Sprintf("version: 1\nvault_root: %q\nprojects:\n  app-alias:\n    folder: 03-Projects/notes-project\n", vault)
				if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				args = []string{"install", "--global", "--client", "codex", "--config", configPath, "--project", project}
			case "bulk":
				args = []string{"install", "--global", "--client", "codex", "--projects-dir", filepath.Join(vault, "03-Projects")}
				server, project = "devvault-notes-project", "notes-project"
			case "missing index":
				if err := os.Remove(filepath.Join(target, "index.md")); err != nil {
					t.Fatal(err)
				}
			}
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatalf("install: %v; %s", err, &diag)
			}
			if mode == "missing index" {
				if strings.Contains(out.String(), "paste this") || !strings.Contains(out.String(), "Index missing") {
					t.Fatalf("missing index should show recovery instructions, not a success prompt: %s", &out)
				}
				return
			}
			for _, want := range []string{
				"Use only the " + server + " MCP tools",
				"the project is " + project + ", then read 03-Projects/notes-project/index.md.",
				"Do not edit files or use shell or\n  filesystem tools as a fallback",
			} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("prompt missing %q: %s", want, &out)
				}
			}
		})
	}
}
