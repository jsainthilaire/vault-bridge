package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func parseDocument(name, markdown string) (*Document, error) {
	metadata := map[string]any{}
	body := markdown
	text := strings.TrimPrefix(markdown, "\ufeff")
	first, rest, hasNewline := strings.Cut(text, "\n")
	if strings.TrimSuffix(first, "\r") == "---" {
		if !hasNewline {
			return nil, scope.Err("invalid_argument", "malformed frontmatter: opening delimiter has no closing delimiter")
		}
		var header strings.Builder
		found := false
		for len(rest) > 0 {
			line, next, _ := strings.Cut(rest, "\n")
			if strings.TrimSuffix(line, "\r") == "---" || strings.TrimSuffix(line, "\r") == "..." {
				body = next
				found = true
				break
			}
			header.WriteString(line)
			header.WriteByte('\n')
			if header.Len() > maxMetadataBytes {
				return nil, scope.Err("resource_limit", "frontmatter exceeds 32768 bytes")
			}
			rest = next
		}
		if !found {
			return nil, scope.Err("invalid_argument", "malformed frontmatter: opening delimiter has no closing delimiter")
		}
		decoder := yaml.NewDecoder(strings.NewReader(header.String()))
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil && err != io.EOF {
			return nil, scope.Err("invalid_argument", "malformed frontmatter: expected a YAML mapping with unique string keys")
		}
		if err := metadataKeys(&node); err != nil {
			return nil, err
		}
		if err := node.Decode(&metadata); err != nil {
			return nil, scope.Err("invalid_argument", "malformed frontmatter: expected a YAML mapping with unique string keys")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, scope.Err("invalid_argument", "malformed frontmatter: expected one YAML mapping")
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return nil, scope.Err("invalid_argument", "malformed frontmatter: metadata must use JSON-compatible values and string keys")
		}
		if len(encoded) > maxMetadataBytes {
			return nil, scope.Err("resource_limit", "expanded frontmatter exceeds 32768 bytes")
		}
		// Decode once through JSON so callers see consistent JSON primitives
		// rather than YAML-specific maps or native timestamp values.
		decoderJSON := json.NewDecoder(bytes.NewReader(encoded))
		decoderJSON.UseNumber()
		if err := decoderJSON.Decode(&metadata); err != nil {
			return nil, scope.Err("invalid_argument", "malformed frontmatter")
		}
	}
	title := metadataString(metadata, "title")
	if title == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				break
			}
		}
	}
	if title == "" {
		title = strings.TrimSuffix(path.Base(name), path.Ext(name))
	}
	title, _ = truncateRunes(title, 300)
	typeName, _ := truncateRunes(metadataString(metadata, "type"), 128)
	status, _ := truncateRunes(metadataString(metadata, "status"), 128)
	updated, _ := truncateRunes(metadataString(metadata, "updated"), 128)
	if len(updated) >= 10 {
		if _, err := time.Parse(time.RFC3339, updated); err == nil {
			updated = updated[:10]
		}
	}
	return &Document{Path: name, Revision: documentRevision(markdown), Title: title, Type: typeName, Status: status, Updated: updated, Frontmatter: metadata, Markdown: markdown, Truncated: false}, nil
}

func metadataKeys(root *yaml.Node) error {
	// An alias graph can describe exponentially more data than its source.
	// Validate each node once, detect cycles, and cache subtree height so aliases
	// at a deeper location cannot bypass the nesting limit through memoization.
	state := map[*yaml.Node]uint8{}
	height := map[*yaml.Node]int{}
	var visit func(*yaml.Node, int) (int, error)
	visit = func(node *yaml.Node, depth int) (int, error) {
		if node == nil {
			return 0, scope.Err("invalid_argument", "malformed frontmatter: invalid alias")
		}
		if depth > 64 {
			return 0, scope.Err("resource_limit", "frontmatter nesting exceeds 64 levels")
		}
		if state[node] == 1 {
			return 0, scope.Err("invalid_argument", "malformed frontmatter: cyclic aliases are not allowed")
		}
		if state[node] == 2 {
			if depth+height[node] > 64 {
				return 0, scope.Err("resource_limit", "frontmatter nesting exceeds 64 levels")
			}
			return height[node], nil
		}
		state[node] = 1
		if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
					return 0, scope.Err("invalid_argument", "malformed frontmatter: mapping keys must be strings")
				}
			}
		}
		children := node.Content
		if node.Kind == yaml.AliasNode {
			children = []*yaml.Node{node.Alias}
		}
		longest := 0
		for _, child := range children {
			childHeight, err := visit(child, depth+1)
			if err != nil {
				return 0, err
			}
			longest = max(longest, childHeight+1)
		}
		height[node], state[node] = longest, 2
		return longest, nil
	}
	_, err := visit(root, 0)
	return err
}

