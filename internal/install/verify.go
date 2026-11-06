package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Verify starts the saved launch command through the SDK and makes read-only
// calls. It checks tool availability, never exercises writes against real notes.
// A missing index is reported separately for compatibility with empty projects.
// This is not a connection through Codex or Claude or a check of client trust.
func Verify(ctx context.Context, installed Result) (indexRead bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, installed.Binary, installed.Args...)
	client := mcp.NewClient(&mcp.Implementation{Name: "vault-bridge-install-check", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		return false, fmt.Errorf("MCP stdio startup: %w", err)
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("MCP tool discovery: %w", err)
	}
	names := make(map[string]bool)
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"project_context", "document_read", "document_list", "document_search", "feature_list", "decision_list"} {
		if !names[name] {
			return false, fmt.Errorf("MCP tool missing: %s", name)
		}
	}
	for _, name := range []string{"document_create", "document_update"} {
		if names[name] != installed.AllowWrites {
			return false, fmt.Errorf("MCP tool %s does not match requested write access", name)
		}
	}
	// Shared checks precede the optional index so projects without an index
	// still verify all selected shared selectors. Never probe writes in real vaults.
	for _, shared := range installed.SharedPaths {
		var listing struct{ Documents []struct{ Path string } }
		code, err := verificationCall(ctx, session, "document_list", map[string]any{"section": "shared:" + shared, "limit": 1}, &listing)
		if err != nil {
			return false, err
		}
		if code != "" {
			return false, fmt.Errorf("shared document_list failed for %s (%s)", shared, code)
		}
		if len(listing.Documents) > 0 {
			name := listing.Documents[0].Path
			var document struct{ Path string }
			code, err := verificationCall(ctx, session, "document_read", map[string]any{"path": name, "max_chars": 1000}, &document)
			if err != nil {
				return false, err
			}
			if code != "" || document.Path != name {
				return false, fmt.Errorf("shared document_read failed for %s", shared)
			}
		}
	}
	var identity struct{ Project, Path string }
	code, err := verificationCall(ctx, session, "project_context", nil, &identity)
	if err != nil {
		return false, err
	}
	if code == "not_found" {
		return false, nil
	}
	if code != "" {
		return false, fmt.Errorf("project_context failed (%s)", code)
	}
	if identity.Project != installed.Project || identity.Path != installed.IndexPath {
		return false, errors.New("MCP project identity does not match the installed project")
	}
	var document struct{ Path string }
	code, err = verificationCall(ctx, session, "document_read", map[string]any{"path": installed.IndexPath, "max_chars": 1000}, &document)
	if err != nil {
		return false, err
	}
	if code != "" {
		return false, fmt.Errorf("document_read failed (%s)", code)
	}
	if document.Path != installed.IndexPath {
		return false, errors.New("MCP index path does not match the installed project")
	}
	return true, nil
}

func verificationCall(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any, out any) (string, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("%s protocol error: %w", name, err)
	}
	if result.IsError {
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				var failure struct{ Error struct{ Code string } }
				if json.Unmarshal([]byte(text.Text), &failure) == nil {
					// Do not copy arbitrary server content into diagnostics.
					switch failure.Error.Code {
					case "not_found", "permission_denied", "invalid_argument", "resource_limit", "internal_error":
						return failure.Error.Code, nil
					}
				}
			}
		}
		return "", fmt.Errorf("%s returned an unrecognized tool error", name)
	}
	if result.StructuredContent == nil {
		return "", fmt.Errorf("%s returned no structured content", name)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err == nil {
		err = json.Unmarshal(data, out)
	}
	if err != nil {
		return "", fmt.Errorf("%s returned invalid structured content", name)
	}
	return "", nil
}
