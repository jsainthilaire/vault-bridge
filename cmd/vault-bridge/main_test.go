package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
)

func TestCLI(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"help", []string{"--help"}, false},
		{"version", []string{"--version"}, false},
		{"vault help", []string{"vault", "init", "--help"}, false},
		{"project help", []string{"project", "--help"}, false},
		{"install help", []string{"install", "--help"}, false},
		{"install missing flags", []string{"install", "--client", "codex"}, true},
		{"install duplicate repo", []string{"install", ".", "--repo", ".", "--client", "codex", "--target", "/vault/03-Projects/alpha"}, true},
		{"global with positional repo", []string{"install", ".", "--global", "--client", "codex", "--target", "/vault/03-Projects/alpha"}, true},
		{"global with repo flag", []string{"install", "--global", "--repo", ".", "--client", "claude", "--target", "/vault/03-Projects/alpha"}, true},
		{"install extra repos", []string{"install", ".", "--client", "codex", "--target", "/vault/03-Projects/alpha", "other"}, true},
		{"target and config", []string{"install", "--client", "codex", "--config", "/config.yaml", "--target", "/vault/03-Projects/alpha"}, true},
		{"missing project", []string{"serve", "--config", "/tmp/config.yaml"}, true},
		{"unknown", []string{"delete"}, true},
		{"extra args", []string{"vault", "init", "extra"}, true},
		{"no init path", []string{"vault", "init"}, true},
		{"no title", []string{"project", "init", "--vault", "/tmp/vault", "--id", "x"}, true},
		{"no HTTP", []string{"serve", "--config", "/tmp/config.yaml", "--project", "x", "--transport", "http"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diag bytes.Buffer
			err := run(context.Background(), tc.args, &out, &diag)
			if (err != nil) != tc.wantError {
				t.Fatalf("run error = %v, wantError %v", err, tc.wantError)
			}
			if tc.wantError && out.Len() != 0 {
				t.Fatalf("failure wrote to stdout: %q", out.String())
			}
		})
	}
}

func TestCLIInstallGlobal(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(root, "vault")
	if err := scaffold.VaultInit(vault); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			dir := filepath.Join(root, client)
			t.Setenv("CODEX_HOME", dir)
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			args := []string{"install", "--global", "--client", client, "--target", filepath.Join(vault, "03-Projects/vault-bridge")}
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatalf("global install: %v; %s", err, &diag)
			}
			if !strings.Contains(out.String(), "Installed devvault globally") || !strings.Contains(out.String(), "same project scope") {
				t.Fatalf("unexpected global output: %s", &out)
			}
			name := ".claude.json"
			if client == "codex" {
				name = "config.toml"
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if err := run(context.Background(), args, &out, &diag); err != nil || !strings.Contains(out.String(), "Already installed devvault globally") {
				t.Fatalf("repeat global install: %v; %s", err, &out)
			}
		})
	}
}

func TestCLIInstallTarget(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(root, "vault")
	if err := scaffold.VaultInit(vault); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(vault, "03-Projects", "vault-bridge")
	for _, client := range []string{"codex", "claude"} {
		for _, position := range []string{"leading", "trailing", "flag"} {
			t.Run(client+"/"+position, func(t *testing.T) {
				repo := filepath.Join(root, client+"-"+position)
				if err := os.Mkdir(repo, 0o700); err != nil {
					t.Fatal(err)
				}
				args := []string{"install", "--client", client, "--target", target}
				switch position {
				case "leading":
					args = append([]string{"install", repo}, args[1:]...)
				case "trailing":
					args = append(args, repo)
				case "flag":
					args = append(args, "--repo", repo)
				}
				var out, diag bytes.Buffer
				if err := run(context.Background(), args, &out, &diag); err != nil {
					t.Fatalf("install: %v; %s", err, &diag)
				}
				if !strings.Contains(out.String(), "project vault-bridge") {
					t.Fatalf("missing inferred project: %s", &out)
				}
				path := filepath.Join(repo, ".mcp.json")
				if client == "codex" {
					path = filepath.Join(repo, ".codex", "config.toml")
				}
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(data, []byte("--target")) || bytes.Contains(data, []byte("--config")) {
					t.Fatalf("unexpected launch configuration: %s; %v", data, err)
				}
			})
		}
	}
	var out, diag bytes.Buffer
	if err := run(context.Background(), []string{"config", "check", "--target", target}, &out, &diag); err != nil || !strings.Contains(out.String(), "project vault-bridge (0 shared grants)") {
		t.Fatalf("target check: %v; %s", err, &out)
	}
}

