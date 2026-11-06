package vault

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

const writeLockName = ".vault-bridge-write.lock"

func documentRevision(markdown string) string {
	digest := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(digest[:])
}

// Create adds one authorized Markdown note without replacing an existing entry.
// Shared writes require both --shared and --allow-writes. Parents must exist.
func (s *Store) Create(ctx context.Context, name, markdown string) (*WriteResult, error) {
	return s.write(ctx, name, markdown, "", false)
}

// Update replaces a project Markdown note only when its complete content still
// matches expectedRevision from document_read. It never truncates in place.
// Project writers share a project-root lock; shared writers lock the note parent.
// External editors must not modify notes
// during a write: ordinary filesystems provide no atomic content-hash CAS.
func (s *Store) Update(ctx context.Context, name, markdown, expectedRevision string) (*WriteResult, error) {
	if len(expectedRevision) != sha256.Size*2 || strings.Trim(expectedRevision, "0123456789abcdef") != "" {
		return nil, scope.Err("invalid_argument", "expected_revision must be a lowercase SHA-256 revision from document_read")
	}
	return s.write(ctx, name, markdown, expectedRevision, true)
}

func (s *Store) lockWriter(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.mu.TryLock() {
			return nil
		}
		if err := waitForWriteLock(ctx); err != nil {
			return err
		}
	}
}

func waitForWriteLock(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Store) write(ctx context.Context, name, markdown, expectedRevision string, update bool) (*WriteResult, error) {
	if err := s.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	if !s.scope.AllowWrites {
		return nil, scope.Err("permission_denied", "project writes were not enabled at server launch")
	}
	if err := scope.RelativePath(name); err != nil {
		return nil, err
	}
	g := s.grants[0]
	sharedWrite := false
	if s.scope.CanWriteShared(name) {
		for _, candidate := range s.grants[1:] {
			if candidate.writable && name != candidate.path && scope.Beneath(candidate.path, name) {
				g, sharedWrite = candidate, true
				break
			}
		}
	}
	if !scope.Visible(name) || !strings.EqualFold(path.Ext(name), ".md") || name == g.path || !scope.Beneath(g.path, name) {
		return nil, scope.Err("permission_denied", "writes require a visible Markdown path within the active project or a writable shared folder")
	}
	if len(markdown) > s.scope.Limits.MaxDocumentBytes {
		return nil, scope.Err("resource_limit", "Markdown exceeds max_document_bytes")
	}
	if !utf8.ValidString(markdown) || strings.ContainsRune(markdown, 0) {
		return nil, scope.Err("invalid_argument", "Markdown must be valid UTF-8 without NUL characters")
	}
	doc, err := parseDocument(name, markdown)
	if err != nil {
		return nil, err
	}
	// Accepted writes must also round-trip through an untruncated document_read.
	// JSON escaping can exceed the output cap even below the raw byte limit.
	if err := outputBound(doc); err != nil {
		return nil, err
	}
	if err := s.checkGrant(g); err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(filepath.FromSlash(g.path), filepath.FromSlash(name))
	if err != nil {
		return nil, scope.Err("invalid_argument", "cannot resolve authorized write path")
	}
	rel = filepath.ToSlash(rel)
	parentName, base := path.Dir(rel), path.Base(rel)
	if _, err := scope.CheckComponents(g.root, parentName, true); err != nil {
		return nil, err
	}
	parent, err := openDirectory(g.root, parentName)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return nil, scope.FSError(err, "inspect write directory")
	}
	lockRoot := g.root
	if sharedWrite {
		// The destination parent is identical even when separate processes grant
		// 05-Knowledge and 05-Knowledge/Go. A grant-root lock would not coordinate
		// these overlapping grants and could lose updates to the same document.
		lockRoot = parent
	}
	lock, err := acquireProjectWriteLock(ctx, lockRoot)
	if err != nil {
		return nil, err
	}
	defer lock.close()
	if err := s.checkWriteLocation(ctx, g, parentName, parentInfo, lock); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	var before *writeSnapshot
	if update {
		before, err = s.writeSnapshot(parent, base)
		if err != nil {
			return nil, err
		}
		if before.revision != expectedRevision {
			return nil, scope.Err("conflict", "document changed; read it again before updating")
		}
		mode = before.info.Mode().Perm()
	} else if err := missingWriteDestination(parent, base); err != nil {
		return nil, err
	}

	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, scope.Err("internal_error", "cannot generate temporary note name")
	}
	temporary := ".vault-bridge-write-" + hex.EncodeToString(token[:])
	file, err := parent.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, scope.FSError(err, "create temporary note")
	}
	defer parent.Remove(temporary)
	_, writeErr := io.WriteString(file, markdown)
	if writeErr == nil {
		writeErr = file.Chmod(mode)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return nil, scope.FSError(writeErr, "prepare note contents")
	}
	if closeErr != nil {
		return nil, scope.FSError(closeErr, "close temporary note")
	}
	if err := s.checkWriteLocation(ctx, g, parentName, parentInfo, lock); err != nil {
		return nil, err
	}
	if update {
		current, err := s.writeSnapshot(parent, base)
		if err != nil {
			if ErrorCode(err) == "not_found" {
				return nil, scope.Err("conflict", "document disappeared during update")
			}
			return nil, err
		}
		if current.revision != expectedRevision || !os.SameFile(before.info, current.info) || before.info.Mode().Perm() != current.info.Mode().Perm() {
			return nil, scope.Err("conflict", "document changed during update")
		}
	} else {
		if err := missingWriteDestination(parent, base); err != nil {
			return nil, err
		}
	}
	if err := s.checkWriteLocation(ctx, g, parentName, parentInfo, lock); err != nil {
		return nil, err
	}
	if update {
		err = parent.Rename(temporary, base)
	} else {
		// Link atomically creates the destination only if it is still absent.
		// Removing the temporary name leaves the complete committed note.
		err = parent.Link(temporary, base)
	}
	if errors.Is(err, os.ErrExist) {
		return nil, scope.Err("conflict", "document already exists")
	}
	if err != nil {
		return nil, scope.FSError(err, "commit note")
	}
	return &WriteResult{Path: name, Revision: doc.Revision}, nil
}

