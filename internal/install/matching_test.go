package install

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

func matchingFixture(t *testing.T, client string) (Options, string) {
	t.Helper()
	o, destination := projectsFixture(t, client)
	o.SourceDir = filepath.Join(filepath.Dir(filepath.Dir(o.ProjectsDir)), "Source Projects")
	for _, id := range []string{"alpha", "vault-bridge", "zeta"} {
		mkdir(t, filepath.Join(o.SourceDir, id))
	}
	put(t, filepath.Join(o.ProjectsDir, "alpha/index.md"), "# Alpha\nExisting documentation.\n")
	return o, destination
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// Include paths, types, permissions and file contents so creating a hidden
// settings/lock file or scaffolding a missing note also fails the assertion.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			value += fmt.Sprintf(" %x", sha256.Sum256(read(t, path)))
		}
		result[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSourceMatchingInstallation(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, writes := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/writes=%v", client, writes), func(t *testing.T) {
				o, destination := matchingFixture(t, client)
				o.AllowWrites = writes
				// zeta has no index, and a preexisting connection must be retained.
				original := "# Keep settings\n[mcp_servers.devvault-zeta]\ncommand = 'keep-me'\n"
				if client == "claude" {
					original = `{"precise":1234567890123456789,"mcpServers":{"devvault-zeta":{"command":"keep-me"}}}`
				}
				put(t, destination, original)
				for _, name := range []string{"docs-only", "case-app", "nested-app"} {
					put(t, filepath.Join(o.ProjectsDir, name, "index.md"), "# Existing\n")
				}
				for _, name := range []string{"source-only", "Case-app", "team/nested-app", ".hidden"} {
					mkdir(t, filepath.Join(o.SourceDir, name))
				}
				put(t, filepath.Join(o.SourceDir, "alpha/main.go"), "package main\n")
				put(t, filepath.Join(o.SourceDir, "README.md"), "# Source\n")
				beforeVault, beforeSource := treeSnapshot(t, o.ProjectsDir), treeSnapshot(t, o.SourceDir)
				result, err := Projects(o)
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Projects) != 2 || result.Projects[0].Project != "alpha" || result.Projects[1].Project != "vault-bridge" {
					t.Fatalf("wrong matches: %+v", result)
				}
				data := read(t, destination)
				servers := projectServers(t, client, data)
				if len(servers) != 3 || servers["devvault-zeta"].(map[string]any)["command"] != "keep-me" {
					t.Fatalf("wrong entries or lost unmatched connection: %v", servers)
				}
				for _, project := range result.Projects {
					if !project.Changed || project.Path != destination || project.Source != filepath.Join(o.SourceDir, project.Project) || project.Target != filepath.Join(o.ProjectsDir, project.Project) {
						t.Fatalf("incomplete result: %+v", project)
					}
					entry := servers["devvault-"+project.Project].(map[string]any)
					want := []any{"serve", "--target", project.Target, "--project", project.Project, "--transport", "stdio"}
					if writes {
						want = append(want, "--allow-writes")
					}
					if entry["command"] != o.Binary || !reflect.DeepEqual(entry["args"], want) || entry["cwd"] != nil {
						t.Fatalf("unexpected launch or source access: %v", entry)
					}
				}
				wantSkips := map[string]string{"Case-app": "no matching vault project", "case-app": "no matching source folder", "docs-only": "no matching source folder", "nested-app": "no matching source folder", "source-only": "no matching vault project", "team": "no matching vault project", "zeta": "missing index.md"}
				gotSkips := map[string]string{}
				for i, skipped := range result.Skipped {
					if i > 0 && result.Skipped[i-1].Project >= skipped.Project {
						t.Fatal("skip output is not sorted")
					}
					gotSkips[skipped.Project] = skipped.Reason
				}
				if !reflect.DeepEqual(gotSkips, wantSkips) {
					t.Fatalf("skips = %v, want %v", gotSkips, wantSkips)
				}
				result, err = Projects(o)
				if err != nil || !bytes.Equal(data, read(t, destination)) {
					t.Fatalf("repeat changed settings: %v", err)
				}
				for _, project := range result.Projects {
					if project.Changed {
						t.Fatal("repeat reported change")
					}
				}
				afterSource := treeSnapshot(t, o.SourceDir)
				for _, id := range []string{"alpha", "vault-bridge"} {
					path := filepath.Join(o.SourceDir, id, instructionName(client))
					if !strings.Contains(string(read(t, path)), "`devvault-"+id+"`") {
						t.Fatal("missing matched project guidance")
					}
					assertInstructionWriteMode(t, read(t, path), writes)
					delete(afterSource, path)
				}
				if !reflect.DeepEqual(beforeVault, treeSnapshot(t, o.ProjectsDir)) || !reflect.DeepEqual(beforeSource, afterSource) {
					t.Fatal("installer changed vault or unrelated source files")
				}
				put(t, filepath.Join(o.ProjectsDir, "new-match/index.md"), "# New\n")
				mkdir(t, filepath.Join(o.SourceDir, "new-match"))
				result, err = Projects(o)
				if err != nil || len(result.Projects) != 3 {
					t.Fatalf("new match: %+v, %v", result, err)
				}
				for _, project := range result.Projects {
					if project.Changed != (project.Project == "new-match") {
						t.Fatalf("wrong change status: %+v", project)
					}
				}
			})
		}
	}
}

