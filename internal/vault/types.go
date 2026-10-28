// Package vault provides bounded retrieval and opt-in writes for one project.
package vault

import (
	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func ErrorCode(err error) string { return scope.ErrorCode(err) }

type DocumentInfo struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Type    string `json:"type,omitempty"`
	Status  string `json:"status,omitempty"`
	Updated string `json:"updated,omitempty"`
}

type Document struct {
	Path        string         `json:"path"`
	Revision    string         `json:"revision"`
	Title       string         `json:"title"`
	Type        string         `json:"type,omitempty"`
	Status      string         `json:"status,omitempty"`
	Updated     string         `json:"updated,omitempty"`
	Frontmatter map[string]any `json:"frontmatter"`
	Markdown    string         `json:"markdown"`
	Truncated   bool           `json:"truncated"`
}

// WriteResult identifies the committed Markdown without returning its contents.
type WriteResult struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type ListOptions struct {
	Section string `json:"section,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
}

type ListResult struct {
	Documents  []DocumentInfo `json:"documents"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type SearchOptions struct {
	Query   string `json:"query"`
	Limit   int    `json:"limit,omitempty"`
	Section string `json:"section,omitempty"`
}

type SearchMatch struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

type SearchResult struct {
	Matches   []SearchMatch `json:"matches"`
	Truncated bool          `json:"truncated"`
}

type ProjectContext struct {
	Project      string   `json:"project"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Status       string   `json:"status,omitempty"`
	Path         string   `json:"path"`
	Paths        []string `json:"paths"`
	Links        []string `json:"links"`
	IndexExcerpt string   `json:"index_excerpt"`
	Truncated    bool     `json:"truncated"`
}