func missingWriteDestination(parent *os.Root, base string) error {
	info, err := parent.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return scope.FSError(err, "inspect new note destination")
	}
	if !info.Mode().IsRegular() {
		return scope.Err("permission_denied", "note destination must not be a symlink, directory or special file")
	}
	return scope.Err("conflict", "document already exists")
}

type writeSnapshot struct {
	info     os.FileInfo
	revision string
}

func (s *Store) writeSnapshot(parent *os.Root, base string) (*writeSnapshot, error) {
	before, err := parent.Lstat(base)
	if err != nil {
		return nil, scope.FSError(err, "inspect document for update")
	}
	if !before.Mode().IsRegular() {
		return nil, scope.Err("permission_denied", "updates require a regular note, not a symlink or special file")
	}
	if before.Size() > int64(s.scope.Limits.MaxDocumentBytes) {
		return nil, scope.Err("resource_limit", "existing note exceeds max_document_bytes")
	}
	file, err := openWriteSnapshot(parent, base)
	if err != nil {
		return nil, scope.FSError(err, "open document for update")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, scope.FSError(err, "inspect open document")
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, scope.Err("conflict", "document changed during update")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(s.scope.Limits.MaxDocumentBytes)+1))
	if err != nil {
		return nil, scope.FSError(err, "read document for update")
	}
	if len(data) > s.scope.Limits.MaxDocumentBytes {
		return nil, scope.Err("resource_limit", "existing note exceeds max_document_bytes")
	}
	after, err := parent.Lstat(base)
	if err != nil {
		return nil, scope.FSError(err, "recheck document for update")
	}
	if !after.Mode().IsRegular() || !os.SameFile(opened, after) {
		return nil, scope.Err("conflict", "document changed during update")
	}
	return &writeSnapshot{info: opened, revision: documentRevision(string(data))}, nil
}

type projectWriteLock struct {
	root *os.Root
	file *os.File
	info os.FileInfo
}

func acquireProjectWriteLock(ctx context.Context, root *os.Root) (*projectWriteLock, error) {
	file, err := openProjectWriteLock(root)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, scope.FSError(err, "inspect write lock")
	}
	lock := &projectWriteLock{root: root, file: file, info: info}
	if err := lock.check(); err != nil {
		file.Close()
		return nil, err
	}
	if err := lockWriteFile(ctx, file); err != nil {
		file.Close()
		return nil, err
	}
	if err := lock.check(); err != nil {
		lock.close()
		return nil, err
	}
	return lock, nil
}

func (l *projectWriteLock) check() error {
	info, err := l.root.Lstat(writeLockName)
	if err != nil {
		return scope.FSError(err, "recheck write lock")
	}
	if !l.info.Mode().IsRegular() || !info.Mode().IsRegular() || !os.SameFile(l.info, info) {
		return scope.Err("permission_denied", "write lock must remain a regular file")
	}
	return nil
}

func (l *projectWriteLock) close() {
	unlockWriteFile(l.file)
	l.file.Close()
}

func (s *Store) checkWriteLocation(ctx context.Context, g grant, parent string, original os.FileInfo, lock *projectWriteLock) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkGrant(g); err != nil {
		return err
	}
	info, err := scope.CheckComponents(g.root, parent, true)
	if err != nil {
		return err
	}
	if !os.SameFile(original, info) {
		return scope.Err("permission_denied", "note parent directory changed during write")
	}
	return lock.check()
}