func TestSourceMatchingFailuresAreAtomic(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		for _, problem := range []string{"malformed index", "oversized index", "invalid UTF-8", "index directory", "index symlink", "source symlink", "source ancestor symlink", "vault symlink", "invalid matched ID", "late conflict"} {
			t.Run(client+"/"+problem, func(t *testing.T) {
				o, destination := matchingFixture(t, client)
				index := filepath.Join(o.ProjectsDir, "zeta/index.md")
				original := "# unchanged\n"
				if client == "claude" {
					original = `{"unchanged":true}`
				}
				switch problem {
				case "malformed index":
					put(t, index, "---\ninvalid: [\n---\n")
				case "oversized index":
					put(t, index, strings.Repeat("x", config.DefaultLimits().MaxDocumentBytes+1))
				case "invalid UTF-8":
					put(t, index, "\xff")
				case "index directory":
					mkdir(t, index)
				case "index symlink":
					if err := os.Symlink(filepath.Join(o.ProjectsDir, "alpha/index.md"), index); err != nil {
						t.Fatal(err)
					}
				case "source symlink", "vault symlink":
					parent := o.SourceDir
					if problem == "vault symlink" {
						parent = o.ProjectsDir
					}
					if err := os.Symlink(filepath.Join(parent, "alpha"), filepath.Join(parent, "zz-link")); err != nil {
						t.Fatal(err)
					}
				case "source ancestor symlink":
					link := filepath.Join(filepath.Dir(o.SourceDir), "alias")
					if err := os.Symlink(filepath.Dir(o.SourceDir), link); err != nil {
						t.Fatal(err)
					}
					o.SourceDir = filepath.Join(link, filepath.Base(o.SourceDir))
				case "invalid matched ID":
					mkdir(t, filepath.Join(o.SourceDir, "zz_INVALID"))
					put(t, filepath.Join(o.ProjectsDir, "zz_INVALID/index.md"), "# Existing")
				case "late conflict":
					put(t, index, "# Zeta\n")
					original = "[mcp_servers.devvault-zeta]\ncommand = 'other'\n"
					if client == "claude" {
						original = `{"mcpServers":{"devvault-zeta":{"command":"other"}}}`
					}
				}
				put(t, destination, original)
				if _, err := Projects(o); err == nil || errors.Is(err, ErrNoMatches) {
					t.Fatalf("unsafe input accepted or skipped: %v", err)
				}
				if string(read(t, destination)) != original {
					t.Fatal("failed batch changed settings")
				}
			})
		}
	}
}

func TestSourceMatchingNoMatches(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			o, destination := projectsFixture(t, client)
			o.SourceDir = filepath.Join(filepath.Dir(o.ProjectsDir), "sources")
			mkdir(t, filepath.Join(o.SourceDir, "zeta")) // exact match, missing index
			mkdir(t, filepath.Join(o.SourceDir, "source-only"))
			result, err := Projects(o)
			if !errors.Is(err, ErrNoMatches) || len(result.Skipped) != 4 || len(result.Projects) != 0 {
				t.Fatalf("no-match diagnostics: %+v %v", result, err)
			}
			if _, err := os.Lstat(filepath.Dir(destination)); !os.IsNotExist(err) {
				t.Fatalf("no-match batch created settings: %v", err)
			}
		})
	}
}

func TestSourceMatchingDiscoveryBound(t *testing.T) {
	o, destination := matchingFixture(t, "codex")
	for i := 0; i < 1025; i++ {
		put(t, filepath.Join(o.SourceDir, fmt.Sprintf("file-%04d", i)), "")
	}
	if _, err := Projects(o); err == nil || !strings.Contains(err.Error(), "source directory exceeds 1024") {
		t.Fatalf("source discovery was not bounded: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(destination)); !os.IsNotExist(err) {
		t.Fatalf("oversized discovery created settings: %v", err)
	}
}
