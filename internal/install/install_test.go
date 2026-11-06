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

	"github.com/jsainthilaire/vault-bridge/internal/scaffold"
)

func fixture(t *testing.T, client string) Options {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(root, "Test Vault")
	if err := scaffold.VaultInit(vault); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "Code Project")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "bridge config.yaml")
	data := fmt.Sprintf("version: 1\nvault_root: %q\nprojects:\n  vault-bridge:\n    folder: 03-Projects/vault-bridge\n    shared_read_paths: []\n", vault)
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bridge executable")
	if err := os.WriteFile(binary, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Options{Client: client, Repo: repo, Config: config, Project: "vault-bridge", Binary: binary}
}

func target(o Options) string {
	if o.Client == "codex" {
		return filepath.Join(o.Repo, ".codex", "config.toml")
	}
	return filepath.Join(o.Repo, ".mcp.json")
}

func put(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProjectInstallation(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", client, existing), func(t *testing.T) {
				o := fixture(t, client)
				// Preserve authored content while managing only the selected client's block.
				put(t, filepath.Join(o.Repo, "AGENTS.md"), "Keep these instructions\n")
				put(t, filepath.Join(o.Repo, "CLAUDE.md"), "Keep Claude instructions\n")
				original := ""
				if existing {
					original = "# Authored comment\nmodel = 'custom'\n[mcp_servers]\n[mcp_servers.other]\ncommand = 'other-server'\n"
					if client == "claude" {
						original = `{"custom":{"precise":1234567890123456789},"mcpServers":{"other":{"command":"other-server"}}}`
					}
					put(t, target(o), original)
				}
				result, err := Project(o)
				if err != nil || !result.Changed || result.Path != target(o) {
					t.Fatalf("install = %+v, %v", result, err)
				}
				data := read(t, target(o))
				var doc map[string]any
				key := "mcp_servers"
				if client == "codex" {
					if err := toml.Unmarshal(data, &doc); err != nil {
						t.Fatal(err)
					}
					if !bytes.HasPrefix(data, []byte(original)) {
						t.Fatal("authored TOML was rewritten")
					}
				} else {
					key = "mcpServers"
					if err := json.Unmarshal(data, &doc); err != nil {
						t.Fatal(err)
					}
					if existing && !bytes.Contains(data, []byte("1234567890123456789")) {
						t.Fatal("unrelated JSON number lost precision")
					}
				}
				servers := doc[key].(map[string]any)
				entry := servers["devvault"].(map[string]any)
				if entry["command"] != o.Binary {
					t.Fatalf("wrong executable: %v", entry)
				}
				wantArgs := []any{"serve", "--config", o.Config, "--project", o.Project, "--transport", "stdio"}
				if !reflect.DeepEqual(entry["args"], wantArgs) {
					t.Fatalf("wrong project binding: %v", entry["args"])
				}
				if existing && servers["other"].(map[string]any)["command"] != "other-server" {
					t.Fatal("lost unrelated MCP server")
				}
				info, _ := os.Stat(target(o))
				wantMode := os.FileMode(0o600)
				if existing {
					wantMode = 0o640
				}
				if info.Mode().Perm() != wantMode {
					t.Fatalf("mode %o, want %o", info.Mode().Perm(), wantMode)
				}
				result, err = Project(o)
				if err != nil || result.Changed || !bytes.Equal(data, read(t, target(o))) {
					t.Fatalf("repeat install changed settings: %+v %v", result, err)
				}
				for file, authored := range map[string]string{"AGENTS.md": "Keep these instructions\n", "CLAUDE.md": "Keep Claude instructions\n"} {
					got := string(read(t, filepath.Join(o.Repo, file)))
					if file == instructionName(client) {
						if !strings.HasPrefix(got, authored) || strings.Count(got, instructionStart) != 1 || !strings.Contains(got, "`vault-bridge`") {
							t.Fatal("missing managed instructions or lost authored content")
						}
					} else if got != authored {
						t.Fatal("modified the other client's instructions")
					}
				}
			})
		}
	}
}

func TestInvalidSettingsArePreserved(t *testing.T) {
	for _, tc := range []struct{ client, name, text string }{
		{"codex", "malformed", "model = ["},
		{"codex", "conflict", "[mcp_servers.devvault]\ncommand = 'another-server'\n"},
		{"codex", "scalar servers", "mcp_servers = 'invalid'\n"},
		{"codex", "inline servers", "mcp_servers = {other = {command = 'other'}}\n"},
		{"codex", "duplicate", "model='one'\nmodel='two'\n"},
		{"claude", "malformed", "{"},
		{"claude", "conflict", `{"mcpServers":{"devvault":{"command":"another-server"}}}`},
		{"claude", "null", "null"},
		{"claude", "null servers", `{"mcpServers":null}`},
		{"claude", "scalar servers", `{"mcpServers":5}`},
		{"claude", "duplicate key", `{"custom":1,"custom":2}`},
		{"claude", "duplicate server", `{"mcpServers":{"other":{},"other":{}}}`},
		{"claude", "trailing data", "{} {}"},
	} {
		t.Run(tc.client+"/"+tc.name, func(t *testing.T) {
			o := fixture(t, tc.client)
			put(t, target(o), tc.text)
			if _, err := Project(o); err == nil {
				t.Fatal("expected error")
			}
			if string(read(t, target(o))) != tc.text {
				t.Fatal("changed invalid or conflicting configuration")
			}
			if _, err := os.Stat(filepath.Join(o.Repo, instructionName(o.Client))); !os.IsNotExist(err) {
				t.Fatalf("changed instructions despite conflicting settings: %v", err)
			}
		})
	}
}

