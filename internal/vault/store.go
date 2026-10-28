package vault

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jsainthilaire/vault-bridge/internal/config"
	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

const (
	maxQueryBytes    = 1024
	maxExcerptRunes  = 4000
	maxMetadataBytes = 32768
	maxOutputBytes   = 1 << 20
	// A cursor contains an allowed section and vault path (about 1KiB each).
	// JSON can escape each byte as six bytes before base64 adds another third.
	maxCursorBytes = 24 << 10
)

type grant struct {
	path string
	root *os.Root
	info fs.FileInfo
}

// Store keeps one descriptor per authorized root. The handles keep reads
// contained even when a pathname is concurrently replaced by an escaping
// symlink. Component checks reject ordinary symlinks, including in-vault ones.
// They cannot guarantee an immutable snapshot under concurrent edits, reject
// hard links, or detect administrator-created bind mounts.
type Store struct {
	mu        sync.RWMutex
	closed    bool
	scope     config.Scope
	vault     *os.Root
	grants    []grant
	cursorKey [32]byte
}

func New(cfg *config.Scope) (*Store, error) {
	if cfg == nil || !config.ValidProjectID(cfg.ProjectID) {
		return nil, scope.Err("invalid_argument", "an explicit validated project scope is required")
	}
	if err := config.ValidateLimits(cfg.Limits); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(cfg.VaultRoot) {
		return nil, scope.Err("invalid_argument", "vault_root must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(cfg.VaultRoot)
	if err != nil {
		return nil, scope.FSError(err, "resolve vault_root")
	}
	vault, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, scope.FSError(err, "open vault_root")
	}
	s := &Store{scope: *cfg, vault: vault}
	s.scope.VaultRoot = canonical
	s.scope.SharedReadPaths = append([]string(nil), cfg.SharedReadPaths...)
	if _, err := rand.Read(s.cursorKey[:]); err != nil {
		vault.Close()
		return nil, scope.Err("internal_error", "cannot initialize pagination")
	}
	names := append([]string{cfg.ProjectRoot}, cfg.SharedReadPaths...)
	for i, name := range names {
		parent := "03-Projects"
		if i > 0 {
			parent = "05-Knowledge"
		}
		if err := scope.RelativePath(name); err != nil {
			s.Close()
			return nil, err
		}
		if !scope.Visible(name) || name == parent || !scope.Beneath(parent, name) {
			s.Close()
			return nil, scope.Err("permission_denied", "scope must name an explicit visible project or knowledge subtree")
		}
		if _, err := scope.CheckComponents(vault, name, true); err != nil {
			s.Close()
			return nil, err
		}
		root, err := openDirectory(vault, name)
		if err != nil {
			s.Close()
			return nil, err
		}
		info, err := root.Stat(".")
		if err != nil {
			root.Close()
			s.Close()
			return nil, scope.FSError(err, "inspect scope")
		}
		s.grants = append(s.grants, grant{path: name, root: root, info: info})
	}
	return s, nil
}

// openDirectory verifies identity at each open and pins parent descriptors.
// It closes the transient parents, leaving only the final confined descriptor.
func openDirectory(parent *os.Root, name string) (*os.Root, error) {
	current := parent
	var owned *os.Root
	for _, part := range strings.Split(name, "/") {
		before, err := current.Lstat(part)
		if err != nil {
			if owned != nil {
				owned.Close()
			}
			return nil, scope.FSError(err, "inspect scope directory")
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			if owned != nil {
				owned.Close()
			}
			return nil, scope.Err("permission_denied", "scope directories must not be symbolic links")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			if owned != nil {
				owned.Close()
			}
			return nil, scope.FSError(err, "open scope directory")
		}
		after, statErr := next.Stat(".")
		check, checkErr := current.Lstat(part)
		if statErr != nil || checkErr != nil || check.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, after) || !os.SameFile(before, check) {
			next.Close()
			if owned != nil {
				owned.Close()
			}
			return nil, scope.Err("permission_denied", "scope directory changed during access")
		}
		if owned != nil {
			owned.Close()
		}
		owned, current = next, next
	}
	return owned, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var result error
	for _, g := range s.grants {
		result = errors.Join(result, g.root.Close())
	}
	if s.vault != nil {
		result = errors.Join(result, s.vault.Close())
	}
	return result
}

