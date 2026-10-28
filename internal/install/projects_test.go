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

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

func projectsFixture(t *testing.T, client string) (Options, string) {
	t.Helper()
	o, destination, _ := globalFixture(t, client)
	o.ProjectsDir = filepath.Join(filepath.Dir(o.Config), "Test Vault", "03-Projects")
	o.Config, o.Project = "", ""
	for _, id := range []string{"zeta", "alpha"} {
		if err := os.Mkdir(filepath.Join(o.ProjectsDir, id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return o, destination
}

func projectServers(t *testing.T, client string, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	key := "mcpServers"
	var err error
	if client == "codex" {
		key = "mcp_servers"
		err = toml.Unmarshal(data, &doc)
	} else {
		err = json.Unmarshal(data, &doc)
	}
	if err != nil {
		t.Fatal(err)
	}
	return doc[key].(map[string]any)
}

func TestProjectsInstallationPreservesSettingsAndScopes(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, writable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/writes=%v", client, writable), func(t *testing.T) {
				o, destination := projectsFixture(t, client)
				o.AllowWrites = writable
				original := "# Keep my settings\nmodel = 'custom'\n[mcp_servers.devvault]\ncommand = 'existing-project'\n[mcp_servers.other]\ncommand = 'other-server'\n"
				if client == "claude" {
					original = `{"accountState":"keep","precise":1234567890123456789,"mcpServers":{"devvault":{"command":"existing-project"},"other":{"command":"other-server"}}}`
				}
				put(t, destination, original)
				if err := os.Mkdir(filepath.Join(o.ProjectsDir, ".hidden"), 0o700); err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(o.ProjectsDir, "README.txt"), "not a project")
				note := filepath.Join(o.ProjectsDir, "vault-bridge", "index.md")
				beforeNote := read(t, note)
				result, err := Projects(o)
				if err != nil {
					t.Fatalf("install projects: %v", err)
				}
				if result.Path != destination || len(result.Projects) != 3 {
					t.Fatalf("batch result: %+v", result)
				}
				data := read(t, destination)
				servers := projectServers(t, client, data)
				if len(servers) != 5 || servers["devvault"].(map[string]any)["command"] != "existing-project" || servers["other"].(map[string]any)["command"] != "other-server" {
					t.Fatalf("lost existing settings or registered wrong folders: %v", servers)
				}
				if client == "codex" && !bytes.HasPrefix(data, []byte(original)) {
					t.Fatal("authored Codex configuration changed")
				}
				if client == "claude" && (!bytes.Contains(data, []byte("1234567890123456789")) || !bytes.Contains(data, []byte(`"accountState": "keep"`))) {
					t.Fatal("unrelated Claude values changed")
				}
				for i, id := range []string{"alpha", "vault-bridge", "zeta"} {
					project := result.Projects[i]
					if project.Project != id || project.Path != destination || !project.Changed {
						t.Fatalf("project result %d: %+v", i, project)
					}
					entry := servers["devvault-"+id].(map[string]any)
					want := []any{"serve", "--target", filepath.Join(o.ProjectsDir, id), "--project", id, "--transport", "stdio"}
					if writable {
						want = append(want, "--allow-writes")
					}
					if entry["command"] != o.Binary || !reflect.DeepEqual(entry["args"], want) {
						t.Fatalf("%s launch scope: %v", id, entry)
					}
					scope, err := config.FromTarget(entry["args"].([]any)[2].(string), id)
					if err != nil || scope.ProjectID != id || len(scope.SharedReadPaths) != 0 {
						t.Fatalf("%s scope: %+v; %v", id, scope, err)
					}
				}
				if !bytes.Equal(beforeNote, read(t, note)) {
					t.Fatal("batch setup changed vault notes")
				}
				info, err := os.Stat(destination)
				if err != nil || info.Mode().Perm() != 0o640 {
					t.Fatalf("lost existing config permissions: %v, %v", info, err)
				}
				result, err = Projects(o)
				if err != nil || !bytes.Equal(data, read(t, destination)) {
					t.Fatalf("repeat batch changed config: %+v; %v", result, err)
				}
				for _, project := range result.Projects {
					if project.Changed {
						t.Fatalf("repeat batch reported change: %+v", project)
					}
				}
				// Adding a project preserves existing entries and identifies only the new one.
				if err := os.Mkdir(filepath.Join(o.ProjectsDir, "new-project"), 0o700); err != nil {
					t.Fatal(err)
				}
				result, err = Projects(o)
				if err != nil || len(result.Projects) != 4 {
					t.Fatalf("extend batch: %+v; %v", result, err)
				}
				for _, project := range result.Projects {
					if project.Changed != (project.Project == "new-project") {
						t.Fatalf("incorrect change status after adding a project: %+v", project)
					}
				}
			})
		}
	}
}

