package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func TestFromTarget(t *testing.T) {
	root, _ := configFixture(t)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(canonical, "03-Projects", "alpha")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, target)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{target, relative, target + string(filepath.Separator)} {
		cfg, err := FromTarget(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.VaultRoot != canonical || cfg.ProjectID != "alpha" || cfg.ProjectRoot != "03-Projects/alpha" || len(cfg.SharedReadPaths) != 0 || cfg.Limits != DefaultLimits() {
			t.Fatalf("unexpected scope: %+v", cfg)
		}
	}
	cfg, err := FromTarget(target, "explicit-alias")
	if err != nil || cfg.ProjectID != "explicit-alias" {
		t.Fatalf("explicit ID: %+v %v", cfg, err)
	}
	spaced := filepath.Join(canonical, "03-Projects", "Project Notes")
	if err := os.Mkdir(spaced, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := FromTarget(spaced, "project-notes"); err != nil {
		t.Fatalf("folder with spaces and explicit ID: %v", err)
	}
}

func TestFromTargetRejectsInvalidScopes(t *testing.T) {
	root, _ := configFixture(t)
	for _, tc := range []struct{ name, target, id, code string }{
		{"empty", "", "", "invalid_argument"},
		{"vault root", root, "alpha", "invalid_argument"},
		{"all projects", filepath.Join(root, "03-Projects"), "alpha", "invalid_argument"},
		{"wrong section", filepath.Join(root, "05-Knowledge", "Go"), "alpha", "invalid_argument"},
		{"missing project", filepath.Join(root, "03-Projects", "missing"), "", "not_found"},
		{"nested folder", filepath.Join(root, "03-Projects", "alpha", "features"), "alpha", "invalid_argument"},
		{"invalid ID", filepath.Join(root, "03-Projects", "alpha"), "Not valid", "invalid_argument"},
		{"NUL", "bad\x00path", "alpha", "invalid_argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := FromTarget(tc.target, tc.id); scope.ErrorCode(err) != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
	for _, name := range []string{".hidden", "secrets", "assets", "Project Notes"} {
		target := filepath.Join(root, "03-Projects", name)
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := FromTarget(target, ""); err == nil {
			t.Fatalf("inferred invalid or excluded ID %s accepted", name)
		}
		if name != "Project Notes" {
			if _, err := FromTarget(target, "alias"); scope.ErrorCode(err) != "permission_denied" {
				t.Fatalf("alias bypassed exclusion %s: %v", name, err)
			}
		}
	}
}

func TestFromTargetRejectsSymlinksAndFiles(t *testing.T) {
	root, _ := configFixture(t)
	link := filepath.Join(root, "03-Projects", "alias")
	if err := os.Symlink("alpha", link); err != nil {
		t.Fatal(err)
	}
	if _, err := FromTarget(link, ""); scope.ErrorCode(err) != "permission_denied" {
		t.Fatalf("project symlink: %v", err)
	}
	file := filepath.Join(root, "03-Projects", "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FromTarget(file, ""); err == nil {
		t.Fatal("accepted a regular file as a target")
	}
	other := t.TempDir()
	if err := os.Symlink(filepath.Join(root, "03-Projects"), filepath.Join(other, "03-Projects")); err != nil {
		t.Fatal(err)
	}
	if _, err := FromTarget(filepath.Join(other, "03-Projects", "alpha"), ""); scope.ErrorCode(err) != "permission_denied" {
		t.Fatalf("project parent symlink: %v", err)
	}
}
