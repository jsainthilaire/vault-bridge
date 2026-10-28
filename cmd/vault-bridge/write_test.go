package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
	"github.com/jsainthilaire/vault-bridge/internal/vault"
)

func writableCLIFixture(t *testing.T) (string, string) {
	t.Helper()
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
	config := filepath.Join(root, "bridge.yaml")
	data := fmt.Sprintf("version: 1\nvault_root: %q\nprojects:\n  vault-bridge:\n    folder: 03-Projects/vault-bridge\n    shared_read_paths: [05-Knowledge/Go]\n", root)
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, config
}

func connectCLIForWrites(t *testing.T, config string, writable bool) (*mcp.ClientSession, func() string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioServerHelper$")
	writes := "0"
	if writable {
		writes = "1"
	}
	cmd.Env = append(os.Environ(), "VAULT_BRIDGE_TEST_SERVER=1", "VAULT_BRIDGE_TEST_TARGET=", "VAULT_BRIDGE_TEST_CONFIG="+config, "VAULT_BRIDGE_TEST_WRITES="+writes)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "write-acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v; %s", err, stderr.String())
	}
	closed := false
	closeSession := func() string {
		if !closed {
			if err := session.Close(); err != nil {
				t.Errorf("close writable CLI: %v", err)
			}
			cancel()
			closed = true
		}
		return stderr.String()
	}
	t.Cleanup(func() { closeSession() })
	return session, closeSession
}

func cliWriteCall(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, wantCode string, out any) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s protocol error: %v", name, err)
	}
	if wantCode != "" {
		if !result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
			t.Fatalf("%s expected %s, got %#v", name, wantCode, result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("%s returned non-text error", name)
		}
		var failure struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(text.Text), &failure); err != nil || failure.Error.Code != wantCode {
			t.Fatalf("%s expected %s: %s (%v)", name, wantCode, text.Text, err)
		}
		return
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("%s did not succeed: %#v", name, result)
	}
	if out != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
}

func vaultBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
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
			result[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCLIWriteToolsRequireOptIn(t *testing.T) {
	root, config := writableCLIFixture(t)
	before := vaultBytes(t, root)
	session, closeSession := connectCLIForWrites(t, config, false)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 6 {
		t.Fatalf("default server has %d tools, want six", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.Name == "document_create" || tool.Name == "document_update" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("default server exposes mutation capability: %#v", tool)
		}
	}
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_create", Arguments: map[string]any{"path": "03-Projects/vault-bridge/new.md", "markdown": "# Must not write"}}); err == nil {
		t.Fatal("default server accepted unregistered write tool")
	}
	if diagnostics := closeSession(); diagnostics != "" {
		t.Fatalf("read-only server wrote diagnostics: %q", diagnostics)
	}
	if !reflect.DeepEqual(before, vaultBytes(t, root)) {
		t.Fatal("default server changed vault bytes")
	}
}

