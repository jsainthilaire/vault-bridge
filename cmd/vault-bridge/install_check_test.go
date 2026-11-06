package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/install"
	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
)

// Install verifies os.Executable over stdio. Let the test executable serve that
// real subprocess path as well, without a mock verifier or a developer vault.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOneCommandInstallation(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			vault, repo := filepath.Join(root, "Vault"), filepath.Join(root, "Code")
			if err := scaffold.VaultInit(vault); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(repo, 0o700); err != nil {
				t.Fatal(err)
			}
			before := projectHashes(t, vault)
			var out, diag bytes.Buffer
			args := []string{"install", repo, "--client", client, "--target", filepath.Join(vault, "03-Projects/vault-bridge"), "--allow-writes"}
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatal(err)
			}
			file := "AGENTS.md"
			if client == "claude" {
				file = "CLAUDE.md"
			}
			data, err := os.ReadFile(filepath.Join(repo, file))
			if err != nil || !bytes.Contains(data, []byte("expected_revision")) || !bytes.Contains(data, []byte("automatically update")) {
				t.Fatalf("missing write instructions: %v", err)
			}
			for _, want := range []string{"Updated agent instructions", "Verified MCP stdio startup", "document_create and document_update", "Verified project identity and read", "Start a new"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q: %s", want, &out)
				}
			}
			if !reflect.DeepEqual(before, projectHashes(t, vault)) {
				t.Fatal("installation wrote vault notes")
			}
		})
	}
}

func TestInstallVerificationFailures(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.VaultInit(root); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := install.Result{Binary: binary, Args: []string{"serve", "--target", filepath.Join(root, "03-Projects/vault-bridge")}, Project: "wrong-project", IndexPath: "03-Projects/vault-bridge/index.md"}
	if _, err := install.Verify(context.Background(), result); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("identity mismatch accepted: %v", err)
	}
	result.Project, result.AllowWrites = "vault-bridge", true
	if _, err := install.Verify(context.Background(), result); err == nil || !strings.Contains(err.Error(), "write access") {
		t.Fatalf("missing write tools accepted: %v", err)
	}
	result.AllowWrites = false
	if err := os.Remove(filepath.Join(root, result.IndexPath)); err != nil {
		t.Fatal(err)
	}
	if read, err := install.Verify(context.Background(), result); err != nil || read {
		t.Fatalf("missing index not reported: %v, %v", read, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := install.Verify(ctx, result); err == nil {
		t.Fatal("canceled verification succeeded")
	}
}

func TestInstallVerificationFailureCanBeRetried(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault, repo := filepath.Join(root, "Vault"), filepath.Join(root, "Code")
	if err := scaffold.VaultInit(vault); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(vault, "03-Projects/vault-bridge")
	index := filepath.Join(target, "index.md")
	if err := os.WriteFile(index, []byte("---\ninvalid: [\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"install", repo, "--client", "codex", "--target", target}
	var out, diag bytes.Buffer
	if err := run(context.Background(), args, &out, &diag); err == nil || !strings.Contains(err.Error(), "connection saved") || !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("missing recovery diagnostic: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(repo, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("# Repaired project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), args, &out, &diag); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Verified project identity") || !strings.Contains(out.String(), "Already installed") {
		t.Fatalf("retry did not verify: %s", &out)
	}
	for path, want := range map[string][]byte{".codex/config.toml": settings, "AGENTS.md": instructions} {
		got, err := os.ReadFile(filepath.Join(repo, path))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("retry changed %s: %v", path, err)
		}
	}
}
