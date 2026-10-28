// Package extensions records boundaries for later adapters. Nothing in this
// package is registered or instantiated. Opt-in project writes live in vault.
package extensions

import "context"

// ReviewedWrite is a future adapter request, after a separate approval design.
// Implementations must validate the exact folder allowlist, approval and expected
// content hash themselves. Merely constructing this value grants no authority.
type ReviewedWrite struct {
	Path         string
	Markdown     string
	ExpectedHash string
	ApprovalID   string
}

type ReviewedWriter interface {
	Apply(context.Context, ReviewedWrite) error
}

type IndexedMatch struct {
	Path    string
	Snippet string
}

// LexicalIndex results must still pass the scoped store's access checks.
type LexicalIndex interface {
	Search(context.Context, string, int) ([]IndexedMatch, error)
}

// AuditEvent deliberately excludes document contents and search queries.
type AuditEvent struct {
	Project string
	Action  string
	Path    string
	Code    string
}

type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

// ResourceReader is an optional future MCP resource adapter with the same
// project authorization rules as tools, rather than a general filesystem reader.
type ResourceReader interface {
	ReadResource(context.Context, string) (markdown string, sourcePath string, err error)
}