func TestCLIWritableMilestonesAndIsolation(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("project writes are supported on Linux and macOS")
	}
	root, config := writableCLIFixture(t)
	const roadmapPath = "03-Projects/vault-bridge/roadmap.md"
	const original = "---\ntitle: Release roadmap\nowner: Human Author\n---\n\n# Roadmap\n\n## Milestones\n\n- [ ] Integrate the client\n\n## Notes\n\nKeep this authored paragraph and [[requirements|requirements]].\n"
	if err := os.WriteFile(filepath.Join(root, roadmapPath), []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, roadmapPath), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "03-Projects/other-project/index.md"), filepath.Join(root, "03-Projects/vault-bridge/linked.md")); err != nil {
		t.Fatal(err)
	}
	session, closeSession := connectCLIForWrites(t, config, true)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 8 {
		t.Fatalf("writable server has %d tools, want eight", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte(`"project"`)) {
			t.Fatalf("%s accepts a project selector", tool.Name)
		}
		if tool.Name == "document_create" || tool.Name == "document_update" {
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint != (tool.Name == "document_update") {
				t.Fatalf("incorrect mutation annotations: %#v", tool)
			}
			if tool.OutputSchema == nil {
				t.Fatalf("%s has no output schema", tool.Name)
			}
		} else if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("read tool lost its read-only annotation: %s", tool.Name)
		}
	}
	var read vault.Document
	cliWriteCall(t, session, "document_read", map[string]any{"path": roadmapPath}, "", &read)
	digest := sha256.Sum256([]byte(original))
	if read.Truncated || read.Markdown != original || read.Revision != hex.EncodeToString(digest[:]) {
		t.Fatalf("full read did not provide original source and revision: %+v", read)
	}
	updated := strings.Replace(original, "- [ ] Integrate the client", "- [x] Integrate the client\n- [ ] Publish release notes", 1)
	var result vault.WriteResult
	cliWriteCall(t, session, "document_update", map[string]any{"path": roadmapPath, "markdown": updated, "expected_revision": read.Revision}, "", &result)
	if result.Path != roadmapPath || result.Revision == read.Revision {
		t.Fatalf("update did not return a new committed revision: %+v", result)
	}
	cliWriteCall(t, session, "document_read", map[string]any{"path": roadmapPath}, "", &read)
	if read.Markdown != updated || read.Revision != result.Revision {
		t.Fatal("milestone update lost authored content or did not persist")
	}
	info, err := os.Stat(filepath.Join(root, roadmapPath))
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("update changed note permissions: %v, %v", info, err)
	}
	// Escaping makes this request exceed the old 64 KiB stdio-frame bound.
	const newPath = "03-Projects/vault-bridge/features/large-note.md"
	large := "# New Note\nPRIVATE-AUDIT-CONTENT-4932\n" + strings.Repeat("\"\\\t", 30000)
	if len(large) <= 64*1024 {
		t.Fatal("test payload does not exceed the read-only frame cap")
	}
	cliWriteCall(t, session, "document_create", map[string]any{"path": newPath, "markdown": large}, "", &result)
	persisted, err := os.ReadFile(filepath.Join(root, newPath))
	if err != nil || string(persisted) != large {
		t.Fatalf("large escaped document was not persisted intact: %v", err)
	}
	beforeFailures := vaultBytes(t, root)
	for _, tc := range []struct {
		name string
		args map[string]any
		code string
	}{
		{"document_create", map[string]any{"path": newPath, "markdown": "# Duplicate"}, "conflict"},
		{"document_update", map[string]any{"path": roadmapPath, "markdown": original, "expected_revision": hex.EncodeToString(digest[:])}, "conflict"},
		{"document_update", map[string]any{"path": roadmapPath, "markdown": original}, "invalid_argument"},
		{"document_update", map[string]any{"path": roadmapPath, "markdown": original, "expected_revision": read.Revision, "project": "other-project"}, "invalid_argument"},
		{"document_create", map[string]any{"path": "03-Projects/other-project/new.md", "markdown": "# Denied"}, "permission_denied"},
		{"document_create", map[string]any{"path": "05-Knowledge/Go/new.md", "markdown": "# Denied"}, "permission_denied"},
		{"document_update", map[string]any{"path": "05-Knowledge/Go/index.md", "markdown": "# Denied", "expected_revision": read.Revision}, "permission_denied"},
		{"document_update", map[string]any{"path": "03-Projects/vault-bridge/linked.md", "markdown": "# Denied", "expected_revision": read.Revision}, "permission_denied"},
		{"document_create", map[string]any{"path": "03-Projects/vault-bridge/../other-project/new.md", "markdown": "# Denied"}, "invalid_argument"},
		{"document_create", map[string]any{"path": "03-Projects/vault-bridge/oversize.md", "markdown": strings.Repeat("x", 262145)}, "resource_limit"},
	} {
		cliWriteCall(t, session, tc.name, tc.args, tc.code, nil)
	}
	if !reflect.DeepEqual(beforeFailures, vaultBytes(t, root)) {
		t.Fatal("a rejected write changed vault bytes or left temporary files")
	}
	diagnostics := closeSession()
	if strings.Contains(diagnostics, "PRIVATE-AUDIT-CONTENT-4932") || strings.Contains(diagnostics, "Human Author") || strings.Contains(diagnostics, root) {
		t.Fatalf("audit contains note content or absolute vault path: %q", diagnostics)
	}
	lines := strings.Split(strings.TrimSpace(diagnostics), "\n")
	if len(lines) != 2 {
		t.Fatalf("wanted one audit event per successful write, got %q", diagnostics)
	}
	for _, line := range lines {
		var event map[string]string
		if err := json.Unmarshal([]byte(line), &event); err != nil || event["event"] != "vault_write" || event["path"] == "" || event["revision"] == "" {
			t.Fatalf("invalid write audit event: %q (%v)", line, err)
		}
	}
}