func TestCLIInstall(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(root, "vault")
	if err := scaffold.VaultInit(vault); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.yaml")
	configData := fmt.Sprintf("version: 1\nvault_root: %q\nprojects:\n  vault-bridge:\n    folder: 03-Projects/vault-bridge\n", vault)
	if err := os.WriteFile(configFile, []byte(configData), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			args := []string{"install", "--client", client, "--repo", root, "--config", configFile, "--project", "vault-bridge"}
			var out, diag bytes.Buffer
			if err := run(context.Background(), args, &out, &diag); err != nil {
				t.Fatalf("install: %v; %s", err, diag.String())
			}
			if !strings.Contains(out.String(), "Installed devvault") {
				t.Fatalf("unexpected output: %s", &out)
			}
			out.Reset()
			if err := run(context.Background(), args, &out, &diag); err != nil || !strings.Contains(out.String(), "Already installed") {
				t.Fatalf("repeat install: %v; %s", err, &out)
			}
		})
	}
}

// The helper runs the actual CLI dispatch and official SDK stdio transport in a
// subprocess. os.Exit prevents the Go test harness from writing PASS to stdout.
func TestStdioServerHelper(t *testing.T) {
	if os.Getenv("VAULT_BRIDGE_TEST_SERVER") != "1" {
		return
	}
	args := []string{"serve", "--config", os.Getenv("VAULT_BRIDGE_TEST_CONFIG"), "--project", "vault-bridge"}
	if target := os.Getenv("VAULT_BRIDGE_TEST_TARGET"); target != "" {
		args = []string{"serve", "--target", target}
	}
	if os.Getenv("VAULT_BRIDGE_TEST_WRITES") == "1" {
		args = append(args, "--allow-writes")
	}
	err := run(context.Background(), args, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestOfficialSDKTargetIsolation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.VaultInit(root); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.ProjectInit(root, "other-project", "Other Project"); err != nil {
		t.Fatal(err)
	}
	private := "OTHER-PROJECT-CONTENT-9182"
	if err := os.WriteFile(filepath.Join(root, "03-Projects/other-project/index.md"), []byte("# Other\n"+private), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "03-Projects", "vault-bridge")
	if err := os.Symlink(filepath.Join(root, "03-Projects/other-project/index.md"), filepath.Join(target, "linked.md")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioServerHelper$")
	cmd.Env = append(os.Environ(), "VAULT_BRIDGE_TEST_SERVER=1", "VAULT_BRIDGE_TEST_CONFIG=", "VAULT_BRIDGE_TEST_TARGET="+target)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	client := mcp.NewClient(&mcp.Implementation{Name: "target-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v; %s", err, &diagnostics)
	}
	defer session.Close()
	for _, tc := range []struct {
		name string
		args map[string]any
		code string
	}{
		{"project_context", nil, ""},
		{"document_list", nil, ""},
		{"document_read", map[string]any{"path": "03-Projects/vault-bridge/index.md"}, ""},
		{"document_search", map[string]any{"query": private}, ""},
		{"feature_list", nil, ""},
		{"decision_list", nil, ""},
		{"document_read", map[string]any{"path": "03-Projects/other-project/index.md"}, "permission_denied"},
		{"document_read", map[string]any{"path": "05-Knowledge/Go/index.md"}, "permission_denied"},
		{"document_read", map[string]any{"path": "03-Projects/vault-bridge/linked.md"}, "permission_denied"},
		{"document_read", map[string]any{"path": "03-Projects/vault-bridge/../other-project/index.md"}, "invalid_argument"},
		{"document_read", map[string]any{"path": "03-Projects/vault-bridge/index.md", "project": "other-project"}, "invalid_argument"},
	} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(private)) {
			t.Fatalf("%s leaked another project", tc.name)
		}
		if tc.code == "" {
			if res.IsError {
				t.Fatalf("%s: %s", tc.name, data)
			}
			if tc.name == "project_context" && (!bytes.Contains(data, []byte(`"project":"vault-bridge"`)) || !bytes.Contains(data, []byte("03-Projects/vault-bridge/index.md"))) {
				t.Fatalf("unexpected target identity: %s", data)
			}
		} else {
			if !res.IsError || len(res.Content) != 1 {
				t.Fatalf("%s expected %s: %s", tc.name, tc.code, data)
			}
			message, ok := res.Content[0].(*mcp.TextContent)
			if !ok || !strings.Contains(message.Text, `"code":"`+tc.code+`"`) {
				t.Fatalf("%s expected %s: %s", tc.name, tc.code, data)
			}
		}
	}
}

