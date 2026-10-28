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

func TestGlobalConfigLocations(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "test-home")
	custom := filepath.Join(home, "custom")
	for _, tc := range []struct {
		name, client, codexHome, claudeDir, want string
		wantError                                bool
	}{
		{"Codex default", "codex", "", "", filepath.Join(home, ".codex/config.toml"), false},
		{"Claude default", "claude", "", "", filepath.Join(home, ".claude.json"), false},
		{"Codex override", "codex", custom, "", filepath.Join(custom, "config.toml"), false},
		{"Claude override", "claude", "", custom, filepath.Join(custom, ".claude.json"), false},
		{"Codex ignores Claude override", "codex", "", custom, filepath.Join(home, ".codex/config.toml"), false},
		{"Claude ignores Codex override", "claude", custom, "", filepath.Join(home, ".claude.json"), false},
		{"relative Codex override", "codex", "relative", "", "", true},
		{"relative Claude override", "claude", "", "relative", "", true},
		{"literal tilde", "codex", "~/.codex", "", "", true},
		{"unknown client", "other", "", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := configPathForHome(tc.client, home, tc.codexHome, tc.claudeDir)
			if (err != nil) != tc.wantError || got != tc.want {
				t.Fatalf("location = %q, %v; want %q, error %v", got, err, tc.want, tc.wantError)
			}
		})
	}
}

func globalFixture(t *testing.T, client string) (Options, string, string) {
	t.Helper()
	o := fixture(t, client)
	repo := o.Repo
	o.Global, o.Repo = true, ""
	dir := filepath.Join(filepath.Dir(repo), "User Config", client)
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	name := ".claude.json"
	if client == "codex" {
		name = "config.toml"
	}
	return o, filepath.Join(dir, name), repo
}

func TestGlobalInstallation(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, mode := range []string{"config", "target"} {
			for _, existing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/existing=%v", client, mode, existing), func(t *testing.T) {
					o, destination, repo := globalFixture(t, client)
					wantArgs := []any{"serve", "--config", o.Config, "--project", o.Project, "--transport", "stdio"}
					if mode == "target" {
						o.Target = filepath.Join(filepath.Dir(o.Config), "Test Vault", "03-Projects/vault-bridge")
						wantArgs = []any{"serve", "--target", o.Target, "--project", o.Project, "--transport", "stdio"}
						o.Config, o.Project = "", ""
					}
					original := ""
					if existing {
						original = "# User configuration\nmodel = 'custom-model'\n[mcp_servers.other]\ncommand = 'other-server'\n"
						if client == "claude" {
							original = `{"accountState":"preserve-me","precise":1234567890123456789,"projects":{"/some/code":{"hasTrustDialogAccepted":true,"mcpServers":{"local":{"command":"local-server"}}}},"mcpServers":{"other":{"command":"other-server"}}}`
						}
						put(t, destination, original)
					}
					result, err := Project(o)
					if err != nil || !result.Changed || result.Path != destination || result.Project != "vault-bridge" {
						t.Fatalf("global install: %+v %v", result, err)
					}
					data := read(t, destination)
					var doc map[string]any
					key := "mcp_servers"
					if client == "codex" {
						err = toml.Unmarshal(data, &doc)
						if !bytes.HasPrefix(data, []byte(original)) {
							t.Fatal("authored global TOML was rewritten")
						}
					} else {
						key = "mcpServers"
						err = json.Unmarshal(data, &doc)
						if existing && (doc["accountState"] != "preserve-me" || !bytes.Contains(data, []byte("1234567890123456789")) || !bytes.Contains(data, []byte("local-server"))) {
							t.Fatal("lost global Claude account or project settings")
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					servers := doc[key].(map[string]any)
					entry := servers["devvault"].(map[string]any)
					if entry["command"] != o.Binary || !reflect.DeepEqual(entry["args"], wantArgs) {
						t.Fatalf("global launch scope changed: %v", entry)
					}
					if existing && servers["other"].(map[string]any)["command"] != "other-server" {
						t.Fatal("lost another global server")
					}
					info, err := os.Stat(destination)
					if err != nil {
						t.Fatal(err)
					}
					wantMode := os.FileMode(0o600)
					if existing {
						wantMode = 0o640
					}
					if info.Mode().Perm() != wantMode {
						t.Fatalf("mode = %o, want %o", info.Mode().Perm(), wantMode)
					}
					result, err = Project(o)
					if err != nil || result.Changed || !bytes.Equal(data, read(t, destination)) {
						t.Fatalf("repeat global install: %+v %v", result, err)
					}
					for _, file := range []string{".mcp.json", ".codex/config.toml", "AGENTS.md", "CLAUDE.md"} {
						if _, err := os.Lstat(filepath.Join(repo, file)); !os.IsNotExist(err) {
							t.Fatalf("global install modified code repository: %s: %v", file, err)
						}
					}
					for _, file := range []string{"AGENTS.md", "CLAUDE.md"} {
						if _, err := os.Lstat(filepath.Join(filepath.Dir(destination), file)); !os.IsNotExist(err) {
							t.Fatalf("global install wrote instructions: %s: %v", file, err)
						}
					}
				})
			}
		}
	}
}

func TestGlobalInstallationRejectsUnsafeSettings(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, problem := range []string{"conflict", "malformed", "oversized", "file symlink", "parent symlink", "repo"} {
			t.Run(client+"/"+problem, func(t *testing.T) {
				o, destination, repo := globalFixture(t, client)
				original := ""
				var sentinel string
				switch problem {
				case "conflict":
					original = "[mcp_servers.devvault]\ncommand = 'keep-me'\n"
					if client == "claude" {
						original = `{"mcpServers":{"devvault":{"command":"keep-me"}}}`
					}
					put(t, destination, original)
				case "malformed":
					original = "["
					put(t, destination, original)
				case "oversized":
					original = strings.Repeat("#", maxConfigBytes+1)
					put(t, destination, original)
				case "file symlink":
					sentinel = filepath.Join(filepath.Dir(repo), "sentinel")
					put(t, sentinel, "untouched")
					if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(sentinel, destination); err != nil {
						t.Fatal(err)
					}
				case "parent symlink":
					outside := filepath.Join(filepath.Dir(repo), "outside")
					sentinel = filepath.Join(outside, filepath.Base(destination))
					put(t, sentinel, "untouched")
					if err := os.MkdirAll(filepath.Dir(filepath.Dir(destination)), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Dir(destination)); err != nil {
						t.Fatal(err)
					}
				case "repo":
					o.Repo = repo
				}
				if _, err := Project(o); err == nil {
					t.Fatal("expected rejected global installation")
				}
				if original != "" && string(read(t, destination)) != original {
					t.Fatal("modified invalid or conflicting global config")
				}
				if sentinel != "" && string(read(t, sentinel)) != "untouched" {
					t.Fatal("modified a symlink destination")
				}
			})
		}
	}
}

func TestLocalInstallIgnoresGlobalLocations(t *testing.T) {
	o := fixture(t, "claude")
	t.Setenv("CODEX_HOME", "invalid-relative-path")
	t.Setenv("CLAUDE_CONFIG_DIR", "invalid-relative-path")
	result, err := Project(o)
	if err != nil || result.Path != target(o) {
		t.Fatalf("local install used global location: %+v %v", result, err)
	}
}
