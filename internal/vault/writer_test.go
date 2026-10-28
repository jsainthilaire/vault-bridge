//go:build linux || darwin

package vault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writableFixture(t *testing.T) fixture {
	t.Helper()
	f := makeFixture(t, true)
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.config.AllowWrites = true
	store, err := New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	t.Cleanup(func() { store.Close() })
	return f
}

func readBytes(t *testing.T, f fixture, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCreateAndUpdateMilestones(t *testing.T) {
	f := writableFixture(t)
	ctx := context.Background()
	name := "03-Projects/alpha/roadmap.md"
	original := "---\ntype: roadmap\nowner: authored\n---\n# Roadmap\n\n## Milestones\n\n- [ ] Ship the first version\n\n## Risks\nKeep this authored paragraph.\n"
	created, err := f.store.Create(ctx, name, original)
	if err != nil {
		t.Fatal(err)
	}
	if created.Path != name || created.Revision != documentRevision(original) {
		t.Fatalf("create result: %+v", created)
	}
	info, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("created mode: %v %v", info, err)
	}
	// The revision covers the complete Markdown, even when its read is truncated.
	doc, err := f.store.Read(ctx, name, 15)
	if err != nil || !doc.Truncated || doc.Revision != created.Revision || len(doc.Markdown) >= len(original) {
		t.Fatalf("truncated read revision: %+v %v", doc, err)
	}
	if err := os.Chmod(filepath.Join(f.root, filepath.FromSlash(name)), 0o640); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(original, "- [ ] Ship the first version", "- [x] Ship the first version\n- [ ] Add a second milestone", 1)
	result, err := f.store.Update(ctx, name, updated, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != documentRevision(updated) || result.Revision == created.Revision || readBytes(t, f, name) != updated {
		t.Fatalf("update result or content: %+v", result)
	}
	info, err = os.Stat(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("updated mode: %v %v", info, err)
	}
	_, err = f.store.Update(ctx, name, "# Overwrite stale note", doc.Revision)
	assertCode(t, err, "conflict")
	_, err = f.store.Create(ctx, name, "# Do not replace")
	assertCode(t, err, "conflict")
	if readBytes(t, f, name) != updated {
		t.Fatal("a rejected write changed the note")
	}
	entries, err := os.ReadDir(filepath.Join(f.root, "03-Projects/alpha"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".vault-bridge-write-") {
			t.Fatalf("left a temporary note: %s", entry.Name())
		}
	}
}

func TestWriteScopeAndValidation(t *testing.T) {
	f := writableFixture(t)
	ctx := context.Background()
	original := readBytes(t, f, "03-Projects/alpha/index.md")
	revision := documentRevision(original)
	for _, tc := range []struct{ name, code string }{
		{"03-Projects/beta/index.md", "permission_denied"},
		{"03-Projects/alpha-private/index.md", "permission_denied"},
		{"05-Knowledge/Go/shared.md", "permission_denied"},
		{"03-Projects/alpha/.private.md", "permission_denied"},
		{"03-Projects/alpha/secrets/secret.md", "permission_denied"},
		{"03-Projects/alpha/assets/image.md", "permission_denied"},
		{"03-Projects/alpha/file.json", "permission_denied"},
		{"03-Projects/alpha/../beta/index.md", "invalid_argument"},
		{"03-Projects/alpha//new.md", "invalid_argument"},
		{filepath.Join(f.root, "03-Projects/alpha/absolute.md"), "invalid_argument"},
		{"03-Projects/alpha/missing/note.md", "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.store.Create(ctx, tc.name, "# Changed")
			assertCode(t, err, tc.code)
			_, err = f.store.Update(ctx, tc.name, "# Changed", revision)
			assertCode(t, err, tc.code)
		})
	}
	for _, tc := range []struct{ name, content, code string }{
		{"malformed frontmatter", "---\nvalue: [private-body", "invalid_argument"},
		{"invalid UTF8", string([]byte{0xff, 0xfe}), "invalid_argument"},
		{"NUL", "# Heading\x00body", "invalid_argument"},
		{"oversized", strings.Repeat("x", f.config.Limits.MaxDocumentBytes+1), "resource_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.store.Create(ctx, "03-Projects/alpha/new.md", tc.content)
			assertCode(t, err, tc.code)
			_, err = f.store.Update(ctx, "03-Projects/alpha/index.md", tc.content, revision)
			assertCode(t, err, tc.code)
			if strings.Contains(err.Error(), "private-body") {
				t.Fatal("validation error leaked submitted contents")
			}
		})
	}
	for _, invalid := range []string{"", "short", strings.Repeat("G", 64), strings.Repeat("A", 64)} {
		_, err := f.store.Update(ctx, "03-Projects/alpha/index.md", "# Changed", invalid)
		assertCode(t, err, "invalid_argument")
	}
	if readBytes(t, f, "03-Projects/alpha/index.md") != original || !strings.Contains(readBytes(t, f, "03-Projects/beta/index.md"), "BETAPRIVATEONLY") || readBytes(t, f, "05-Knowledge/Go/shared.md") != "# Go Shared\nsharedonly" {
		t.Fatal("rejected writes changed an existing note")
	}
	if _, err := os.Lstat(filepath.Join(f.root, "03-Projects/alpha/new.md")); !os.IsNotExist(err) {
		t.Fatalf("created a rejected note: %v", err)
	}
}

