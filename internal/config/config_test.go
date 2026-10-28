package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func configFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"03-Projects/alpha", "03-Projects/beta", "05-Knowledge/Go", "05-Knowledge/Security"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := fmt.Sprintf("version: 1\nvault_root: %q\nprojects:\n  alpha:\n    folder: 03-Projects/alpha\n  beta:\n    folder: 03-Projects/beta\n", root)
	return root, data
}

func writeConfig(t *testing.T, root, data string) string {
	t.Helper()
	name := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(name, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestLoadDefaultsAndExplicitIdentity(t *testing.T) {
	root, data := configFixture(t)
	name := writeConfig(t, root, data)
	cfg, err := Load(name, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VaultRoot != canonical || cfg.ProjectID != "alpha" || cfg.ProjectRoot != "03-Projects/alpha" || len(cfg.SharedReadPaths) != 0 || cfg.Limits != DefaultLimits() {
		t.Fatalf("unexpected scope: %+v", cfg)
	}
	if _, err := Load(name, "unknown"); scope.ErrorCode(err) != "not_found" {
		t.Fatalf("unknown ID: %v", err)
	}
	if _, err := Load(name, ""); scope.ErrorCode(err) != "invalid_argument" {
		t.Fatalf("empty ID: %v", err)
	}
	if _, err := Load("relative.yaml", "alpha"); scope.ErrorCode(err) != "invalid_argument" {
		t.Fatalf("relative config: %v", err)
	}
}

func TestStrictConfigurationAndLimits(t *testing.T) {
	root, base := configFixture(t)
	cases := []struct{ name, data, code string }{
		{"unknown-key", base + "typo: true\n", "invalid_argument"},
		{"unknown-project-key", strings.Replace(base, "folder: 03-Projects/alpha", "folder: 03-Projects/alpha\n    wrong: true", 1), "invalid_argument"},
		{"duplicate-key", base + "version: 1\n", "invalid_argument"},
		{"multiple-docs", base + "---\nversion: 1\n", "invalid_argument"},
		{"unsupported-version", strings.Replace(base, "version: 1", "version: 2", 1), "invalid_argument"},
		{"relative-vault", strings.Replace(base, fmt.Sprintf("vault_root: %q", root), "vault_root: ./vault", 1), "invalid_argument"},
		{"traversal", strings.Replace(base, "03-Projects/alpha", "03-Projects/alpha/../beta", 1), "invalid_argument"},
		{"broad-projects", strings.Replace(base, "03-Projects/alpha", "03-Projects", 1), "invalid_argument"},
		{"missing-mapping", strings.Replace(base, "03-Projects/beta", "03-Projects/missing", 1), "not_found"},
		{"zero-byte-limit", base + "limits:\n  max_document_bytes: 0\n", "invalid_argument"},
		{"negative-results", base + "limits:\n  max_search_results: -1\n", "invalid_argument"},
		{"huge-scan", base + "limits:\n  max_search_scan_files: 999999\n", "invalid_argument"},
		{"hidden-root", strings.Replace(base, "03-Projects/alpha", "03-Projects/.alpha", 1), "permission_denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, root, tc.data), "alpha")
			if scope.ErrorCode(err) != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
	valid := base + "limits:\n  max_document_bytes: 1024\n  max_search_results: 2\n  max_search_scan_files: 20\n"
	cfg, err := Load(writeConfig(t, root, valid), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits != (Limits{1024, 2, 20}) {
		t.Fatalf("wrong limits %+v", cfg.Limits)
	}
}

func TestSharedGrantsAreExplicitKnowledgeSubtrees(t *testing.T) {
	root, base := configFixture(t)
	for _, shared := range []string{"05-Knowledge", "03-Projects/beta", "05-Knowledge/Go/../Security", "05-Knowledge/Go2", "05-Knowledge/.private"} {
		t.Run(shared, func(t *testing.T) {
			data := strings.Replace(base, "folder: 03-Projects/alpha", "folder: 03-Projects/alpha\n    shared_read_paths: ["+shared+"]", 1)
			if _, err := Load(writeConfig(t, root, data), "alpha"); err == nil {
				t.Fatal("invalid shared grant accepted")
			}
		})
	}
	data := strings.Replace(base, "folder: 03-Projects/alpha", "folder: 03-Projects/alpha\n    shared_read_paths: [05-Knowledge/Go]", 1)
	cfg, err := Load(writeConfig(t, root, data), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SharedReadPaths) != 1 || cfg.SharedReadPaths[0] != "05-Knowledge/Go" {
		t.Fatalf("unexpected grants %+v", cfg.SharedReadPaths)
	}
}

func TestConfigurationRejectsSymlinkComponents(t *testing.T) {
	root, base := configFixture(t)
	link := filepath.Join(root, "03-Projects/link")
	if err := os.Symlink("beta", link); err != nil {
		t.Skip(err)
	}
	data := strings.Replace(base, "03-Projects/alpha", "03-Projects/link", 1)
	if _, err := Load(writeConfig(t, root, data), "alpha"); scope.ErrorCode(err) != "permission_denied" {
		t.Fatalf("symlink root: %v", err)
	}
	link = filepath.Join(root, "05-Knowledge/alias")
	if err := os.Symlink("Go", link); err != nil {
		t.Fatal(err)
	}
	data = strings.Replace(base, "folder: 03-Projects/alpha", "folder: 03-Projects/alpha\n    shared_read_paths: [05-Knowledge/alias]", 1)
	if _, err := Load(writeConfig(t, root, data), "alpha"); scope.ErrorCode(err) != "permission_denied" {
		t.Fatalf("symlink shared: %v", err)
	}
}

func TestInvalidYAMLDiagnosticOmitsContents(t *testing.T) {
	root, _ := configFixture(t)
	secret := "never-print-this-private-value"
	_, err := Load(writeConfig(t, root, "version: ["+secret), "alpha")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe YAML error: %v", err)
	}
}
