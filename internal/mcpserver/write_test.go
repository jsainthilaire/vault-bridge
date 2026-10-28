package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
	"github.com/jsainthilaire/vault-bridge/internal/vault"
)

type testWriter struct {
	create func(context.Context, string, string) (*vault.WriteResult, error)
	update func(context.Context, string, string, string) (*vault.WriteResult, error)
}

func (w testWriter) Create(ctx context.Context, path, markdown string) (*vault.WriteResult, error) {
	return w.create(ctx, path, markdown)
}

func (w testWriter) Update(ctx context.Context, path, markdown, revision string) (*vault.WriteResult, error) {
	return w.update(ctx, path, markdown, revision)
}

func connectWritableTestServer(t *testing.T, writer Writer, audit io.Writer) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := NewWritable(testReader{}, writer, audit).Connect(ctx, st, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "independent-write-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		serverSession.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		session.Close()
		serverSession.Close()
		cancel()
	})
	return session
}

func TestWriteSchemasRejectInvalidArgumentsWithoutEcho(t *testing.T) {
	const private = "PRIVATE-WRITE-ARGUMENT-3962"
	called := false
	writer := testWriter{
		create: func(context.Context, string, string) (*vault.WriteResult, error) {
			called = true
			return nil, errors.New("must not reach writer")
		},
		update: func(context.Context, string, string, string) (*vault.WriteResult, error) {
			called = true
			return nil, errors.New("must not reach writer")
		},
	}
	var audit bytes.Buffer
	session := connectWritableTestServer(t, writer, &audit)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"document_create", map[string]any{"path": "03-Projects/test/note.md", "markdown": "# New", "project": private}},
		{"document_create", map[string]any{"path": map[string]string{"secret": private}, "markdown": "# New"}},
		{"document_create", map[string]any{"path": "03-Projects/test/note.md", "markdown": map[string]string{"secret": private}}},
		{"document_create", map[string]any{"path": "03-Projects/test/note.md"}},
		{"document_update", map[string]any{"path": "03-Projects/test/note.md", "markdown": "# New"}},
		{"document_update", map[string]any{"path": "03-Projects/test/note.md", "markdown": "# New", "expected_revision": map[string]string{"secret": private}}},
		{"document_update", map[string]any{"path": "03-Projects/test/note.md", "markdown": "# New", "expected_revision": strings.Repeat("a", 64), "project": private}},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := errorCode(t, result); got != "invalid_argument" {
			t.Fatalf("%s: got %s, want invalid_argument", tc.name, got)
		}
		wire, err := json.Marshal(result)
		if err != nil || bytes.Contains(wire, []byte(private)) {
			t.Fatalf("schema failure exposed private argument: %s (%v)", wire, err)
		}
	}
	if called || audit.Len() != 0 {
		t.Fatalf("invalid arguments reached writer or audit: called=%v audit=%q", called, &audit)
	}
}

func TestWriteErrorsRemainCompactAndPrivate(t *testing.T) {
	const private = "PRIVATE-NOTE-/absolute/user/vault/secret-6817"
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"conflict", scope.Err("conflict", private), "conflict"},
		{"permission", scope.Err("permission_denied", private), "permission_denied"},
		{"resource", scope.Err("resource_limit", private), "resource_limit"},
		{"not found", scope.Err("not_found", private), "not_found"},
		{"unexpected", errors.New(private), "internal_error"},
		{"canceled", context.Canceled, "resource_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var audit bytes.Buffer
			writer := testWriter{update: func(context.Context, string, string, string) (*vault.WriteResult, error) {
				return nil, tc.err
			}}
			session := connectWritableTestServer(t, writer, &audit)
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_update", Arguments: map[string]any{"path": "03-Projects/test/note.md", "markdown": private, "expected_revision": strings.Repeat("b", 64)}})
			if err != nil {
				t.Fatal(err)
			}
			if got := errorCode(t, result); got != tc.code {
				t.Fatalf("code = %s, want %s", got, tc.code)
			}
			wire, err := json.Marshal(result)
			if err != nil || bytes.Contains(wire, []byte(private)) || len(wire) > 1024 {
				t.Fatalf("unsafe or excessive error output: %s (%v)", wire, err)
			}
			if audit.Len() != 0 {
				t.Fatalf("failed write emitted a success audit: %q", &audit)
			}
		})
	}
}

type brokenAuditWriter struct{}

func (brokenAuditWriter) Write([]byte) (int, error) { return 0, errors.New("audit unavailable") }

func TestAuditFailureDoesNotMaskCommittedWrite(t *testing.T) {
	writer := testWriter{create: func(context.Context, string, string) (*vault.WriteResult, error) {
		return &vault.WriteResult{Path: "03-Projects/test/new.md", Revision: strings.Repeat("c", 64)}, nil
	}}
	session := connectWritableTestServer(t, writer, brokenAuditWriter{})
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_create", Arguments: map[string]any{"path": "03-Projects/test/new.md", "markdown": "# Committed"}})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("audit failure masked committed success: %#v (%v)", result, err)
	}
}
