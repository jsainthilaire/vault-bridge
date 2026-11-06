//go:build linux || darwin

package vault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

func sharedStore(t *testing.T, f fixture, project, shared string, writes bool) *Store {
	t.Helper()
	cfg := *f.config
	cfg.ProjectID, cfg.ProjectRoot, cfg.AllowWrites = project, "03-Projects/"+project, writes
	if err := config.AddShared(&cfg, []string{shared}); err != nil {
		t.Fatal(err)
	}
	s, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSharedRootRetrievalAndWritePermissions(t *testing.T) {
	f := makeFixture(t, true)
	ctx := context.Background()
	for _, writes := range []bool{false, true} {
		s := sharedStore(t, f, "alpha", "05-Knowledge", writes)
		listing, err := s.List(ctx, ListOptions{Section: "shared:05-Knowledge"})
		if err != nil || len(listing.Documents) != 2 {
			t.Fatalf("whole knowledge listing: %+v, %v", listing, err)
		}
		search, err := s.Search(ctx, SearchOptions{Section: "shared:05-Knowledge", Query: "sharedonly"})
		if err != nil || len(search.Matches) != 1 {
			t.Fatalf("whole knowledge search: %+v, %v", search, err)
		}
		doc, err := s.Read(ctx, "05-Knowledge/Go/shared.md", 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Update(ctx, doc.Path, "# Updated shared\n", doc.Revision)
		if writes {
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Update(ctx, doc.Path, "# Stale update", doc.Revision)
			assertCode(t, err, "conflict")
			if _, err := s.Create(ctx, "05-Knowledge/new.md", "# New shared note"); err != nil {
				t.Fatal(err)
			}
		} else {
			assertCode(t, err, "permission_denied")
			_, err := s.Create(ctx, "05-Knowledge/new.md", "# Forbidden")
			assertCode(t, err, "permission_denied")
			if _, err := os.Stat(filepath.Join(f.root, "05-Knowledge/Go", writeLockName)); !os.IsNotExist(err) {
				t.Fatal("read-only write created a lock")
			}
		}
		_, err = s.Read(ctx, "03-Projects/beta/index.md", 0)
		assertCode(t, err, "permission_denied")
		_, err = s.Create(ctx, "03-Projects/beta/forbidden.md", "# Forbidden")
		assertCode(t, err, "permission_denied")
	}
}

func TestSharedWritesStayWithinExplicitGrant(t *testing.T) {
	f := makeFixture(t, true)
	s := sharedStore(t, f, "alpha", "05-Knowledge/Security", true)
	ctx := context.Background()
	doc, err := s.Read(ctx, "05-Knowledge/Go/shared.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update(ctx, doc.Path, "# Legacy read grant", doc.Revision)
	assertCode(t, err, "permission_denied")
	for _, tc := range []struct{ path, code string }{
		{"05-Knowledge/Go/forbidden.md", "permission_denied"},
		{"05-Knowledge/Security/../Go/forbidden.md", "invalid_argument"},
		{"05-Knowledge/Security/.hidden.md", "permission_denied"},
		{"05-Knowledge/Security/secrets/key.md", "permission_denied"},
		{"05-Knowledge/Security/file.txt", "permission_denied"},
		{"05-Knowledge/Security/missing/note.md", "not_found"},
	} {
		_, err := s.Create(ctx, tc.path, "# Forbidden")
		assertCode(t, err, tc.code)
	}
	if err := os.Symlink(filepath.Join(f.root, "03-Projects/beta"), filepath.Join(f.root, "05-Knowledge/Security/link")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "05-Knowledge/Security/link/note.md", "# Escape"); err == nil {
		t.Fatal("followed directory symlink")
	}
	if err := os.Symlink(filepath.Join(f.root, "03-Projects/beta/index.md"), filepath.Join(f.root, "05-Knowledge/Security/link.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, "05-Knowledge/Security/link.md", "# Escape", doc.Revision); err == nil {
		t.Fatal("followed note symlink")
	}
	if _, err := s.Create(ctx, "05-Knowledge/Security/large.md", strings.Repeat("x", s.scope.Limits.MaxDocumentBytes+1)); ErrorCode(err) != "resource_limit" {
		t.Fatalf("shared byte cap: %v", err)
	}
}

func TestOverlappingSharedWritersHaveOneWinner(t *testing.T) {
	f := makeFixture(t, false)
	a := sharedStore(t, f, "alpha", "05-Knowledge", true)
	b := sharedStore(t, f, "beta", "05-Knowledge/Go", true)
	ctx := context.Background()
	doc, err := a.Read(ctx, "05-Knowledge/Go/shared.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			<-start
			_, err := s.Update(ctx, doc.Path, []string{"# Alpha update", "# Beta update"}[i], doc.Revision)
			results <- err
		}(i, s)
	}
	close(start)
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if ErrorCode(err) == "conflict" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	if _, err := os.Stat(filepath.Join(f.root, "05-Knowledge/Go", writeLockName)); err != nil {
		t.Fatal("shared writers did not lock the destination parent")
	}
}

func TestWritableSharedGrantMustKeepIdentity(t *testing.T) {
	f := makeFixture(t, true) // read-only parent must not bypass the pinned writable child.
	f.write(t, "05-Knowledge/Go/mutable/note.md", "# Original")
	s := sharedStore(t, f, "alpha", "05-Knowledge/Go/mutable", true)
	path := filepath.Join(f.root, "05-Knowledge/Go/mutable")
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create(context.Background(), "05-Knowledge/Go/mutable/new.md", "# Changed root")
	assertCode(t, err, "permission_denied")
}
