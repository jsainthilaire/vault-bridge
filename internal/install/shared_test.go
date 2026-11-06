package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestInstallAddsSharedGrantsAndPreservesPreviousGrants(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, global := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/global=%v", client, global), func(t *testing.T) {
				o := fixture(t, client)
				path := target(o)
				if global {
					o, path, _ = globalFixture(t, client)
				}
				o.AllowWrites = true
				if _, err := Project(o); err != nil {
					t.Fatal(err)
				}
				before := read(t, path)
				o.SharedPaths = []string{"05-Knowledge/Go"}
				result, err := Project(o)
				if err != nil || !result.Changed {
					t.Fatalf("add shared: %+v %v", result, err)
				}
				if !reflect.DeepEqual(result.SharedWritablePaths, o.SharedPaths) {
					t.Fatalf("wrong write grants: %v", result.SharedWritablePaths)
				}
				withGo := read(t, path)
				if bytes.Equal(before, withGo) || !bytes.Contains(withGo, []byte("--shared")) {
					t.Fatal("grant not persisted")
				}
				o.SharedPaths = []string{"05-Knowledge"}
				result, err = Project(o)
				if err != nil || !result.Changed || !reflect.DeepEqual(result.SharedPaths, []string{"05-Knowledge", "05-Knowledge/Go"}) {
					t.Fatalf("root and previous grants: %+v %v", result, err)
				}
				if !global {
					instructions := string(read(t, result.InstructionsPath))
					for _, want := range []string{"shared:05-Knowledge", "read and write", "reusable cross-project knowledge", "expected_revision"} {
						if !strings.Contains(instructions, want) {
							t.Fatalf("missing %q in instructions", want)
						}
					}
					if strings.Contains(instructions, "Shared knowledge remains read-only") {
						t.Fatal("stale shared read-only guidance")
					}
				}
				installed := read(t, path)
				o.SharedPaths = nil
				result, err = Project(o)
				if err != nil || result.Changed || result.InstructionsChanged || !bytes.Equal(installed, read(t, path)) || len(result.SharedPaths) != 2 {
					t.Fatalf("omission revoked or changed grants: %+v %v", result, err)
				}
				o.SharedPaths = []string{"05-Knowledge", "05-Knowledge"}
				result, err = Project(o)
				if err != nil || result.Changed {
					t.Fatalf("repeat duplicated grants: %+v %v", result, err)
				}
				o.AllowWrites = false
				if _, err := Project(o); err == nil || !bytes.Equal(installed, read(t, path)) {
					t.Fatal("shared flags changed write permissions")
				}
			})
		}
	}
}

func TestCodexSharedAppendPreservesTOMLLayouts(t *testing.T) {
	for _, layout := range []string{"table", "quoted", "dotted", "inline"} {
		t.Run(layout, func(t *testing.T) {
			o := fixture(t, "codex")
			o.AllowWrites = true
			result, err := Project(o)
			if err != nil {
				t.Fatal(err)
			}
			argsJSON, _ := json.Marshal(result.Args)
			args := string(argsJSON)
			if layout != "inline" {
				args = "[\n# Keep inside array\n" + args[1:len(args)-1] + ", # Keep after last element\n]"
			}
			body := fmt.Sprintf("command = %q\nargs = %s\nstartup_timeout_sec = 10\ntool_timeout_sec = 30\n", o.Binary, args)
			var original string
			switch layout {
			case "table":
				original = "[mcp_servers.devvault]\n" + body
			case "quoted":
				original = "[mcp_servers.'devvault']\n" + body
			case "dotted":
				original = "[mcp_servers]\n" + strings.ReplaceAll(strings.ReplaceAll(body, "command =", "devvault.command ="), "args =", "devvault.args =")
				original = strings.ReplaceAll(original, "startup_timeout_sec =", "devvault.startup_timeout_sec =")
				original = strings.ReplaceAll(original, "tool_timeout_sec =", "devvault.tool_timeout_sec =")
			case "inline":
				original = fmt.Sprintf("mcp_servers = { devvault = { command = %q, args = %s, startup_timeout_sec = 10, tool_timeout_sec = 30, tools = { document_update = { approval_mode = 'approve' } } } }\n", o.Binary, args)
			}
			original = "# User comment\nmodel = 'custom'\n" + original
			if layout != "inline" {
				original += "[mcp_servers.devvault.tools.document_update]\napproval_mode = 'approve'\n"
			}
			put(t, target(o), original)
			o.SharedPaths = []string{"05-Knowledge/Go"}
			if _, err := Project(o); err != nil {
				t.Fatal(err)
			}
			after := string(read(t, target(o)))
			added := `, "--shared","05-Knowledge/Go"`
			if strings.Count(after, added) != 1 || strings.Replace(after, added, "", 1) != original {
				t.Fatalf("rewrote existing TOML bytes:\n%s", after)
			}
			var doc map[string]any
			if err := toml.Unmarshal([]byte(after), &doc); err != nil {
				t.Fatal(err)
			}
			entry := doc["mcp_servers"].(map[string]any)["devvault"].(map[string]any)
			parsed, _ := stringArgs(entry["args"])
			if !reflect.DeepEqual(parsed, append(result.Args, "--shared", "05-Knowledge/Go")) {
				t.Fatalf("wrong updated args: %v", parsed)
			}
		})
	}
}

