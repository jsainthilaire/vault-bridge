package scaffold

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func testDir(t *testing.T) string {
	t.Helper()
	// macOS commonly provides a /var/... TMPDIR whose /var ancestor is a
	// symlink. Use its canonical spelling because initialization rejects links.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTest(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func treeFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func frontmatter(t *testing.T, name string, content []byte) map[string]any {
	t.Helper()
	parts := strings.SplitN(string(content), "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatalf("%s has no YAML frontmatter", name)
	}
	var metadata map[string]any
	if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
		t.Fatalf("%s frontmatter: %v", name, err)
	}
	for _, field := range []string{"created", "updated"} {
		date, ok := metadata[field].(string)
		if !ok {
			t.Fatalf("%s: %s must be an ISO date string, got %#v", name, field, metadata[field])
		}
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			t.Fatalf("%s: invalid %s date %q", name, field, date)
		}
	}
	return metadata
}

func TestVaultInitCreatesUsableVault(t *testing.T) {
	dir := filepath.Join(testDir(t), "nested", "vault")
	if err := VaultInit(dir); err != nil {
		t.Fatal(err)
	}
	files := treeFiles(t, dir)
	for _, required := range []string{
		"00-Dashboard/Home.md", "01-Inbox/index.md", "02-Ideas/index.md", "03-Projects/index.md",
		"04-Architecture/index.md", "05-Knowledge/Go/index.md", "05-Knowledge/AI-Agents/index.md",
		"05-Knowledge/Security/index.md", "05-Knowledge/Infrastructure/index.md", "06-Agent-Workflows/index.md",
		"07-Research/index.md", "08-Templates/Project.md", "08-Templates/Feature.md", "08-Templates/ADR.md",
		"08-Templates/Research.md", "08-Templates/Session.md", "08-Templates/Idea.md", "09-Archive/index.md",
		"03-Projects/vault-bridge/index.md", "03-Projects/vault-bridge/vision.md", "03-Projects/vault-bridge/requirements.md",
		"03-Projects/vault-bridge/architecture.md", "03-Projects/vault-bridge/roadmap.md", "03-Projects/vault-bridge/tasks.md",
		"03-Projects/vault-bridge/features/vault-scaffolding.md", "03-Projects/vault-bridge/features/scoped-file-access.md",
		"03-Projects/vault-bridge/features/mcp-read-tools.md", "03-Projects/vault-bridge/features/agent-integration.md",
		"03-Projects/vault-bridge/decisions/ADR-001-local-stdio-mcp.md", "03-Projects/vault-bridge/decisions/ADR-002-read-only-mvp.md",
		"03-Projects/vault-bridge/decisions/ADR-003-explicit-project-scoping.md", "03-Projects/vault-bridge/research/sdk-and-client-integration.md",
	} {
		if _, ok := files[required]; !ok {
			t.Errorf("missing %s", required)
		}
	}
	for _, collection := range []string{"features", "decisions", "research", "sessions"} {
		if info, err := os.Stat(filepath.Join(dir, "03-Projects/vault-bridge", collection)); err != nil || !info.IsDir() {
			t.Errorf("missing project collection %s", collection)
		}
	}
	projects, err := os.ReadDir(filepath.Join(dir, "03-Projects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if project.IsDir() && project.Name() != "vault-bridge" {
			t.Errorf("unsolicited demo project %s", project.Name())
		}
	}
	links := regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
	for name, data := range files {
		frontmatter(t, name, data)
		if strings.HasPrefix(name, "03-Projects/vault-bridge/") {
			meta := frontmatter(t, name, data)
			if meta["project"] != "vault-bridge" {
				t.Errorf("%s: wrong project metadata %#v", name, meta["project"])
			}
		}
		for _, match := range links.FindAllSubmatch(data, -1) {
			target := string(match[1]) + ".md"
			// The reusable Project template deliberately contains project-id
			// placeholders; every generated project/dashboard link resolves.
			if strings.HasPrefix(name, "08-Templates/") {
				continue
			}
			if _, ok := files[target]; !ok {
				t.Errorf("%s: unresolved wikilink %s", name, target)
			}
		}
	}
	meta := frontmatter(t, "index", files["03-Projects/vault-bridge/index.md"])
	for _, field := range []string{"type", "project", "status", "priority", "language", "repository", "created", "updated"} {
		if _, ok := meta[field]; !ok {
			t.Errorf("project index lacks %s", field)
		}
	}
	if !bytes.Contains(files["00-Dashboard/Home.md"], []byte("[[03-Projects/vault-bridge/index|Vault Bridge — Obsidian MCP]]")) {
		t.Error("dashboard lacks bridge project link")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, "03-Projects/vault-bridge/index.md"))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("note permissions %o, want 600", info.Mode().Perm())
		}
		info, _ = os.Stat(filepath.Join(dir, "03-Projects/vault-bridge"))
		if info.Mode().Perm() != 0o700 {
			t.Errorf("directory permissions %o, want 700", info.Mode().Perm())
		}
	}
}

