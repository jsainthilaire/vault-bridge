package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

// TestExistingVaultStdioAcceptance is explicitly opt-in. It performs read-only
// calls through a built binary and verifies project notes remain unchanged.
// Ordinary automated tests continue to use temporary vaults exclusively.
func TestExistingVaultStdioAcceptance(t *testing.T) {
	configPath := os.Getenv("VAULT_BRIDGE_ACCEPTANCE_CONFIG")
	binary := os.Getenv("VAULT_BRIDGE_ACCEPTANCE_BINARY")
	if configPath == "" && binary == "" {
		t.Skip("set VAULT_BRIDGE_ACCEPTANCE_CONFIG and VAULT_BRIDGE_ACCEPTANCE_BINARY for a real-vault read-only SDK check")
	}
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(binary) {
		t.Fatal("acceptance config and binary must both be absolute paths")
	}
	project := os.Getenv("VAULT_BRIDGE_ACCEPTANCE_PROJECT")
	if project == "" {
		project = "vault-bridge"
	}
	denied := os.Getenv("VAULT_BRIDGE_ACCEPTANCE_DENIED")
	if denied == "" {
		t.Fatal("VAULT_BRIDGE_ACCEPTANCE_DENIED must name an ungranted vault-relative Markdown path")
	}
	scope, err := config.Load(configPath, project)
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(scope.VaultRoot, filepath.FromSlash(scope.ProjectRoot))
	before := projectHashes(t, projectRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "serve", "--config", configPath, "--project", project, "--transport", "stdio")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "vault-bridge-real-vault-check", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("SDK stdio connect: %v; stderr: %s", err, stderr.String())
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close stdio server: %v", err)
		}
		if stderr.Len() != 0 {
			t.Errorf("unexpected server diagnostics: %s", stderr.String())
		}
		after := projectHashes(t, projectRoot)
		if !reflect.DeepEqual(before, after) {
			t.Error("existing project files changed during read-only acceptance")
		}
	}()
	toolList, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(toolList.Tools) != 6 {
		t.Fatalf("got %d tools, want six read tools", len(toolList.Tools))
	}
	seen := map[string]bool{}
	for _, tool := range toolList.Tools {
		seen[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s not declared read-only", tool.Name)
		}
	}
	for _, tool := range []string{"project_context", "document_list", "document_read", "document_search", "decision_list", "feature_list"} {
		if !seen[tool] {
			t.Fatalf("missing required tool %s", tool)
		}
	}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s protocol error: %v", name, err)
		}
		return result
	}
	for _, name := range []string{"project_context", "document_list", "decision_list", "feature_list"} {
		result := call(name, nil)
		if result.IsError || result.StructuredContent == nil {
			t.Fatalf("%s failed or missing structured content", name)
		}
		t.Logf("%s succeeded", name)
	}
	for _, name := range []string{"index.md", "architecture.md", "roadmap.md"} {
		result := call("document_read", map[string]any{"path": scope.ProjectRoot + "/" + name, "max_chars": 300})
		if result.IsError || result.StructuredContent == nil {
			t.Fatalf("document_read %s failed", name)
		}
		t.Logf("document_read %s succeeded", name)
	}
	result := call("document_search", map[string]any{"query": "project"})
	if result.IsError || result.StructuredContent == nil {
		t.Fatal("document_search failed")
	}
	t.Log("document_search succeeded")
	denial := call("document_read", map[string]any{"path": denied})
	if !denial.IsError || len(denial.Content) != 1 {
		t.Fatal("ungranted document_read was not rejected")
	}
	text, ok := denial.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("denial did not return a text application error")
	}
	var appError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text.Text), &appError); err != nil || appError.Error.Code != "permission_denied" {
		t.Fatalf("ungranted read code = %q (%v), want permission_denied", appError.Error.Code, err)
	}
	t.Log("ungranted document_read denied with permission_denied")
}

func projectHashes(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	hashes := map[string][sha256.Size]byte{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			hashes[rel] = sha256.Sum256(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hashes
}
