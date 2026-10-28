package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
	"github.com/jsainthilaire/vault-bridge/internal/vault"
)

type testReader struct {
	read func(context.Context, string, int) (*vault.Document, error)
}

func (r testReader) Read(ctx context.Context, name string, maxChars int) (*vault.Document, error) {
	return r.read(ctx, name, maxChars)
}
func (testReader) ProjectContext(context.Context) (*vault.ProjectContext, error) {
	return &vault.ProjectContext{Project: "test", Paths: []string{}, Links: []string{}}, nil
}
func (testReader) List(context.Context, vault.ListOptions) (*vault.ListResult, error) {
	return &vault.ListResult{Documents: []vault.DocumentInfo{}}, nil
}
func (testReader) Search(context.Context, vault.SearchOptions) (*vault.SearchResult, error) {
	return &vault.SearchResult{Matches: []vault.SearchMatch{}}, nil
}
func (testReader) Decisions(context.Context, int) (*vault.ListResult, error) {
	return &vault.ListResult{Documents: []vault.DocumentInfo{}}, nil
}
func (testReader) Features(context.Context, string, int) (*vault.ListResult, error) {
	return &vault.ListResult{Documents: []vault.DocumentInfo{}}, nil
}

func connectTestServer(t *testing.T, reader Reader) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ct, st := mcp.NewInMemoryTransports()
	serverSession, err := New(reader).Connect(ctx, st, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "independent-test", Version: "1"}, nil)
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

func errorCode(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("expected one application error text block, got %#v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text error, got %T", result.Content[0])
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text.Text), &e); err != nil || e.Error.Code == "" {
		t.Fatalf("invalid application error: %q (%v)", text.Text, err)
	}
	if result.StructuredContent != nil {
		t.Fatal("failed tool returned success structuredContent")
	}
	return e.Error.Code
}

func TestToolErrorsKeepPrivateDetailsOutOfProtocol(t *testing.T) {
	const private = "PRIVATE-NOTE-CONTENTS-/absolute/user/path-9731"
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"invalid_argument", scope.Err("invalid_argument", private), "invalid_argument"},
		{"not_found", scope.Err("not_found", private), "not_found"},
		{"permission_denied", scope.Err("permission_denied", private), "permission_denied"},
		{"resource_limit", scope.Err("resource_limit", private), "resource_limit"},
		{"unexpected", errors.New(private), "internal_error"},
		{"unknown code", scope.Err("unrecognized", private), "internal_error"},
		{"canceled", context.Canceled, "resource_limit"},
		{"deadline", context.DeadlineExceeded, "resource_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := connectTestServer(t, testReader{read: func(context.Context, string, int) (*vault.Document, error) { return nil, tc.err }})
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_read", Arguments: map[string]any{"path": "03-Projects/test/index.md"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := errorCode(t, result); got != tc.code {
				t.Fatalf("error code = %q, want %q", got, tc.code)
			}
			wire, err := json.Marshal(result)
			if err != nil || strings.Contains(string(wire), private) {
				t.Fatalf("error exposed private data: %s (%v)", wire, err)
			}
		})
	}
}

func TestInvalidToolArgumentsAreRejectedWithoutEcho(t *testing.T) {
	const private = "PRIVATE-ARGUMENT-5791"
	called := false
	session := connectTestServer(t, testReader{read: func(context.Context, string, int) (*vault.Document, error) {
		called = true
		return nil, errors.New("should not reach store")
	}})
	for _, args := range []map[string]any{
		{"path": map[string]any{"private": private}},
		{"path": "03-Projects/test/index.md", "project": private},
		{"path": "03-Projects/test/index.md", "max_chars": private},
		{},
	} {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_read", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if code := errorCode(t, result); code != "invalid_argument" {
			t.Fatalf("error code = %q", code)
		}
		wire, err := json.Marshal(result)
		if err != nil || strings.Contains(string(wire), private) {
			t.Fatalf("schema error exposed argument: %s (%v)", wire, err)
		}
	}
	if called {
		t.Fatal("invalid schema reached the store")
	}
}

func TestOversizedAndUnencodableToolOutputsFailSafely(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  *vault.Document
		code string
	}{
		{"oversized", &vault.Document{Path: "03-Projects/test/index.md", Markdown: strings.Repeat("x", MaxOutputBytes+1), Frontmatter: map[string]any{}}, "resource_limit"},
		{"unencodable metadata", &vault.Document{Path: "03-Projects/test/index.md", Frontmatter: map[string]any{"private": make(chan int)}}, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := connectTestServer(t, testReader{read: func(context.Context, string, int) (*vault.Document, error) { return tc.doc, nil }})
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "document_read", Arguments: map[string]any{"path": "03-Projects/test/index.md"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := errorCode(t, result); got != tc.code {
				t.Fatalf("error code = %q, want %q", got, tc.code)
			}
			wire, err := json.Marshal(result)
			if err != nil || len(wire) > 1024 {
				t.Fatalf("failure output is not compact: %d bytes (%v)", len(wire), err)
			}
		})
	}
}

func TestClientCancellationReachesReader(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	session := connectTestServer(t, testReader{read: func(ctx context.Context, _ string, _ int) (*vault.Document, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "document_read", Arguments: map[string]any{"path": "03-Projects/test/index.md"}})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("reader was not called")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CallTool error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client call did not cancel")
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach server reader")
	}
}