func (s *Store) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return scope.Err("internal_error", "store is closed")
	}
	return nil
}

// lockReader lets canceled requests leave the queue even if a writer is waiting
// on another process's project lock while holding the store's exclusive mutex.
func (s *Store) lockReader(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.mu.TryRLock() {
			return nil
		}
		if err := waitForWriteLock(ctx); err != nil {
			return err
		}
	}
}

func (s *Store) limit(value int) (int, error) {
	if value == 0 {
		return s.scope.Limits.MaxSearchResults, nil
	}
	if value < 0 || value > s.scope.Limits.MaxSearchResults {
		return 0, scope.Err("invalid_argument", "limit must be positive and no greater than configured max_search_results")
	}
	return value, nil
}

func (s *Store) checkGrant(g grant) error {
	info, err := scope.CheckComponents(s.vault, g.path, true)
	if err != nil {
		return err
	}
	if !os.SameFile(info, g.info) {
		return scope.Err("permission_denied", "authorized directory changed; restart the server after reviewing configuration")
	}
	return nil
}

func (s *Store) resolve(name string) (grant, string, error) {
	if err := scope.RelativePath(name); err != nil {
		return grant{}, "", err
	}
	if !scope.Visible(name) || strings.ToLower(path.Ext(name)) != ".md" {
		return grant{}, "", scope.Err("permission_denied", "only visible regular Markdown notes may be read")
	}
	for _, g := range s.grants {
		if name != g.path && scope.Beneath(g.path, name) {
			rel, _ := filepath.Rel(filepath.FromSlash(g.path), filepath.FromSlash(name))
			return g, filepath.ToSlash(rel), nil
		}
	}
	return grant{}, "", scope.Err("permission_denied", "path is outside the active project's grants")
}

func (s *Store) section(section string) (grant, string, string, error) {
	if section == "" || section == "project" {
		return s.grants[0], ".", "project", nil
	}
	if strings.HasPrefix(section, "shared:") {
		name := strings.TrimPrefix(section, "shared:")
		if err := scope.RelativePath(name); err != nil {
			return grant{}, "", "", err
		}
		for _, g := range s.grants[1:] {
			if name == g.path {
				return g, ".", section, nil
			}
		}
		return grant{}, "", "", scope.Err("permission_denied", "shared section must exactly match an explicit shared_read_paths grant")
	}
	if err := scope.RelativePath(section); err != nil {
		return grant{}, "", "", err
	}
	if !scope.Visible(section) {
		return grant{}, "", "", scope.Err("permission_denied", "hidden, secret and attachment sections are excluded")
	}
	return s.grants[0], section, "project:" + section, nil
}