func TestRejectUnsafeTargets(t *testing.T) {
	for _, tc := range []string{"repo symlink", "ancestor symlink", "directory symlink", "file symlink", "special file", "oversized"} {
		t.Run(tc, func(t *testing.T) {
			o := fixture(t, "codex")
			outside := filepath.Join(filepath.Dir(o.Repo), "outside")
			put(t, filepath.Join(outside, "sentinel"), "untouched")
			var err error
			switch tc {
			case "repo symlink":
				link := o.Repo + "-link"
				err = os.Symlink(o.Repo, link)
				o.Repo = link
			case "ancestor symlink":
				link := o.Repo + "-link"
				err = os.Symlink(filepath.Dir(o.Repo), link)
				o.Repo = filepath.Join(link, filepath.Base(o.Repo))
			case "directory symlink":
				err = os.Symlink(outside, filepath.Join(o.Repo, ".codex"))
			case "file symlink":
				put(t, target(o), "")
				if err := os.Remove(target(o)); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(filepath.Join(outside, "sentinel"), target(o))
			case "special file":
				err = os.MkdirAll(target(o), 0o700)
			case "oversized":
				put(t, target(o), strings.Repeat("#", maxConfigBytes+1))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Project(o); err == nil {
				t.Fatal("expected unsafe target rejection")
			}
			if string(read(t, filepath.Join(outside, "sentinel"))) != "untouched" {
				t.Fatal("modified symlink destination")
			}
		})
	}
}

func TestInvalidInputsDoNotInstall(t *testing.T) {
	for _, tc := range []string{"client", "project", "config", "binary", "missing repo"} {
		t.Run(tc, func(t *testing.T) {
			o := fixture(t, "codex")
			switch tc {
			case "client":
				o.Client = "other"
			case "project":
				o.Project = "unmapped"
			case "config":
				o.Config = "relative.yaml"
			case "binary":
				o.Binary = filepath.Join(o.Repo, "missing")
			case "missing repo":
				o.Repo = filepath.Join(o.Repo, "missing")
			}
			if _, err := Project(o); err == nil {
				t.Fatal("expected validation error")
			}
			if _, err := os.Lstat(target(o)); !os.IsNotExist(err) {
				t.Fatalf("created config on invalid input: %v", err)
			}
		})
	}
}

func TestProjectInstallationFromTarget(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			o := fixture(t, client)
			absoluteTarget := filepath.Join(filepath.Dir(o.Config), "Test Vault", "03-Projects", "vault-bridge")
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			o.Target, err = filepath.Rel(cwd, absoluteTarget)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(o.Config); err != nil {
				t.Fatal(err)
			}
			o.Config, o.Project = "", ""
			result, err := Project(o)
			if err != nil || !result.Changed || result.Project != "vault-bridge" {
				t.Fatalf("target install: %+v %v", result, err)
			}
			data := read(t, target(o))
			var doc map[string]any
			key := "mcp_servers"
			if client == "codex" {
				err = toml.Unmarshal(data, &doc)
			} else {
				key = "mcpServers"
				err = json.Unmarshal(data, &doc)
			}
			if err != nil {
				t.Fatal(err)
			}
			entry := doc[key].(map[string]any)["devvault"].(map[string]any)
			want := []any{"serve", "--target", absoluteTarget, "--project", "vault-bridge", "--transport", "stdio"}
			if !reflect.DeepEqual(entry["args"], want) {
				t.Fatalf("launch arguments: %v", entry["args"])
			}
			result, err = Project(o)
			if err != nil || result.Changed || !bytes.Equal(data, read(t, target(o))) {
				t.Fatalf("repeat target install: %+v %v", result, err)
			}
		})
	}
}

func TestTargetInstallationRejectsConflictingOrUnsafeInput(t *testing.T) {
	for _, name := range []string{"config and target", "vault root", "symlink", "conflicting server"} {
		t.Run(name, func(t *testing.T) {
			o := fixture(t, "claude")
			o.Target = filepath.Join(filepath.Dir(o.Config), "Test Vault", "03-Projects", "vault-bridge")
			if name != "config and target" {
				o.Config, o.Project = "", ""
			}
			switch name {
			case "vault root":
				o.Target = filepath.Dir(filepath.Dir(o.Target))
			case "symlink":
				link := filepath.Join(filepath.Dir(o.Target), "alias")
				if err := os.Symlink(o.Target, link); err != nil {
					t.Fatal(err)
				}
				o.Target = link
			case "conflicting server":
				put(t, target(o), `{"mcpServers":{"devvault":{"command":"keep-me"}}}`)
			}
			if _, err := Project(o); err == nil {
				t.Fatal("expected rejected installation")
			}
			if name == "conflicting server" {
				if !bytes.Contains(read(t, target(o)), []byte("keep-me")) {
					t.Fatal("replaced conflicting connection")
				}
			} else if _, err := os.Lstat(target(o)); !os.IsNotExist(err) {
				t.Fatalf("created client config for invalid target: %v", err)
			}
		})
	}
}