func TestRepeatedInitializationPreservesAuthoredFiles(t *testing.T) {
	dir := testDir(t)
	if err := VaultInit(dir); err != nil {
		t.Fatal(err)
	}
	for _, note := range []string{"00-Dashboard/Home.md", "03-Projects/index.md", "03-Projects/vault-bridge/index.md", "03-Projects/vault-bridge/tasks.md", "03-Projects/vault-bridge/features/mcp-read-tools.md", "08-Templates/Feature.md"} {
		file, err := os.OpenFile(filepath.Join(dir, note), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("\nAuthored material that must survive.\n"); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	before := treeFiles(t, dir)
	if err := VaultInit(dir); err != nil {
		t.Fatal(err)
	}
	if err := ProjectInit(dir, "vault-bridge", "Changed title"); err != nil {
		t.Fatal(err)
	}
	after := treeFiles(t, dir)
	if len(after) != len(before) {
		t.Fatalf("repeat changed file count: %d to %d", len(before), len(after))
	}
	for name, data := range before {
		if !bytes.Equal(data, after[name]) {
			t.Errorf("repeat changed authored file %s", name)
		}
	}
}

func TestProjectInitCreatesNormalProjectAndAddsExactLinks(t *testing.T) {
	dir := testDir(t)
	writeTest(t, filepath.Join(dir, "00-Dashboard/Home.md"), "# Authored dashboard\n\n- [[03-Projects/test-project-lookalike/index|Existing]]\n")
	title := `Engine "quoted": API's \ path`
	if err := ProjectInit(dir, "test-project", title); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "03-Projects/test-project/index.md"))
	if err != nil {
		t.Fatal(err)
	}
	meta := frontmatter(t, "new project", data)
	if meta["project"] != "test-project" {
		t.Fatal("wrong project ID")
	}
	if !strings.Contains(string(data), "# "+title) {
		t.Error("title text was changed")
	}
	before := treeFiles(t, dir)
	if err := ProjectInit(dir, "test-project", title); err != nil {
		t.Fatal(err)
	}
	after := treeFiles(t, dir)
	for name, original := range before {
		if !bytes.Equal(after[name], original) {
			t.Errorf("repeat project init changed %s", name)
		}
	}
	dashboard := string(after["00-Dashboard/Home.md"])
	if !strings.HasPrefix(dashboard, "# Authored dashboard\n") || strings.Count(dashboard, "[[03-Projects/test-project/index|") != 1 {
		t.Errorf("dashboard content/link handling failed: %q", dashboard)
	}
	if _, err := os.Stat(filepath.Join(dir, "03-Projects/vault-bridge")); !errors.Is(err, os.ErrNotExist) {
		t.Error("ordinary project init seeded an unsolicited bridge project")
	}
}

