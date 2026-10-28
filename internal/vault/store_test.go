package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

type fixture struct {
	root   string
	config *config.Scope
	store  *Store
}

func makeFixture(t *testing.T, shared bool) fixture {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"03-Projects/alpha/features", "03-Projects/alpha/decisions", "03-Projects/beta/features", "03-Projects/beta/decisions", "05-Knowledge/Go", "05-Knowledge/Security", "03-Projects/alpha-private"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Scope{VaultRoot: root, ProjectID: "alpha", ProjectRoot: "03-Projects/alpha", Limits: config.DefaultLimits()}
	if shared {
		cfg.SharedReadPaths = []string{"05-Knowledge/Go"}
	}
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f := fixture{root: root, config: cfg, store: store}
	f.write(t, "03-Projects/alpha/index.md", "---\ntype: project\nproject: alpha\nstatus: planning\nupdated: 2026-10-23\n---\n# Alpha\n\nAlpha private knowledge.\n\n[[03-Projects/alpha/features/a|Feature A]]\n[[03-Projects/beta/index|Other]]\n")
	f.write(t, "03-Projects/alpha/features/a.md", "---\ntype: feature\nstatus: planned\n---\n# Feature A\nAlpha private uniquealpha")
	f.write(t, "03-Projects/alpha/features/z.md", "---\ntype: feature\nstatus: complete\n---\n# Feature Z\nMore alpha")
	f.write(t, "03-Projects/alpha/decisions/ADR-002.md", "---\ntype: adr\n---\n# Later decision\nAlpha private decision.")
	f.write(t, "03-Projects/alpha/decisions/ADR-001.md", "# First decision\nAlpha private decision.")
	f.write(t, "03-Projects/beta/index.md", "# Beta\nBETAPRIVATEONLY")
	f.write(t, "03-Projects/beta/features/b.md", "# B\nBETAPRIVATEONLY")
	f.write(t, "03-Projects/beta/decisions/b.md", "# B decision\nBETAPRIVATEONLY")
	f.write(t, "03-Projects/alpha-private/index.md", "# Prefix\nLOOKALIKEPRIVATE")
	f.write(t, "05-Knowledge/Go/shared.md", "# Go Shared\nsharedonly")
	f.write(t, "05-Knowledge/Security/private.md", "# Security Shared\nsecretshared")
	return f
}

func (f fixture) write(t *testing.T, name, data string) {
	t.Helper()
	full := filepath.Join(f.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || ErrorCode(err) != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestProjectIsolationAcrossRetrieval(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	context, err := f.store.ProjectContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if context.Project != "alpha" || context.Title != "Alpha" || !strings.Contains(context.Summary, "Alpha private") {
		t.Fatalf("wrong project context %+v", context)
	}
	for _, name := range []string{"03-Projects/beta/index.md", "03-Projects/alpha-private/index.md", "05-Knowledge/Go/shared.md"} {
		_, err := f.store.Read(ctx, name, 0)
		assertCode(t, err, "permission_denied")
	}
	list, err := f.store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 5 {
		t.Fatalf("got %d documents: %+v", len(list.Documents), list)
	}
	search, err := f.store.Search(ctx, SearchOptions{Query: "BETAPRIVATEONLY"})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Matches) != 0 {
		t.Fatal("cross-project search leak")
	}
	features, err := f.store.Features(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(features.Documents) != 2 {
		t.Fatalf("feature count: %+v", features)
	}
	decisions, err := f.store.Decisions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions.Documents) != 2 || !strings.HasSuffix(decisions.Documents[0].Path, "ADR-001.md") {
		t.Fatalf("decision sorting: %+v", decisions)
	}
	for _, result := range []any{context, list, search, features, decisions} {
		data, _ := json.Marshal(result)
		if strings.Contains(string(data), "BETAPRIVATEONLY") || strings.Contains(string(data), "LOOKALIKEPRIVATE") || strings.Contains(string(data), f.root) {
			t.Fatalf("leaked private data or absolute root: %s", data)
		}
	}
}

func TestProjectContextResolvesOnlyAuthorizedLinks(t *testing.T) {
	f := makeFixture(t, true)
	f.write(t, "03-Projects/alpha/index.md", "# Alpha\n\n[[03-Projects/index|Projects]]\n[[00-Dashboard/Home|Home]]\n[[03-Projects/beta/index|Other]]\n[[features/a|Relative feature]]\n[[03-Projects/alpha/decisions/ADR-001|Decision]]\n[[05-Knowledge/Go/shared|Granted]]\n[[05-Knowledge/Security/private|Denied]]\n[[../beta/index|Traversal]]\n")
	result, err := f.store.ProjectContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"03-Projects/alpha/features/a.md", "03-Projects/alpha/decisions/ADR-001.md", "05-Knowledge/Go/shared.md"}
	if strings.Join(result.Links, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected links: %v", result.Links)
	}
}