func (s *Store) Read(ctx context.Context, name string, maxChars int) (*Document, error) {
	if err := s.lockReader(ctx); err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if maxChars < 0 || maxChars > s.scope.Limits.MaxDocumentBytes {
		return nil, scope.Err("invalid_argument", "max_chars must be positive and no greater than max_document_bytes")
	}
	doc, err := s.read(ctx, name)
	if err != nil {
		return nil, err
	}
	if maxChars > 0 {
		doc.Markdown, doc.Truncated = truncateRunes(doc.Markdown, maxChars)
	}
	if err := outputBound(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Store) read(ctx context.Context, name string) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, rel, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	if err := s.checkGrant(g); err != nil {
		return nil, err
	}
	before, err := scope.CheckComponents(g.root, rel, false)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, scope.Err("permission_denied", "only regular Markdown files may be read")
	}
	if before.Size() > int64(s.scope.Limits.MaxDocumentBytes) {
		return nil, scope.Err("resource_limit", "note exceeds max_document_bytes")
	}
	f, err := openNote(g.root, rel)
	if err != nil {
		return nil, scope.FSError(err, "open note")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, scope.FSError(err, "inspect note")
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, scope.Err("permission_denied", "note changed during access")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(s.scope.Limits.MaxDocumentBytes)+1))
	if err != nil {
		return nil, scope.FSError(err, "read note")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > s.scope.Limits.MaxDocumentBytes {
		return nil, scope.Err("resource_limit", "note exceeds max_document_bytes")
	}
	if !utf8.Valid(data) {
		return nil, scope.Err("invalid_argument", "note must contain valid UTF-8 Markdown")
	}
	after, err := scope.CheckComponents(g.root, rel, false)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) {
		return nil, scope.Err("permission_denied", "note changed during access")
	}
	if err := s.checkGrant(g); err != nil {
		return nil, err
	}
	doc, err := parseDocument(name, string(data))
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// walk bounds every observed entry (including skipped non-Markdown files), not
// only returned notes. Directories are read in bounded batches and sorted after
// the scan, so an enormous directory cannot be loaded by WalkDir/ReadDir(-1).
func (s *Store) walk(ctx context.Context, g grant, subtree string) ([]string, error) {
	if err := s.checkGrant(g); err != nil {
		return nil, err
	}
	if _, err := scope.CheckComponents(g.root, subtree, true); err != nil {
		return nil, err
	}
	files := make([]string, 0)
	count := 0
	var visit func(string, int) error
	visit = func(dir string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return scope.Err("resource_limit", "directory nesting exceeds 64 levels")
		}
		before, err := scope.CheckComponents(g.root, dir, true)
		if err != nil {
			return err
		}
		f, err := g.root.Open(dir)
		if err != nil {
			return scope.FSError(err, "open section")
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil {
			return scope.FSError(err, "inspect section")
		}
		if !opened.IsDir() || !os.SameFile(before, opened) {
			return scope.Err("permission_denied", "section changed during access")
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, readErr := f.ReadDir(128)
			for _, entry := range entries {
				count++
				if count > s.scope.Limits.MaxSearchScanFiles {
					return scope.Err("resource_limit", "scan exceeds max_search_scan_files (counts all encountered entries)")
				}
				name := entry.Name()
				if err := scope.RelativePath(name); err != nil || !scope.Visible(name) {
					continue
				}
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				rel := name
				if dir != "." {
					rel = dir + "/" + name
				}
				if len(rel) > 1024 {
					return scope.Err("resource_limit", "note path exceeds 1024 bytes")
				}
				if entry.IsDir() {
					if err := visit(rel, depth+1); err != nil {
						return err
					}
				} else if strings.EqualFold(path.Ext(name), ".md") {
					info, err := scope.CheckComponents(g.root, rel, false)
					if err != nil {
						return err
					}
					if info.Mode().IsRegular() {
						files = append(files, g.path+"/"+rel)
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return scope.FSError(readErr, "scan section")
			}
		}
		after, err := scope.CheckComponents(g.root, dir, true)
		if err != nil {
			return err
		}
		if !os.SameFile(opened, after) {
			return scope.Err("permission_denied", "section changed during access")
		}
		return nil
	}
	if err := visit(subtree, 0); err != nil {
		return nil, err
	}
	if err := s.checkGrant(g); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func info(doc *Document) DocumentInfo {
	return DocumentInfo{Path: doc.Path, Title: doc.Title, Type: doc.Type, Status: doc.Status, Updated: doc.Updated}
}

type cursorData struct {
	Section string `json:"s"`
	Last    string `json:"p"`
}

func (s *Store) encodeCursor(section, last string) string {
	data, _ := json.Marshal(cursorData{Section: section, Last: last})
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(append(data, mac.Sum(nil)...))
}

func (s *Store) decodeCursor(token, section string) (string, error) {
	if token == "" {
		return "", nil
	}
	bad := scope.Err("invalid_argument", "invalid cursor: use next_cursor from this server and the same section")
	if len(token) > maxCursorBytes {
		return "", bad
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) <= sha256.Size {
		return "", bad
	}
	payload, signature := data[:len(data)-sha256.Size], data[len(data)-sha256.Size:]
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", bad
	}
	var cursor cursorData
	if json.Unmarshal(payload, &cursor) != nil || cursor.Section != section || scope.RelativePath(cursor.Last) != nil {
		return "", bad
	}
	return cursor.Last, nil
}

func (s *Store) List(ctx context.Context, opts ListOptions) (*ListResult, error) {
	if err := s.lockReader(ctx); err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	limit, err := s.limit(opts.Limit)
	if err != nil {
		return nil, err
	}
	g, subtree, section, err := s.section(opts.Section)
	if err != nil {
		return nil, err
	}
	last, err := s.decodeCursor(opts.Cursor, section)
	if err != nil {
		return nil, err
	}
	files, err := s.walk(ctx, g, subtree)
	if err != nil {
		return nil, err
	}
	result := &ListResult{Documents: make([]DocumentInfo, 0)}
	start := sort.SearchStrings(files, last)
	if start < len(files) && files[start] == last {
		start++
	}
	for i := start; i < len(files) && len(result.Documents) < limit; i++ {
		doc, err := s.read(ctx, files[i])
		if err != nil {
			return nil, err
		}
		result.Documents = append(result.Documents, info(doc))
	}
	if start+len(result.Documents) < len(files) && len(result.Documents) > 0 {
		result.NextCursor = s.encodeCursor(section, result.Documents[len(result.Documents)-1].Path)
	}
	if err := outputBound(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	if err := s.lockReader(ctx); err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if !utf8.ValidString(opts.Query) || len(opts.Query) > maxQueryBytes || strings.TrimSpace(opts.Query) == "" || strings.ContainsRune(opts.Query, 0) {
		return nil, scope.Err("invalid_argument", "query must contain 1 to 1024 bytes of UTF-8 text")
	}
	limit, err := s.limit(opts.Limit)
	if err != nil {
		return nil, err
	}
	g, subtree, _, err := s.section(opts.Section)
	if err != nil {
		return nil, err
	}
	files, err := s.walk(ctx, g, subtree)
	if err != nil {
		return nil, err
	}
	tokens := strings.Fields(strings.ToLower(opts.Query))
	result := &SearchResult{Matches: make([]SearchMatch, 0)}
	for _, name := range files {
		doc, err := s.read(ctx, name)
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(doc.Markdown)
		matches := true
		for _, token := range tokens {
			if !strings.Contains(lower, token) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		if len(result.Matches) == limit {
			result.Truncated = true
			break
		}
		result.Matches = append(result.Matches, SearchMatch{Path: name, Title: doc.Title, Snippet: snippet(doc.Markdown, tokens[0])})
	}
	if err := outputBound(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) Decisions(ctx context.Context, limit int) (*ListResult, error) {
	return s.specialList(ctx, "decisions", "", limit)
}

func (s *Store) Features(ctx context.Context, status string, limit int) (*ListResult, error) {
	if len(status) > 128 || !utf8.ValidString(status) || strings.ContainsAny(status, "\x00\r\n") {
		return nil, scope.Err("invalid_argument", "status must be at most 128 bytes on one line")
	}
	return s.specialList(ctx, "features", status, limit)
}

func (s *Store) specialList(ctx context.Context, section, status string, value int) (*ListResult, error) {
	if err := s.lockReader(ctx); err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	limit, err := s.limit(value)
	if err != nil {
		return nil, err
	}
	files, err := s.walk(ctx, s.grants[0], section)
	if err != nil {
		if scope.ErrorCode(err) == "not_found" {
			return &ListResult{Documents: []DocumentInfo{}}, nil
		}
		return nil, err
	}
	result := &ListResult{Documents: make([]DocumentInfo, 0)}
	for _, name := range files {
		doc, err := s.read(ctx, name)
		if err != nil {
			return nil, err
		}
		if status != "" && doc.Status != status {
			continue
		}
		result.Documents = append(result.Documents, info(doc))
		if len(result.Documents) == limit {
			break
		}
	}
	if err := outputBound(result); err != nil {
		return nil, err
	}
	return result, nil
}

func outputBound(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return scope.Err("invalid_argument", "frontmatter must be representable as JSON")
	}
	if len(data) > maxOutputBytes {
		return scope.Err("resource_limit", "result exceeds the 1048576-byte output limit; request fewer results or max_chars")
	}
	return nil
}

func truncateRunes(text string, limit int) (string, bool) {
	count := 0
	for offset := range text {
		if count == limit {
			return text[:offset], true
		}
		count++
	}
	return text, false
}

func snippet(text, token string) string {
	// Locate by rune index to avoid offsets changing under Unicode lowercasing.
	runes := []rune(text)
	lower := strings.ToLower(text)
	index := 0
	if byteIndex := strings.Index(lower, token); byteIndex >= 0 {
		index = utf8.RuneCountInString(lower[:byteIndex])
	}
	start := max(0, index-80)
	end := min(len(runes), start+240)
	value := string(runes[start:end])
	if start > 0 {
		value = "…" + value
	}
	if end < len(runes) {
		value += "…"
	}
	return value
}