func TestDashboardRecognizesExistingTargetWithDifferentLabel(t *testing.T) {
	for _, target := range []string{"[[03-Projects/existing/index]]", "[[03-Projects/existing/index|Custom label]]", "[[03-Projects/existing/index.md|Custom label]]"} {
		t.Run(target, func(t *testing.T) {
			dir := testDir(t)
			original := "# Dashboard\n" + target + "\n"
			writeTest(t, filepath.Join(dir, "00-Dashboard/Home.md"), original)
			if err := ProjectInit(dir, "existing", "New label"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "00-Dashboard/Home.md"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != original {
				t.Errorf("existing project link was duplicated: %q", data)
			}
		})
	}
}

func TestInvalidProjectInputDoesNotWrite(t *testing.T) {
	for _, id := range []string{"", "../outside", "has.dot", "Upper", "two--hyphens", "-leading", "trailing-", "a/b", "a\\b", "a\x00b", strings.Repeat("a", 65)} {
		t.Run("id="+id, func(t *testing.T) {
			dir := testDir(t)
			if err := ProjectInit(dir, id, "Valid title"); err == nil {
				t.Error("invalid ID accepted")
			}
			if files := treeFiles(t, dir); len(files) != 0 {
				t.Error("invalid input created files")
			}
		})
	}
	for _, title := range []string{"", " leading", "trailing ", "two\nlines", "control\x00byte", "tab\tvalue", "[injection]", "bad|link", "unicode\u2028separator", strings.Repeat("a", 201), string([]byte{0xff})} {
		t.Run("title="+title, func(t *testing.T) {
			dir := testDir(t)
			if err := ProjectInit(dir, "valid-project", title); err == nil {
				t.Error("invalid title accepted")
			}
			if files := treeFiles(t, dir); len(files) != 0 {
				t.Error("invalid input created files")
			}
		})
	}
}

func TestExcludedProjectIDsDoNotCreateFilesystemEntries(t *testing.T) {
	for _, id := range []string{"assets", "attachments", "attachment", "secrets", "secret", "node_modules", "vendor", "credentials", "private-keys", ".hidden"} {
		t.Run(id, func(t *testing.T) {
			dir := testDir(t)
			if err := ProjectInit(dir, id, "Reserved project"); err == nil {
				t.Fatal("project ID excluded by the read policy was accepted")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("rejected ID created filesystem entries: %v", entries)
			}
		})
	}
	// Exclusion applies to a complete component, not lookalike project names.
	dir := testDir(t)
	if err := ProjectInit(dir, "assets-project", "Asset tooling"); err != nil {
		t.Fatalf("valid project ID rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "03-Projects/assets-project/index.md")); err != nil {
		t.Fatal(err)
	}
}

func TestInitRejectsSymlinkRootsParentsAndWriteTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require additional privileges on Windows")
	}
	for _, target := range []string{"root", "parent", "03-Projects", "00-Dashboard", "00-Dashboard/Home.md", "08-Templates/Project.md", "03-Projects/test-project", "03-Projects/test-project/index.md"} {
		t.Run(target, func(t *testing.T) {
			base, outside := testDir(t), testDir(t)
			vault := filepath.Join(base, "vault")
			if err := os.Mkdir(vault, 0o700); err != nil {
				t.Fatal(err)
			}
			writeTest(t, filepath.Join(outside, "marker.md"), "unchanged")
			if target == "root" || target == "parent" {
				link := filepath.Join(base, "linked")
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
				vault = link
				if target == "parent" {
					vault = filepath.Join(link, "child")
				}
			} else {
				link := filepath.Join(vault, filepath.FromSlash(target))
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				destination := outside
				if strings.HasSuffix(target, ".md") {
					destination = filepath.Join(outside, "marker.md")
				}
				if err := os.Symlink(destination, link); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if strings.Contains(target, "test-project") {
				err = ProjectInit(vault, "test-project", "Test project")
			} else {
				err = VaultInit(vault)
			}
			if err == nil {
				t.Error("symlink accepted")
			}
			outsideFiles := treeFiles(t, outside)
			if len(outsideFiles) != 1 || string(outsideFiles["marker.md"]) != "unchanged" {
				t.Errorf("initialization wrote outside the vault: %#v", outsideFiles)
			}
		})
	}
}

func TestInitRejectsNonregularAndOversizedDashboard(t *testing.T) {
	for _, shape := range []string{"directory", "oversized"} {
		t.Run(shape, func(t *testing.T) {
			dir := testDir(t)
			name := filepath.Join(dir, "00-Dashboard/Home.md")
			if shape == "directory" {
				if err := os.MkdirAll(name, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTest(t, name, strings.Repeat("x", maxDashboardBytes+1))
			}
			if err := ProjectInit(dir, "test", "Test"); err == nil {
				t.Fatal("unsafe dashboard accepted")
			}
			if shape == "oversized" {
				info, err := os.Stat(name)
				if err != nil || info.Size() != maxDashboardBytes+1 {
					t.Error("oversized authored dashboard was changed")
				}
			}
		})
	}
}

func TestMissingAndInvalidVaultPaths(t *testing.T) {
	dir := filepath.Join(testDir(t), "missing")
	if err := ProjectInit(dir, "valid", "Valid"); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing vault: expected not-exist error, got %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Error("project init created missing vault")
	}
	for _, name := range []string{"", "invalid\x00path", string(filepath.Separator)} {
		if err := VaultInit(name); err == nil {
			t.Errorf("invalid vault path %q accepted", name)
		}
	}
}