func metadataString(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
}

var wikiLinks = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
var markdownLinks = regexp.MustCompile(`\[[^\]\n]*\]\(([^)\s]+)\)`)

func (s *Store) ProjectContext(ctx context.Context) (*ProjectContext, error) {
	if err := s.lockReader(ctx); err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	doc, err := s.read(ctx, s.scope.ProjectRoot+"/index.md")
	if err != nil {
		return nil, err
	}
	_, body, err := frontmatterBody(doc.Markdown)
	if err != nil {
		return nil, err
	}
	summary := metadataString(doc.Frontmatter, "summary")
	if summary == "" {
		summary = firstParagraph(body)
	}
	summary, _ = truncateRunes(summary, 600)
	excerpt, truncated := truncateRunes(doc.Markdown, maxExcerptRunes)
	result := &ProjectContext{Project: s.scope.ProjectID, Title: doc.Title, Summary: summary, Status: doc.Status, Path: doc.Path, Paths: []string{s.scope.ProjectRoot}, Links: []string{}, IndexExcerpt: excerpt, Truncated: truncated}
	seen := map[string]bool{}
	add := func(target string) {
		target = strings.SplitN(target, "|", 2)[0]
		target = strings.SplitN(target, "#", 2)[0]
		if target == "" || strings.ContainsAny(target, ":%\\") {
			return
		}
		if path.Ext(target) == "" {
			target += ".md"
		}
		if err := scope.RelativePath(target); err != nil {
			return
		}
		if _, _, err := s.resolve(target); err != nil {
			// Numbered vault sections denote vault-relative links. Do not
			// fabricate an active-project path for a link to another section.
			first, _, _ := strings.Cut(target, "/")
			if (len(first) >= 3 && first[0] >= '0' && first[0] <= '9' && first[1] >= '0' && first[1] <= '9' && first[2] == '-') || first == "assets" {
				return
			}
			target = path.Join(s.scope.ProjectRoot, target)
		}
		if _, _, err := s.resolve(target); err != nil {
			return
		}
		if !seen[target] && len(result.Links) < 32 {
			seen[target] = true
			result.Links = append(result.Links, target)
		}
	}
	for _, match := range wikiLinks.FindAllStringSubmatch(body, 64) {
		add(match[1])
	}
	for _, match := range markdownLinks.FindAllStringSubmatch(body, 64) {
		add(match[1])
	}
	for _, name := range []string{"index.md", "vision.md", "requirements.md", "architecture.md", "roadmap.md", "tasks.md", "features", "decisions", "research", "sessions"} {
		result.Paths = append(result.Paths, s.scope.ProjectRoot+"/"+name)
	}
	if err := outputBound(result); err != nil {
		return nil, err
	}
	return result, nil
}

// frontmatterBody is used only after parseDocument has validated the note.
func frontmatterBody(markdown string) (string, string, error) {
	text := strings.TrimPrefix(markdown, "\ufeff")
	first, rest, _ := strings.Cut(text, "\n")
	if strings.TrimSuffix(first, "\r") != "---" {
		return "", text, nil
	}
	var header strings.Builder
	for len(rest) > 0 {
		line, next, _ := strings.Cut(rest, "\n")
		if strings.TrimSuffix(line, "\r") == "---" || strings.TrimSuffix(line, "\r") == "..." {
			return header.String(), next, nil
		}
		header.WriteString(line)
		header.WriteByte('\n')
		rest = next
	}
	return "", "", scope.Err("invalid_argument", "malformed frontmatter")
}

func firstParagraph(body string) string {
	var paragraph []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[[") || strings.HasPrefix(trimmed, "```") {
			continue
		}
		paragraph = append(paragraph, trimmed)
		if len(paragraph) >= 8 {
			break
		}
	}
	return strings.Join(paragraph, " ")
}