func TestWritesAreDisabledByDefault(t *testing.T) {
	f := makeFixture(t, true)
	_, err := f.store.Create(context.Background(), "03-Projects/alpha/new.md", "# Disabled")
	assertCode(t, err, "permission_denied")
	doc, err := f.store.Read(context.Background(), "03-Projects/alpha/index.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Update(context.Background(), doc.Path, "# Disabled", doc.Revision)
	assertCode(t, err, "permission_denied")
	if _, err := os.Lstat(filepath.Join(f.root, "03-Projects/alpha", writeLockName)); !os.IsNotExist(err) {
		t.Fatalf("disabled writes created a lock: %v", err)
	}
}

func TestWritesRequireReadableSerializedDocument(t *testing.T) {
	f := writableFixture(t)
	ctx := context.Background()
	name := "03-Projects/alpha/index.md"
	original := readBytes(t, f, name)
	// Each ampersand becomes six bytes in JSON's HTML-safe representation, so
	// this valid Markdown is below the raw limit but above the serialized cap.
	markdown := "# Note\n" + strings.Repeat("&", 200000)
	if len(markdown) >= f.config.Limits.MaxDocumentBytes {
		t.Fatal("test does not exercise the smaller serialized-output cap")
	}
	_, err := f.store.Create(ctx, "03-Projects/alpha/new.md", markdown)
	assertCode(t, err, "resource_limit")
	_, err = f.store.Update(ctx, name, markdown, documentRevision(original))
	assertCode(t, err, "resource_limit")
	if readBytes(t, f, name) != original {
		t.Fatal("unreadable replacement changed the original note")
	}
	if _, err := os.Lstat(filepath.Join(f.root, "03-Projects/alpha/new.md")); !os.IsNotExist(err) {
		t.Fatalf("created a note that cannot round-trip through document_read: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, "03-Projects/alpha", writeLockName)); !os.IsNotExist(err) {
		t.Fatalf("output validation changed the filesystem: %v", err)
	}
}

func TestWritesRejectSymlinksAndPreserveHardlinkTargets(t *testing.T) {
	f := writableFixture(t)
	ctx := context.Background()
	privatePath := filepath.Join(f.root, "03-Projects/beta/index.md")
	private := readBytes(t, f, "03-Projects/beta/index.md")
	base := filepath.Join(f.root, "03-Projects/alpha")
	if err := os.Symlink(privatePath, filepath.Join(base, "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(privatePath), filepath.Join(base, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"03-Projects/alpha/linked.md", "03-Projects/alpha/linked-dir/index.md"} {
		_, err := f.store.Create(ctx, name, "# Changed")
		assertCode(t, err, "permission_denied")
		_, err = f.store.Update(ctx, name, "# Changed", documentRevision(private))
		assertCode(t, err, "permission_denied")
	}
	if err := os.Link(privatePath, filepath.Join(base, "hardlink.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Update(ctx, "03-Projects/alpha/hardlink.md", "# New independent note", documentRevision(private)); err != nil {
		t.Fatal(err)
	}
	if readBytes(t, f, "03-Projects/beta/index.md") != private {
		t.Fatal("atomic replacement changed the external hardlink target")
	}
}

func TestCrossStoreUpdatesUseOneRevision(t *testing.T) {
	f := writableFixture(t)
	other, err := New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	doc, err := f.store.Read(context.Background(), "03-Projects/alpha/index.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for i, store := range []*Store{f.store, other} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.Update(context.Background(), doc.Path, "# New revision "+string(rune('A'+i)), doc.Revision)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if ErrorCode(err) == "conflict" {
			conflicts++
		} else {
			t.Fatalf("unexpected update failure: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes %d, conflicts %d", successes, conflicts)
	}
}

func TestCanceledWritesWaitForNeitherStoreNorProjectLock(t *testing.T) {
	f := writableFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.store.Create(ctx, "03-Projects/alpha/new.md", "# Canceled"); err != context.Canceled {
		t.Fatalf("canceled create: %v", err)
	}
	f.store.mu.Lock()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err := f.store.Create(ctx, "03-Projects/alpha/new.md", "# Timed out")
	cancel()
	f.store.mu.Unlock()
	if err != context.DeadlineExceeded {
		t.Fatalf("waiting for store mutex: %v", err)
	}
	lock, err := acquireProjectWriteLock(context.Background(), f.store.grants[0].root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, err = f.store.Create(ctx, "03-Projects/alpha/new.md", "# Timed out")
	cancel()
	lock.close()
	if err != context.DeadlineExceeded {
		t.Fatalf("waiting for project flock: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, "03-Projects/alpha/new.md")); !os.IsNotExist(err) {
		t.Fatalf("canceled write created a note: %v", err)
	}
}

func TestWriteLockRejectsSymlink(t *testing.T) {
	f := writableFixture(t)
	private := "03-Projects/beta/index.md"
	before := readBytes(t, f, private)
	if err := os.Symlink(filepath.Join(f.root, filepath.FromSlash(private)), filepath.Join(f.root, "03-Projects/alpha", writeLockName)); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.Create(context.Background(), "03-Projects/alpha/new.md", "# Note")
	assertCode(t, err, "permission_denied")
	if readBytes(t, f, private) != before {
		t.Fatal("write lock touched its symlink target")
	}
}

func TestDocumentRevisionHashesExactMarkdown(t *testing.T) {
	for _, markdown := range []string{"", "\ufeff# Unicode é中🙂\r\n", "---\nkey: value\n---\n# Body\n"} {
		doc, err := parseDocument("03-Projects/alpha/note.md", markdown)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(markdown))
		if doc.Revision != hex.EncodeToString(sum[:]) {
			t.Fatalf("wrong exact revision: %q", doc.Revision)
		}
	}
}

func TestCanceledReadersDoNotWaitForWriterProjectLock(t *testing.T) {
	f := writableFixture(t)
	held, err := acquireProjectWriteLock(context.Background(), f.store.grants[0].root)
	if err != nil {
		t.Fatal(err)
	}
	writerContext, cancelWriter := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() {
		_, err := f.store.Create(writerContext, "03-Projects/alpha/blocked.md", "# Pending write")
		writerDone <- err
	}()
	defer func() {
		cancelWriter()
		held.close()
		<-writerDone
	}()
	// Wait until the writer owns the mutex and is queued on the separately held
	// flock. Readers must be able to honor cancellation without that flock moving.
	writerHasMutex := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f.store.mu.TryRLock() {
			f.store.mu.RUnlock()
			time.Sleep(time.Millisecond)
			continue
		}
		writerHasMutex = true
		break
	}
	if !writerHasMutex {
		t.Fatal("writer did not acquire the store mutex")
	}
	for _, operation := range []struct {
		name string
		call func(context.Context) error
	}{
		{"Read", func(ctx context.Context) error {
			_, err := f.store.Read(ctx, "03-Projects/alpha/index.md", 0)
			return err
		}},
		{"List", func(ctx context.Context) error { _, err := f.store.List(ctx, ListOptions{}); return err }},
		{"Search", func(ctx context.Context) error {
			_, err := f.store.Search(ctx, SearchOptions{Query: "Alpha"})
			return err
		}},
		{"Decisions", func(ctx context.Context) error { _, err := f.store.Decisions(ctx, 0); return err }},
		{"Features", func(ctx context.Context) error { _, err := f.store.Features(ctx, "", 0); return err }},
		{"ProjectContext", func(ctx context.Context) error { _, err := f.store.ProjectContext(ctx); return err }},
	} {
		for _, queued := range []bool{false, true} {
			name := "already canceled"
			if queued {
				name = "cancel after queuing"
			}
			t.Run(operation.name+"/"+name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if !queued {
					cancel()
				}
				started := make(chan struct{})
				done := make(chan error, 1)
				go func() {
					close(started)
					done <- operation.call(ctx)
				}()
				<-started
				if queued {
					select {
					case err := <-done:
						t.Fatalf("reader returned before cancellation despite writer lock: %v", err)
					case <-time.After(15 * time.Millisecond):
					}
					cancel()
				}
				select {
				case err := <-done:
					if err != context.Canceled {
						t.Fatalf("canceled reader: %v", err)
					}
				case <-time.After(500 * time.Millisecond):
					t.Fatal("canceled reader waited behind the blocked writer")
				}
			})
		}
	}
}