func TestPathArgumentRejection(t *testing.T) {
	f := makeFixture(t, false)
	for _, name := range []string{"", "../beta/index.md", "03-Projects/alpha/../../beta/index.md", "03-Projects/alpha/../beta/index.md", "03-Projects/alpha//index.md", "03-Projects/alpha/./index.md", "/etc/passwd", filepath.Join(f.root, "03-Projects/alpha/index.md"), "03-Projects/alpha/%2e%2e/beta.md", "03-Projects/alpha/..%2findex.md", "03-Projects/alpha/evil\x00.md", "03-Projects\\alpha\\index.md", "C:/Users/note.md", "03-Projects/alpha/index.md:stream"} {
		t.Run(name, func(t *testing.T) {
			_, err := f.store.Read(context.Background(), name, 0)
			assertCode(t, err, "invalid_argument")
		})
	}
	for _, section := range []string{"../beta", "03-Projects/alpha/../../beta", "/tmp", "features/..", "shared:05-Knowledge/Go/.."} {
		if _, err := f.store.List(context.Background(), ListOptions{Section: section}); err == nil {
			t.Fatalf("accepted section %q", section)
		}
		if _, err := f.store.Search(context.Background(), SearchOptions{Query: "alpha", Section: section}); err == nil {
			t.Fatalf("accepted search section %q", section)
		}
	}
}

