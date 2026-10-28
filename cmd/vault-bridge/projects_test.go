package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCLIInstallProjectsGlobally(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			projects := filepath.Join(root, "vault", "03-Projects")
			for _, name := range []string{"zeta", "alpha", ".hidden"} {
				if err := os.MkdirAll(filepath.Join(projects, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(projects, "index.md"), []byte("# Projects\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			global := filepath.Join(root, "client")
			t.Setenv("CODEX_HOME", global)
			t.Setenv("CLAUDE_CONFIG_DIR", global)
			args := []string{"install", "--global", "--client", client, "--projects-dir", projects, "--allow-writes"}
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatalf("install projects: %v; %s", err, &diag)
			}
			output := out.String()
			first, last := strings.Index(output, "devvault-alpha"), strings.Index(output, "devvault-zeta")
			if first < 0 || last <= first || !strings.Contains(output, "/mcp") {
				t.Fatalf("missing sorted project results or restart instructions: %s", output)
			}
			name, key := ".claude.json", "mcpServers"
			if client == "codex" {
				name, key = "config.toml", "mcp_servers"
			}
			path := filepath.Join(global, name)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if client == "codex" {
				err = toml.Unmarshal(before, &doc)
			} else {
				err = json.Unmarshal(before, &doc)
			}
			if err != nil {
				t.Fatal(err)
			}
			servers := doc[key].(map[string]any)
			if len(servers) != 2 {
				t.Fatalf("registered hidden or non-directory entries: %v", servers)
			}
			for _, id := range []string{"alpha", "zeta"} {
				entry := servers["devvault-"+id].(map[string]any)
				want := []any{"serve", "--target", filepath.Join(projects, id), "--project", id, "--transport", "stdio", "--allow-writes"}
				if !reflect.DeepEqual(entry["args"], want) {
					t.Fatalf("%s launch scope = %v, want %v", id, entry["args"], want)
				}
			}
			out.Reset()
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatal(err)
			}
			if strings.Count(out.String(), "Already installed") != 2 {
				t.Fatalf("repeat install did not report existing projects: %s", &out)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("repeat install changed global configuration: %v", err)
			}
		})
	}
}

func TestCLIInstallProjectsRejectsAmbiguousFlagsBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing global", nil},
		{"config", []string{"--global", "--config", "/unused.yaml"}},
		{"target", []string{"--global", "--target", "/unused/03-Projects/project"}},
		{"project", []string{"--global", "--project", "alpha"}},
		{"empty config", []string{"--global", "--config="}},
		{"empty target", []string{"--global", "--target="}},
		{"empty project", []string{"--global", "--project="}},
		{"empty projects directory", []string{"--global", "--projects-dir="}},
		{"repo flag", []string{"--global", "--repo", "."}},
		{"repo positional", []string{"--global", "."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			global := filepath.Join(root, "client")
			t.Setenv("CODEX_HOME", global)
			t.Setenv("CLAUDE_CONFIG_DIR", global)
			projects := filepath.Join(root, "vault", "03-Projects")
			if err := os.MkdirAll(filepath.Join(projects, "alpha"), 0o700); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"install", "--client", "codex", "--projects-dir", projects}, tc.args...)
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err == nil {
				t.Fatal("ambiguous batch flags accepted")
			}
			if out.Len() != 0 {
				t.Fatalf("failure printed success output: %s", &out)
			}
			if _, err := os.Lstat(global); !os.IsNotExist(err) {
				t.Fatalf("invalid flags created global settings: %v", err)
			}
		})
	}
}