func TestInvalidSharedInstallLeavesFilesUntouched(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			o := fixture(t, client)
			if _, err := Project(o); err != nil {
				t.Fatal(err)
			}
			before := treeSnapshot(t, o.Repo)
			o.SharedPaths = []string{"03-Projects/vault-bridge"}
			if _, err := Project(o); err == nil {
				t.Fatal("accepted unrelated shared grant")
			}
			if !reflect.DeepEqual(before, treeSnapshot(t, o.Repo)) {
				t.Fatal("invalid grant changed client files")
			}
		})
	}
}

func TestBulkSharedGrantsAndLegacyInstructions(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			o, destination := matchingFixture(t, client)
			o.AllowWrites = true
			if _, err := Projects(o); err != nil {
				t.Fatal(err)
			}
			o.SharedPaths = []string{"05-Knowledge"}
			result, err := Projects(o)
			if err != nil {
				t.Fatal(err)
			}
			for _, project := range result.Projects {
				if len(project.SharedWritablePaths) != 1 || !strings.Contains(string(read(t, project.InstructionsPath)), "shared:05-Knowledge") {
					t.Fatalf("missing bulk grant: %+v", project)
				}
			}
			before := read(t, destination)
			o.SharedPaths = nil
			result, err = Projects(o)
			if err != nil || !bytes.Equal(before, read(t, destination)) {
				t.Fatalf("bulk grants not retained: %v", err)
			}
		})
	}
	o := fixture(t, "codex")
	data := string(read(t, o.Config))
	data = strings.Replace(data, "shared_read_paths: []", "shared_read_paths: [05-Knowledge/Go]", 1)
	put(t, o.Config, data)
	o.AllowWrites = true
	result, err := Project(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SharedPaths) != 1 || len(result.SharedWritablePaths) != 0 {
		t.Fatal("legacy grant elevated")
	}
	if !strings.Contains(string(read(t, result.InstructionsPath)), `"05-Knowledge/Go": read-only`) {
		t.Fatal("legacy grant not described as read-only")
	}
}

func TestSharedBatchFailurePreventsPartialConfiguration(t *testing.T) {
	o, destination := matchingFixture(t, "codex")
	o.SharedPaths = []string{"05-Knowledge"}
	// A later conflicting entry must not install earlier valid shared grants.
	put(t, destination, "[mcp_servers.devvault-vault-bridge]\ncommand = 'other-server'\n")
	before := treeSnapshot(t, o.SourceDir)
	configBefore := read(t, destination)
	if _, err := Projects(o); err == nil {
		t.Fatal("accepted conflicting batch")
	}
	if !bytes.Equal(configBefore, read(t, destination)) || !reflect.DeepEqual(before, treeSnapshot(t, o.SourceDir)) {
		t.Fatal("partial shared installation")
	}
	if _, err := os.Stat(filepath.Join(o.SourceDir, "alpha/AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("wrote early instructions")
	}
}