func TestOfficialSDKStdioIsolation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.VaultInit(root); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.ProjectInit(root, "private-project", "Private Project"); err != nil {
		t.Fatal(err)
	}
	private := "UNRELATED-PRIVATE-CONTENT-7362"
	if err := os.WriteFile(filepath.Join(root, "03-Projects/private-project/secret.md"), []byte("# Private\n"+private), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "05-Knowledge/Go/reference.md"), []byte("# Go Reference\nSHARED-KNOWLEDGE-7182"), 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	configData := fmt.Sprintf("version: 1\nvault_root: %q\nprojects:\n  vault-bridge:\n    folder: 03-Projects/vault-bridge\n    shared_read_paths: [05-Knowledge/Go]\n  private-project:\n    folder: 03-Projects/private-project\n", root)
	if err := os.WriteFile(configFile, []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if err := run(context.Background(), []string{"config", "check", "--config", configFile, "--project", "vault-bridge"}, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioServerHelper$")
	cmd.Env = append(os.Environ(), "VAULT_BRIDGE_TEST_SERVER=1", "VAULT_BRIDGE_TEST_CONFIG="+configFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "vault-bridge-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v; stderr: %s", err, stderr.String())
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		if stderr.Len() != 0 {
			t.Errorf("unexpected server diagnostic: %q", stderr.String())
		}
	}()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 6 {
		t.Fatalf("got %d tools, want 6", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(schema, []byte(`"project"`)) {
			t.Fatalf("%s accepts a project selector: %s", tool.Name, schema)
		}
		if tool.OutputSchema == nil || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("missing output schema/read-only annotation: %s", tool.Name)
		}
	}
	call := func(name string, args map[string]any, code string) string {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s protocol error: %v", name, err)
		}
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(private)) || bytes.Contains(data, []byte("private-project")) {
			t.Fatalf("%s leaked unrelated project: %s", name, data)
		}
		if code == "" {
			if res.IsError {
				t.Fatalf("%s failed: %s", name, data)
			}
			if res.StructuredContent == nil {
				t.Fatalf("%s has no structured result", name)
			}
		} else {
			if !res.IsError || len(res.Content) != 1 {
				t.Fatalf("%s expected %s, got %s", name, code, data)
			}
			text, ok := res.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("%s non-text error", name)
			}
			var e struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(text.Text), &e); err != nil || e.Error.Code != code {
				t.Fatalf("%s expected %s, got %s", name, code, text.Text)
			}
		}
		return string(data)
	}
	call("project_context", nil, "")
	call("document_list", nil, "")
	for _, p := range []string{"index.md", "architecture.md", "roadmap.md"} {
		call("document_read", map[string]any{"path": "03-Projects/vault-bridge/" + p}, "")
	}
	call("decision_list", nil, "")
	call("feature_list", nil, "")
	call("document_search", map[string]any{"query": private}, "")
	call("document_read", map[string]any{"path": "03-Projects/private-project/secret.md"}, "permission_denied")
	call("document_list", map[string]any{"section": "../private-project"}, "invalid_argument")
	call("document_read", map[string]any{"path": "03-Projects/vault-bridge/../../private-project/index.md"}, "invalid_argument")
	call("document_read", map[string]any{"path": "03-Projects/vault-bridge/index.md", "project": "private-project"}, "invalid_argument")
	call("document_read", map[string]any{"path": 123}, "invalid_argument")
	if data := call("document_search", map[string]any{"query": "SHARED-KNOWLEDGE-7182"}, ""); strings.Contains(data, "reference.md") {
		t.Fatal("default search included shared root")
	}
	if data := call("document_search", map[string]any{"query": "SHARED-KNOWLEDGE-7182", "section": "shared:05-Knowledge/Go"}, ""); !strings.Contains(data, "reference.md") {
		t.Fatal("explicit shared selector did not search granted root")
	}
}
