package scaffold

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVaultInitProjectCreatesSelectedProjectAndTemplates(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "empty"
		if missing {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			dir := testDir(t)
			if missing {
				dir = filepath.Join(dir, "nested", "new vault")
			}
			if err := VaultInitProject(dir, "my-app", "My App"); err != nil {
				t.Fatal(err)
			}
			files := treeFiles(t, dir)
			meta := frontmatter(t, "index", files["03-Projects/my-app/index.md"])
			if meta["project"] != "my-app" || meta["type"] != "project" {
				t.Fatalf("unexpected project metadata: %#v", meta)
			}
			if language, ok := meta["language"].([]any); !ok || len(language) != 0 {
				t.Errorf("generic project assumes a language: %#v", meta["language"])
			}
			for _, name := range []string{"Vision", "Requirements", "Architecture", "Roadmap", "Tasks"} {
				note := "03-Projects/my-app/" + strings.ToLower(name) + ".md"
				template := "08-Templates/" + name + ".md"
				for _, target := range []struct{ path, project string }{{note, "my-app"}, {template, "project-id"}} {
					data := files[target.path]
					meta := frontmatter(t, target.path, data)
					if meta["type"] != strings.ToLower(name) || meta["project"] != target.project {
						t.Errorf("%s: unexpected metadata %#v", target.path, meta)
					}
					if !bytes.Contains(data, []byte("[[03-Projects/"+target.project+"/index|Project overview]]")) {
						t.Errorf("%s lacks project backlink", target.path)
					}
					if bytes.Contains(data, []byte("{{")) {
						t.Errorf("%s contains unrendered template syntax", target.path)
					}
				}
				if !bytes.Contains(files["08-Templates/index.md"], []byte("[[08-Templates/"+name+"|"+name+"]]")) {
					t.Errorf("template index lacks %s", name)
				}
			}
			for _, collection := range []string{"features", "decisions", "research", "sessions"} {
				entries, err := os.ReadDir(filepath.Join(dir, "03-Projects/my-app", collection))
				if err != nil || len(entries) != 0 {
					t.Errorf("collection %s should be empty: entries=%v err=%v", collection, entries, err)
				}
			}
			for _, index := range []string{"00-Dashboard/Home.md", "03-Projects/index.md"} {
				if !bytes.Contains(files[index], []byte("[[03-Projects/my-app/index|My App]]")) {
					t.Errorf("%s lacks selected project link", index)
				}
			}
			entries, err := os.ReadDir(filepath.Join(dir, "03-Projects"))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "my-app" && entry.Name() != "index.md" {
					t.Errorf("unexpected project entry %s", entry.Name())
				}
			}
		})
	}
}

func TestVaultInitProjectPreservesAuthoredPlanningNotesAndTemplates(t *testing.T) {
	dir := testDir(t)
	if err := VaultInitProject(dir, "my-app", "My App"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"03-Projects/my-app/roadmap.md", "08-Templates/Roadmap.md", "00-Dashboard/Home.md"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeTest(t, path, string(data)+"\nAuthored work and completed milestones.\n")
	}
	before := treeFiles(t, dir)
	if err := VaultInitProject(dir, "my-app", "Renamed App"); err != nil {
		t.Fatal(err)
	}
	if after := treeFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("reinitialization changed authored content or added files")
	}
}

func TestVaultInitProjectRejectsInvalidIdentityBeforeCreatingDirectories(t *testing.T) {
	for _, input := range []struct{ id, title string }{
		{"../escape", "Valid"}, {"assets", "Valid"}, {"valid", "Injected|link"}, {"valid", " two lines\n"},
	} {
		t.Run(input.id+input.title, func(t *testing.T) {
			parent := testDir(t)
			if err := VaultInitProject(filepath.Join(parent, "missing", "vault"), input.id, input.title); err == nil {
				t.Fatal("invalid identity accepted")
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid identity caused filesystem changes: %v, %v", entries, err)
			}
		})
	}
}

func TestVaultInitProjectRejectsSymlinksWithoutOutsideWrites(t *testing.T) {
	for _, kind := range []string{"ancestor", "planning-note", "reusable-template"} {
		t.Run(kind, func(t *testing.T) {
			parent, outside := testDir(t), testDir(t)
			marker := filepath.Join(outside, "marker.md")
			writeTest(t, marker, "authored outside content\n")
			vault := filepath.Join(parent, "vault")
			var link, destination string
			switch kind {
			case "ancestor":
				link, destination = filepath.Join(parent, "linked"), outside
				vault = filepath.Join(link, "missing-vault")
			case "planning-note":
				link, destination = filepath.Join(vault, "03-Projects/my-app/roadmap.md"), marker
			case "reusable-template":
				link, destination = filepath.Join(vault, "08-Templates/Roadmap.md"), marker
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			if err := VaultInitProject(vault, "my-app", "My App"); err == nil {
				t.Fatal("symlink accepted")
			}
			if got := treeFiles(t, outside); len(got) != 1 || string(got["marker.md"]) != "authored outside content\n" {
				t.Fatalf("outside content changed: %v", got)
			}
			if kind == "ancestor" {
				if _, err := os.Stat(filepath.Join(outside, "missing-vault")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("created directory through ancestor symlink: %v", err)
				}
			}
		})
	}
}
