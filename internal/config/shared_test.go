package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExplicitSharedGrants(t *testing.T) {
	root, _ := configFixture(t)
	cfg, err := FromTarget(filepath.Join(root, "03-Projects/alpha"), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.SharedReadPaths = []string{"05-Knowledge/Go"}
	if err := AddShared(cfg, []string{"05-Knowledge", "05-Knowledge/Security", "05-Knowledge"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.SharedPaths, []string{"05-Knowledge", "05-Knowledge/Security"}) || len(cfg.SharedGrants()) != 3 {
		t.Fatalf("unexpected grants: %+v", cfg)
	}
	if cfg.CanWriteShared("05-Knowledge/Go/example.md") {
		t.Fatal("read-only launch enables shared writes")
	}
	cfg.AllowWrites = true
	if !cfg.CanWriteShared("05-Knowledge/Go/example.md") || cfg.CanWriteShared("03-Projects/beta/index.md") || cfg.CanWriteShared("05-Knowledge-other/note.md") {
		t.Fatal("wrong shared write scope")
	}
	cfg.SharedPaths = []string{"05-Knowledge/Security"}
	if cfg.CanWriteShared("05-Knowledge/Go/example.md") {
		t.Fatal("legacy YAML grant became writable")
	}
}

func TestInvalidSharedFlagsPreserveScope(t *testing.T) {
	root, _ := configFixture(t)
	if err := os.Symlink(filepath.Join(root, "05-Knowledge/Go"), filepath.Join(root, "05-Knowledge/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "05-Knowledge/file.md"), []byte("# Note"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "../05-Knowledge", "03-Projects/beta", "05-Knowledge/Go/../Security", "05-Knowledge/Go/", "05-Knowledge/.private", "05-Knowledge/assets", "05-Knowledge/missing", "05-Knowledge/link", "05-Knowledge/file.md", filepath.Join(root, "05-Knowledge"), "05-Knowledge\n"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := FromTarget(filepath.Join(root, "03-Projects/alpha"), "")
			if err != nil {
				t.Fatal(err)
			}
			if err := AddShared(cfg, []string{"05-Knowledge/Go", name}); err == nil {
				t.Fatal("accepted invalid shared folder")
			}
			if len(cfg.SharedPaths) != 0 {
				t.Fatal("partially applied invalid grants")
			}
		})
	}
}

func TestSharedGrantCapIncludesLegacyGrants(t *testing.T) {
	root, _ := configFixture(t)
	cfg, err := FromTarget(filepath.Join(root, "03-Projects/alpha"), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("05-Knowledge/folder-%d", i)
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
		cfg.SharedReadPaths = append(cfg.SharedReadPaths, name)
	}
	if err := AddShared(cfg, []string{"05-Knowledge"}); err == nil {
		t.Fatal("accepted more than 32 combined grants")
	}
}
