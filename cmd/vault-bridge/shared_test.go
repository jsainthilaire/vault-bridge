//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jsainthilaire/vault-bridge/internal/vault"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectSharedCLI(t *testing.T, args ...string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "shared-acceptance", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.CommandContext(ctx, os.Args[0], append([]string{"serve"}, args...)...)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session
}

func TestCLISharedInstallAndVerification(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, yaml := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/yaml=%v", client, yaml), func(t *testing.T) {
				root, config := writableCLIFixture(t)
				// Source must be a sibling of this temporary vault, not inside it.
				repo, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				before := projectHashes(t, root)
				connection := []string{"--target", filepath.Join(root, "03-Projects/vault-bridge")}
				if yaml {
					connection = []string{"--config", config, "--project", "vault-bridge"}
				}
				args := append([]string{"install", repo, "--client", client}, connection...)
				args = append(args, "--shared", "05-Knowledge", "--allow-writes")
				var out, diag bytes.Buffer
				if err := run(context.Background(), args, &out, &diag); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "Verified shared listing and available note retrieval: 05-Knowledge (read and write") {
					t.Fatalf("missing shared verification: %s", &out)
				}
				if !reflect.DeepEqual(before, projectHashes(t, root)) {
					t.Fatal("install verification wrote to vault")
				}
				file := "AGENTS.md"
				if client == "claude" {
					file = "CLAUDE.md"
				}
				instructions, err := os.ReadFile(filepath.Join(repo, file))
				if err != nil || !bytes.Contains(instructions, []byte(`"05-Knowledge": read and write`)) {
					t.Fatalf("missing shared guidance: %v", err)
				}
				var checked bytes.Buffer
				check := append([]string{"config", "check"}, connection...)
				check = append(check, "--shared=05-Knowledge")
				if err := run(context.Background(), check, &checked, &diag); err != nil {
					t.Fatal(err)
				}
				want := 1
				if yaml {
					want = 2
				}
				if !strings.Contains(checked.String(), fmt.Sprintf("(%d shared grants)", want)) {
					t.Fatalf("wrong shared scope: %s", &checked)
				}
			})
		}
	}
}

func TestSharedMCPPermissions(t *testing.T) {
	root, config := writableCLIFixture(t)
	name := "05-Knowledge/Go/cli-shared.md"
	if err := os.WriteFile(filepath.Join(root, name), []byte("# Shared seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--config", config, "--project", "vault-bridge"}
	legacy := connectSharedCLI(t, append(args, "--allow-writes")...)
	var doc vault.Document
	cliWriteCall(t, legacy, "document_read", map[string]any{"path": name}, "", &doc)
	cliWriteCall(t, legacy, "document_update", map[string]any{"path": name, "markdown": "# Blocked", "expected_revision": doc.Revision}, "permission_denied", nil)
	readonly := connectSharedCLI(t, append(args, "--shared", "05-Knowledge")...)
	tools, err := readonly.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "document_create" || tool.Name == "document_update" {
			t.Fatal("read-only shared session exposed writes")
		}
	}
	var list vault.ListResult
	cliWriteCall(t, readonly, "document_list", map[string]any{"section": "shared:05-Knowledge", "limit": 1}, "", &list)
	cliWriteCall(t, readonly, "document_read", map[string]any{"path": name}, "", &doc)
	writable := connectSharedCLI(t, append(args, "--shared", "05-Knowledge/Go", "--allow-writes")...)
	cliWriteCall(t, writable, "document_update", map[string]any{"path": name, "markdown": "# Shared revised", "expected_revision": doc.Revision}, "", nil)
	cliWriteCall(t, writable, "document_update", map[string]any{"path": name, "markdown": "# Stale", "expected_revision": doc.Revision}, "conflict", nil)
	cliWriteCall(t, writable, "document_create", map[string]any{"path": "05-Knowledge/Go/new.md", "markdown": "# Created"}, "", nil)
	cliWriteCall(t, writable, "document_create", map[string]any{"path": "05-Knowledge/Security/no.md", "markdown": "# Forbidden"}, "permission_denied", nil)
	cliWriteCall(t, writable, "document_read", map[string]any{"path": "03-Projects/other-project/index.md"}, "permission_denied", nil)
	cliWriteCall(t, writable, "document_create", map[string]any{"path": "03-Projects/other-project/no.md", "markdown": "# Forbidden"}, "permission_denied", nil)
}

func TestSharedWritersAcrossProcessesUseSameLock(t *testing.T) {
	root, _ := writableCLIFixture(t)
	name := "05-Knowledge/Go/concurrent.md"
	if err := os.WriteFile(filepath.Join(root, name), []byte("# Original"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := connectSharedCLI(t, "--target", filepath.Join(root, "03-Projects/vault-bridge"), "--shared", "05-Knowledge", "--allow-writes")
	b := connectSharedCLI(t, "--target", filepath.Join(root, "03-Projects/other-project"), "--shared", "05-Knowledge/Go", "--allow-writes")
	var doc vault.Document
	cliWriteCall(t, a, "document_read", map[string]any{"path": name}, "", &doc)
	lock, err := os.OpenFile(filepath.Join(root, "05-Knowledge/Go/.vault-bridge-write.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	type response struct {
		result *mcp.CallToolResult
		err    error
	}
	results := make(chan response, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, session := range []*mcp.ClientSession{a, b} {
		go func(i int, s *mcp.ClientSession) {
			r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "document_update", Arguments: map[string]any{"path": name, "markdown": fmt.Sprintf("# Writer %d", i), "expected_revision": doc.Revision}})
			results <- response{r, err}
		}(i, session)
	}
	select {
	case result := <-results:
		t.Fatalf("shared writer bypassed the parent lock: %+v", result)
	case <-time.After(150 * time.Millisecond):
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	winners, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !result.result.IsError {
			winners++
			continue
		}
		if len(result.result.Content) != 1 {
			t.Fatal("unexpected conflict result")
		}
		content, ok := result.result.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(content.Text, `"conflict"`) {
			t.Fatalf("unexpected error: %+v", result.result)
		}
		conflicts++
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
}