func TestSharedGrantsAndDefaultSearch(t *testing.T) {
	f := makeFixture(t, true)
	ctx := context.Background()
	doc, err := f.store.Read(ctx, "05-Knowledge/Go/shared.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Markdown, "sharedonly") {
		t.Fatal("shared note missing")
	}
	_, err = f.store.Read(ctx, "05-Knowledge/Security/private.md", 0)
	assertCode(t, err, "permission_denied")
	result, err := f.store.Search(ctx, SearchOptions{Query: "sharedonly"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 0 {
		t.Fatal("default search expanded to shared grants")
	}
	result, err = f.store.Search(ctx, SearchOptions{Query: "sharedonly", Section: "shared:05-Knowledge/Go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Path != "05-Knowledge/Go/shared.md" {
		t.Fatalf("wrong shared result %+v", result)
	}
	list, err := f.store.List(ctx, ListOptions{Section: "shared:05-Knowledge/Go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 1 {
		t.Fatalf("wrong shared list %+v", list)
	}
	for _, section := range []string{"shared:05-Knowledge", "shared:05-Knowledge/Security", "shared:05-Knowledge/Go/nested"} {
		_, err := f.store.List(ctx, ListOptions{Section: section})
		assertCode(t, err, "permission_denied")
	}
}

func TestHiddenAttachmentsAndNonMarkdownExcluded(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	for _, name := range []string{".hidden.md", ".obsidian/settings.md", ".git/config.md", ".env", ".ssh/id.md", "secrets/token.md", "attachments/image.md", "assets/plan.md", "node_modules/note.md", "paper.pdf", "image.png", "note.txt"} {
		full := "03-Projects/alpha/" + name
		f.write(t, full, "EXCLUDETHISPRIVATE")
		_, err := f.store.Read(ctx, full, 0)
		assertCode(t, err, "permission_denied")
	}
	list, err := f.store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 5 {
		t.Fatalf("excluded files listed %+v", list)
	}
	result, err := f.store.Search(ctx, SearchOptions{Query: "EXCLUDETHISPRIVATE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("excluded files searched %+v", result)
	}
}

func TestSymlinkFileParentAndRootDenied(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	base := filepath.Join(f.root, "03-Projects/alpha")
	links := map[string]string{"leak.md": "../beta/index.md", "internal.md": "index.md", "linked": "../beta"}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(base, name)); err != nil {
			t.Skip(err)
		}
	}
	for _, name := range []string{"leak.md", "internal.md", "linked/index.md"} {
		_, err := f.store.Read(ctx, "03-Projects/alpha/"+name, 0)
		assertCode(t, err, "permission_denied")
	}
	list, err := f.store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Documents) != 5 {
		t.Fatalf("symlink entries surfaced: %+v", list)
	}
	search, err := f.store.Search(ctx, SearchOptions{Query: "BETAPRIVATEONLY"})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Matches) != 0 {
		t.Fatal("symlink search leak")
	}
	if err := os.Rename(base, base+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("beta", base); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Read(ctx, "03-Projects/alpha/index.md", 0)
	assertCode(t, err, "permission_denied")
	_, err = f.store.ProjectContext(ctx)
	assertCode(t, err, "permission_denied")
}

func TestListPaginationIsStableAndSectionBound(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	var paths []string
	cursor := ""
	for {
		result, err := f.store.List(ctx, ListOptions{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Documents) > 2 {
			t.Fatal("page cap exceeded")
		}
		for _, doc := range result.Documents {
			paths = append(paths, doc.Path)
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
		if len(paths) > 10 {
			t.Fatal("pagination loop")
		}
	}
	if len(paths) != 5 {
		t.Fatalf("wrong total %v", paths)
	}
	for i := 1; i < len(paths); i++ {
		if paths[i] <= paths[i-1] {
			t.Fatalf("unstable ordering %v", paths)
		}
	}
	page, err := f.store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.List(ctx, ListOptions{Section: "features", Cursor: page.NextCursor})
	assertCode(t, err, "invalid_argument")
	for _, bad := range []string{"not-a-cursor", page.NextCursor + "x", strings.Repeat("x", 3000)} {
		_, err := f.store.List(ctx, ListOptions{Cursor: bad})
		assertCode(t, err, "invalid_argument")
	}
	other, err := New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, err = other.List(ctx, ListOptions{Cursor: page.NextCursor})
	assertCode(t, err, "invalid_argument")
}

func TestPaginationAcceptsLongSectionsAndJSONEscaping(t *testing.T) {
	for _, character := range []string{"a", "&"} {
		t.Run(character, func(t *testing.T) {
			f := makeFixture(t, false)
			component := strings.Repeat(character, 200)
			section := strings.Join([]string{component, component, component, component}, "/")
			base := "03-Projects/alpha/" + section + "/"
			f.write(t, base+"a.md", "# A\n")
			f.write(t, base+"b.md", "# B\n")
			first, err := f.store.List(context.Background(), ListOptions{Section: section, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Documents) != 1 || first.Documents[0].Path != base+"a.md" {
				t.Fatalf("wrong first page: %+v", first)
			}
			if len(first.NextCursor) <= 2048 || len(first.NextCursor) > maxCursorBytes {
				t.Fatalf("unexpected cursor length: %d", len(first.NextCursor))
			}
			second, err := f.store.List(context.Background(), ListOptions{Section: section, Limit: 1, Cursor: first.NextCursor})
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Documents) != 1 || second.Documents[0].Path != base+"b.md" || second.NextCursor != "" {
				t.Fatalf("wrong second page: %+v", second)
			}
			_, err = f.store.List(context.Background(), ListOptions{Section: "features", Cursor: first.NextCursor})
			assertCode(t, err, "invalid_argument")
		})
	}
}

func TestMetadataMalformedEmptyAndUnicodeTruncation(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	f.write(t, "03-Projects/alpha/unicode.md", "é中🙂abc")
	doc, err := f.store.Read(ctx, "03-Projects/alpha/unicode.md", 3)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Markdown != "é中🙂" || !doc.Truncated {
		t.Fatalf("bad rune truncation %+v", doc)
	}
	doc, err = f.store.Read(ctx, "03-Projects/alpha/index.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Type != "project" || doc.Updated != "2026-10-23" || doc.Frontmatter["project"] != "alpha" {
		t.Fatalf("wrong metadata %+v", doc)
	}
	f.write(t, "03-Projects/alpha/empty.md", "")
	doc, err = f.store.Read(ctx, "03-Projects/alpha/empty.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "empty" || doc.Markdown != "" || doc.Frontmatter == nil || doc.Truncated {
		t.Fatalf("wrong empty note %+v", doc)
	}
	for _, data := range []string{"---\nkey: [SENSITIVEVALUE\n---\n# Bad", "---\nkey: 1\nkey: 2\n---\n# Bad", "---\n- scalar\n---\n# Bad", "---\nkey: value\n# no closing delimiter", "---\nnested:\n  1: value\n---\n"} {
		f.write(t, "03-Projects/alpha/bad.md", data)
		_, err := f.store.Read(ctx, "03-Projects/alpha/bad.md", 0)
		if err == nil {
			t.Fatalf("malformed metadata accepted: %q", data)
		}
		assertCode(t, err, "invalid_argument")
		if strings.Contains(err.Error(), "SENSITIVEVALUE") {
			t.Fatalf("note content in error: %v", err)
		}
	}
	f.write(t, "03-Projects/alpha/bad.md", string([]byte{0xff, 0xfe}))
	_, err = f.store.Read(ctx, "03-Projects/alpha/bad.md", 0)
	assertCode(t, err, "invalid_argument")
}

func TestResourceLimitsAndCancellation(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	_, err := f.store.List(ctx, ListOptions{Limit: 26})
	assertCode(t, err, "invalid_argument")
	_, err = f.store.List(ctx, ListOptions{Limit: -1})
	assertCode(t, err, "invalid_argument")
	_, err = f.store.Read(ctx, "03-Projects/alpha/index.md", -1)
	assertCode(t, err, "invalid_argument")
	_, err = f.store.Read(ctx, "03-Projects/alpha/index.md", f.config.Limits.MaxDocumentBytes+1)
	assertCode(t, err, "invalid_argument")
	for _, query := range []string{"", "  ", strings.Repeat("x", 1025), "x\x00y"} {
		_, err := f.store.Search(ctx, SearchOptions{Query: query})
		assertCode(t, err, "invalid_argument")
	}
	f.write(t, "03-Projects/alpha/oversize.md", strings.Repeat("x", f.config.Limits.MaxDocumentBytes+1))
	_, err = f.store.Read(ctx, "03-Projects/alpha/oversize.md", 10)
	assertCode(t, err, "resource_limit")
	if err := os.Remove(filepath.Join(f.root, "03-Projects/alpha/oversize.md")); err != nil {
		t.Fatal(err)
	}
	f.config.Limits.MaxSearchScanFiles = 2
	limited, err := New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	_, err = limited.List(ctx, ListOptions{})
	assertCode(t, err, "resource_limit")
	_, err = limited.Search(ctx, SearchOptions{Query: "alpha"})
	assertCode(t, err, "resource_limit")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	checks := []func() error{
		func() error { _, err := f.store.Read(cancelled, "03-Projects/alpha/index.md", 0); return err },
		func() error { _, err := f.store.List(cancelled, ListOptions{}); return err },
		func() error { _, err := f.store.Search(cancelled, SearchOptions{Query: "alpha"}); return err },
		func() error { _, err := f.store.ProjectContext(cancelled); return err },
		func() error { _, err := f.store.Features(cancelled, "", 0); return err },
		func() error { _, err := f.store.Decisions(cancelled, 0); return err },
	}
	for i, check := range checks {
		if err := check(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel check %d: %v", i, err)
		}
	}
}

func TestSearchTokensResultLimitAndFeatureFilter(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	result, err := f.store.Search(ctx, SearchOptions{Query: "PRIVATE ALPHA", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || !result.Truncated {
		t.Fatalf("wrong bounded token search %+v", result)
	}
	result, err = f.store.Search(ctx, SearchOptions{Query: "UNIQueALPha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || !strings.HasSuffix(result.Matches[0].Path, "features/a.md") {
		t.Fatalf("wrong case insensitive search %+v", result)
	}
	features, err := f.store.Features(ctx, "complete", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(features.Documents) != 1 || features.Documents[0].Status != "complete" {
		t.Fatalf("wrong status filter %+v", features)
	}
	features, err = f.store.Features(ctx, "nonexistent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(features.Documents) != 0 {
		t.Fatalf("filter returned wrong notes %+v", features)
	}
}

func TestMissingNotesEmptyProjectAndClosedStore(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	_, err := f.store.Read(ctx, "03-Projects/alpha/missing.md", 0)
	assertCode(t, err, "not_found")
	empty := filepath.Join(f.root, "03-Projects/empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := *f.config
	cfg.ProjectID = "empty"
	cfg.ProjectRoot = "03-Projects/empty"
	store, err := New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, ListOptions{})
	if err != nil || len(list.Documents) != 0 {
		t.Fatalf("empty list: %+v %v", list, err)
	}
	features, err := store.Features(ctx, "", 0)
	if err != nil || len(features.Documents) != 0 {
		t.Fatalf("empty features: %+v %v", features, err)
	}
	decisions, err := store.Decisions(ctx, 0)
	if err != nil || len(decisions.Documents) != 0 {
		t.Fatalf("empty decisions: %+v %v", decisions, err)
	}
	_, err = store.ProjectContext(ctx)
	assertCode(t, err, "not_found")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = store.List(ctx, ListOptions{})
	assertCode(t, err, "internal_error")
}

func TestMetadataBoundsAndSafeDiagnostics(t *testing.T) {
	f := makeFixture(t, false)
	ctx := context.Background()
	f.write(t, "03-Projects/alpha/metadata.md", fmt.Sprintf("---\nsummary: %q\n---\n# Title", strings.Repeat("x", maxMetadataBytes)))
	_, err := f.store.Read(ctx, "03-Projects/alpha/metadata.md", 0)
	assertCode(t, err, "resource_limit")
	if strings.Contains(err.Error(), f.root) || len(err.Error()) > 1024 {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
}

func TestFrontmatterAliasGraphsAreBounded(t *testing.T) {
	f := makeFixture(t, false)
	var amplified strings.Builder
	amplified.WriteString("---\na0: &a0 [value, value]\n")
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&amplified, "a%d: &a%d [*a%d, *a%d, *a%d, *a%d]\n", i, i, i-1, i-1, i-1, i-1)
	}
	amplified.WriteString("---\n# Bounded aliases\n")
	f.write(t, "03-Projects/alpha/aliases.md", amplified.String())
	_, err := f.store.Read(context.Background(), "03-Projects/alpha/aliases.md", 0)
	assertCode(t, err, "resource_limit")
	f.write(t, "03-Projects/alpha/aliases.md", "---\ncycle: &cycle [*cycle]\n---\n")
	_, err = f.store.Read(context.Background(), "03-Projects/alpha/aliases.md", 0)
	assertCode(t, err, "invalid_argument")
	f.write(t, "03-Projects/alpha/aliases.md", "---\noriginal: &value hello\ncopy: *value\n---\n# Valid aliases\n")
	doc, err := f.store.Read(context.Background(), "03-Projects/alpha/aliases.md", 0)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Frontmatter["copy"] != "hello" {
		t.Fatalf("valid alias was not decoded: %+v", doc.Frontmatter)
	}
}
