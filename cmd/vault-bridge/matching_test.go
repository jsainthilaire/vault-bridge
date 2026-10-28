package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISourceMatching(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			projects, sources, global := filepath.Join(root, "vault/03-Projects"), filepath.Join(root, "sources"), filepath.Join(root, "client")
			t.Setenv("CODEX_HOME", global)
			t.Setenv("CLAUDE_CONFIG_DIR", global)
			for _, path := range []string{filepath.Join(projects, "alpha"), filepath.Join(projects, "empty"), filepath.Join(sources, "alpha"), filepath.Join(sources, "empty"), filepath.Join(sources, "source-only")} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			index := filepath.Join(projects, "alpha/index.md")
			if err := os.WriteFile(index, []byte("# Alpha\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(cwd, sources)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"install", "--global", "--client", client, "--projects-dir", projects, "--source-dir", relative, "--allow-writes"}
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatalf("matching failed: %v; %s", err, &diag)
			}
			for _, want := range []string{"Installed devvault-alpha", filepath.Join(sources, "alpha"), filepath.Join(projects, "alpha"), `Skipped "empty": missing index.md`, `Skipped "source-only": no matching vault project`, "/mcp"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q in output: %s", want, &out)
				}
			}
			name := "config.toml"
			if client == "claude" {
				name = ".claude.json"
			}
			path := filepath.Join(global, name)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(before, []byte("--allow-writes")) || bytes.Contains(before, []byte("--source-dir")) {
				t.Fatalf("wrong saved launch arguments: %s", before)
			}
			out.Reset()
			if err := run(context.Background(), args, &out, &diag); err != nil || !strings.Contains(out.String(), "Already installed devvault-alpha") {
				t.Fatalf("repeat failed: %v; %s", err, &out)
			}
			// A subsequent no-match run must preserve even its previous entry.
			if err := os.Remove(index); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			diag.Reset()
			if err := run(context.Background(), args, &out, &diag); err == nil || !strings.Contains(err.Error(), "no eligible projects") {
				t.Fatalf("expected no-match failure: %v", err)
			}
			if out.Len() != 0 || !strings.Contains(diag.String(), `Skipped "alpha": missing index.md`) {
				t.Fatalf("bad failure output: stdout=%s stderr=%s", &out, &diag)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("repeat/no-match changed config: %v", err)
			}
		})
	}
}

func TestCLISourceMatchingRequiresBulkFlags(t *testing.T) {
	for _, tail := range [][]string{
		{}, {"--global"}, {"--projects-dir", "/unused/03-Projects"},
		{"--global", "--projects-dir="},
		{"--global", "--projects-dir", "/unused/03-Projects", "--source-dir="},
		{"--global", "--projects-dir", "/unused/03-Projects", "--target="},
		{"--global", "--projects-dir", "/unused/03-Projects", "--config="},
		{"--global", "--projects-dir", "/unused/03-Projects", "--project="},
		{"--global", "--projects-dir", "/unused/03-Projects", "--repo", "."},
		{"--global", "--projects-dir", "/unused/03-Projects", "."},
	} {
		t.Run(strings.Join(tail, " "), func(t *testing.T) {
			global := filepath.Join(t.TempDir(), "client")
			t.Setenv("CODEX_HOME", global)
			args := append([]string{"install", "--client", "codex", "--source-dir", "/unused/source"}, tail...)
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err == nil {
				t.Fatal("invalid flag combination accepted")
			}
			if out.Len() != 0 {
				t.Fatalf("invalid flags printed success: %s", &out)
			}
			if _, err := os.Lstat(global); !os.IsNotExist(err) {
				t.Fatalf("invalid flags created settings: %v", err)
			}
		})
	}
}