func TestProjectsInstallationIsAtomicOnFailure(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, problem := range []string{"late conflict", "invalid folder", "symlink folder", "symlink file", "symlink parent", "malformed settings", "oversized settings"} {
			t.Run(client+"/"+problem, func(t *testing.T) {
				o, destination := projectsFixture(t, client)
				original := "# Keep settings\nmodel = 'custom'\n"
				if client == "claude" {
					original = `{"accountState":"keep"}`
				}
				switch problem {
				case "late conflict":
					original = "[mcp_servers.devvault-zeta]\ncommand = 'keep-me'\n"
					if client == "claude" {
						original = `{"mcpServers":{"devvault-zeta":{"command":"keep-me"}}}`
					}
				case "invalid folder":
					if err := os.Mkdir(filepath.Join(o.ProjectsDir, "zz_INVALID"), 0o700); err != nil {
						t.Fatal(err)
					}
				case "symlink folder", "symlink file":
					target := filepath.Join(o.ProjectsDir, "alpha")
					if problem == "symlink file" {
						target = filepath.Join(o.ProjectsDir, "index.md")
					}
					if err := os.Symlink(target, filepath.Join(o.ProjectsDir, "zz-linked")); err != nil {
						t.Fatal(err)
					}
				case "symlink parent":
					link := filepath.Join(filepath.Dir(filepath.Dir(o.ProjectsDir)), "linked-vault")
					if err := os.Symlink(filepath.Dir(o.ProjectsDir), link); err != nil {
						t.Fatal(err)
					}
					o.ProjectsDir = filepath.Join(link, "03-Projects")
				case "malformed settings":
					original = "["
				case "oversized settings":
					original = strings.Repeat("#", maxConfigBytes+1)
				}
				put(t, destination, original)
				if _, err := Projects(o); err == nil {
					t.Fatal("unsafe or conflicting batch accepted")
				}
				if !bytes.Equal([]byte(original), read(t, destination)) {
					t.Fatal("failed batch partially changed global configuration")
				}
			})
		}
	}
}

func TestProjectsInvalidInputsDoNotCreateSettings(t *testing.T) {
	for _, problem := range []string{"missing global", "repo", "config", "target", "project", "client", "binary", "wrong directory", "empty", "invalid folder"} {
		t.Run(problem, func(t *testing.T) {
			o, destination := projectsFixture(t, "codex")
			switch problem {
			case "missing global":
				o.Global = false
			case "repo":
				o.Repo = "."
			case "config":
				o.Config = "/unused.yaml"
			case "target":
				o.Target = filepath.Join(o.ProjectsDir, "alpha")
			case "project":
				o.Project = "alpha"
			case "client":
				o.Client = "unknown"
			case "binary":
				o.Binary = filepath.Join(o.ProjectsDir, "missing-binary")
			case "wrong directory":
				o.ProjectsDir = filepath.Dir(o.ProjectsDir)
			case "empty":
				o.ProjectsDir = filepath.Join(filepath.Dir(filepath.Dir(o.ProjectsDir)), "empty-vault", "03-Projects")
				if err := os.MkdirAll(o.ProjectsDir, 0o700); err != nil {
					t.Fatal(err)
				}
				put(t, filepath.Join(o.ProjectsDir, "index.md"), "# Projects")
			case "invalid folder":
				if err := os.Mkdir(filepath.Join(o.ProjectsDir, "zz_INVALID"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Projects(o); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Lstat(filepath.Dir(destination)); !os.IsNotExist(err) {
				t.Fatalf("invalid batch created global configuration directory: %v", err)
			}
		})
	}
}

func TestProjectsDiscoveryLimitDoesNotCreateSettings(t *testing.T) {
	o, destination := projectsFixture(t, "codex")
	// Even ignored regular files count toward bounded directory discovery.
	for i := 0; i < 1025; i++ {
		path := filepath.Join(o.ProjectsDir, fmt.Sprintf("note-%04d.md", i))
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Projects(o); err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("unbounded directory accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(destination)); !os.IsNotExist(err) {
		t.Fatalf("oversized discovery created settings: %v", err)
	}
}

func TestProjectsOutputSizeLimitPreservesSettings(t *testing.T) {
	o, destination := projectsFixture(t, "codex")
	// The input is valid and within its limit; registering even one project
	// would take the resulting configuration beyond the output limit.
	original := strings.Repeat("#", maxConfigBytes-40) + "\n"
	put(t, destination, original)
	if _, err := Projects(o); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("oversized output accepted: %v", err)
	}
	if !bytes.Equal([]byte(original), read(t, destination)) {
		t.Fatal("output size failure changed existing settings")
	}
}
