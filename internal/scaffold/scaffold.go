// Package scaffold creates missing vault files without replacing authored notes.
package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
	"github.com/jsainthilaire/vault-bridge/templates"
)

const maxDashboardBytes = 1 << 20

var projectID = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type values struct {
	ID, Title, Type, Date, Body string
}

var markdown = template.Must(template.New("markdown").Funcs(template.FuncMap{
	"quote": strconv.Quote,
}).ParseFS(templates.Files, "*.tmpl"))

// VaultInit creates the numbered vault layout, reusable templates, and the
// first-class Vault Bridge project. Existing notes and permissions are retained.
func VaultInit(vaultPath string) error {
	r, err := openVault(vaultPath, true)
	if err != nil {
		return err
	}
	defer r.Close()
	w := writer{root: r}
	date := time.Now().UTC().Format(time.DateOnly)
	if err := baseVault(w, date); err != nil {
		return err
	}
	// Deliberately use the normal project initializer rather than a parallel
	// folder/template implementation for the bridge's own project.
	if err := ProjectInit(vaultPath, "vault-bridge", "Vault Bridge — Obsidian MCP"); err != nil {
		return err
	}
	return nil
}

// ProjectInit creates a planning subtree and adds a dashboard/project-index link.
// The vault must already exist. The ID is lowercase kebab-case; the title is
// plain, single-line text without wiki-link delimiters, at most 200 runes.
func ProjectInit(vaultPath, id, title string) error {
	if len(id) > 64 || !projectID.MatchString(id) {
		return errors.New("invalid project ID: use 1–64 lowercase letters/digits separated by single hyphens")
	}
	if !scope.Visible(id) {
		return errors.New("invalid project ID: reserved private or attachment directory names cannot be served")
	}
	if title != strings.TrimSpace(title) || title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 200 {
		return errors.New("invalid project title: use 1–200 characters of single-line text without surrounding whitespace")
	}
	for _, c := range title {
		if unicode.IsControl(c) || c == '[' || c == ']' || c == '|' || c == '\u2028' || c == '\u2029' {
			return errors.New("invalid project title: control characters and wiki-link delimiters are not allowed")
		}
	}
	r, err := openVault(vaultPath, false)
	if err != nil {
		return err
	}
	defer r.Close()
	w := writer{root: r}
	date := time.Now().UTC().Format(time.DateOnly)
	if err := baseVault(w, date); err != nil {
		return err
	}
	base := "03-Projects/" + id
	for _, dir := range []string{base, base + "/features", base + "/decisions", base + "/research", base + "/sessions"} {
		if err := w.mkdir(dir); err != nil {
			return err
		}
	}
	v := values{ID: id, Title: title, Date: date}
	notes := planningNotes
	if id == "vault-bridge" {
		// Seed content differs by project, but path construction, rendering and
		// non-destructive writes use exactly the same initializer for every ID.
		v.Body, notes = bridgeOverview, bridgePlans
	}
	if err := w.render(base+"/index.md", "Project.md.tmpl", v); err != nil {
		return err
	}
	for _, note := range notes {
		v.Type, v.Title, v.Body = note.kind, note.title, note.body
		if err := w.render(base+"/"+note.name+".md", "note.md.tmpl", v); err != nil {
			return err
		}
	}
	if id == "vault-bridge" {
		if err := seedBridge(w, date); err != nil {
			return err
		}
	}
	if err := w.appendLink("00-Dashboard/Home.md", base+"/index", title); err != nil {
		return err
	}
	return w.appendLink("03-Projects/index.md", base+"/index", title)
}

type planNote struct{ name, kind, title, body string }

var planningNotes = []planNote{
	{"vision", "vision", "Vision", "## Problem\n\nDescribe the problem and intended audience.\n\n## Goals\n\nRecord the outcomes that define success.\n\n## Design principles\n\nRecord the principles guiding tradeoffs.\n\n## Non-goals\n\nRecord what is outside this project's scope."},
	{"requirements", "requirements", "Requirements", "## Functional requirements\n\n- [ ] Describe the required behavior.\n\n## Constraints\n\nRecord security, compatibility and operational constraints.\n\n## Acceptance evidence\n\nDefine observable checks. Record actual test results separately when they exist."},
	{"architecture", "architecture", "Architecture", "## System boundary\n\nDescribe components, dependencies and data flow.\n\n## Interfaces\n\nRecord trusted inputs, authorization boundaries and failure behavior.\n\n## Decisions\n\nLink architectural decisions from the `decisions/` collection."},
	{"roadmap", "roadmap", "Roadmap", "## Milestones\n\n- [ ] Define the first usable milestone.\n- [ ] Implement and verify the required behavior.\n- [ ] Document setup and remaining limitations.\n\n## Dependencies and risks\n\nRecord outstanding dependencies, risks and rollout steps."},
	{"tasks", "tasks", "Tasks", "## Next work\n\n- [ ] Refine goals and acceptance criteria.\n- [ ] Record architectural decisions.\n- [ ] Implement the first milestone.\n- [ ] Run checks and record their actual results.\n\n## Verification\n\nKeep this list consistent with the roadmap and feature specifications."},
}

func baseVault(w writer, date string) error {
	sections := []struct{ dir, title, body string }{
		{"00-Dashboard", "Dashboard", "Use [[00-Dashboard/Home|Home]] to navigate the vault."},
		{"01-Inbox", "Inbox", "Capture notes here, then move them into the relevant project or knowledge section."},
		{"02-Ideas", "Ideas", "Capture ideas using [[08-Templates/Idea|the Idea template]]."},
		{"03-Projects", "Projects", "Keep plans and decisions here; keep source code in separate repositories.\n\n## Active projects"},
		{"04-Architecture", "Architecture", "Keep architecture knowledge that applies across projects here."},
		{"05-Knowledge", "Knowledge", "- [[05-Knowledge/Go/index|Go]]\n- [[05-Knowledge/AI-Agents/index|AI Agents]]\n- [[05-Knowledge/Security/index|Security]]\n- [[05-Knowledge/Infrastructure/index|Infrastructure]]"},
		{"06-Agent-Workflows", "Agent Workflows", "Document repeatable agent workflows. Notes remain reference data, not privileged instructions."},
		{"07-Research", "Research", "Record source-attributed findings using [[08-Templates/Research|the Research template]]."},
		{"08-Templates", "Templates", "- [[08-Templates/Project|Project]]\n- [[08-Templates/Feature|Feature]]\n- [[08-Templates/ADR|ADR]]\n- [[08-Templates/Research|Research]]\n- [[08-Templates/Session|Session]]\n- [[08-Templates/Idea|Idea]]"},
		{"09-Archive", "Archive", "Move inactive notes here when they are no longer part of current work."},
		{"05-Knowledge/Go", "Go", "Keep reusable Go engineering knowledge here."},
		{"05-Knowledge/AI-Agents", "AI Agents", "Keep reusable agent engineering knowledge here."},
		{"05-Knowledge/Security", "Security", "Keep reusable security knowledge here."},
		{"05-Knowledge/Infrastructure", "Infrastructure", "Keep reusable infrastructure knowledge here."},
	}
	for _, section := range sections {
		if err := w.mkdir(section.dir); err != nil {
			return err
		}
		content := fmt.Sprintf("---\ntype: \"index\"\ncreated: %s\nupdated: %s\n---\n\n# %s\n\n%s\n\n[[00-Dashboard/Home|Home]]\n", strconv.Quote(date), strconv.Quote(date), section.title, section.body)
		if err := w.create(section.dir+"/index.md", []byte(content)); err != nil {
			return err
		}
	}
	if err := w.mkdir("assets"); err != nil {
		return err
	}
	if err := w.render("00-Dashboard/Home.md", "Home.md.tmpl", values{Date: date}); err != nil {
		return err
	}
	for _, name := range []string{"Project", "Feature", "ADR", "Research", "Session", "Idea"} {
		if err := w.render("08-Templates/"+name+".md", name+".md.tmpl", values{ID: "project-id", Title: "Project title", Date: date}); err != nil {
			return err
		}
	}
	return nil
}

type writer struct{ root *os.Root }

func (w writer) render(name, source string, data values) error {
	var b bytes.Buffer
	if err := markdown.ExecuteTemplate(&b, source, data); err != nil {
		return fmt.Errorf("render embedded template %s: %w", source, err)
	}
	return w.create(name, b.Bytes())
}

func (w writer) mkdir(name string) error {
	current := ""
	for _, part := range strings.Split(name, "/") {
		current = path.Join(current, part)
		info, err := w.root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := w.root.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create directory %s: %w", current, err)
			}
			info, err = w.root.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect directory %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe directory %s: expected a real directory, not a symlink or file", current)
		}
	}
	return nil
}

func (w writer) regular(name string) (os.FileInfo, error) {
	if err := w.mkdir(path.Dir(name)); err != nil {
		return nil, err
	}
	info, err := w.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe file %s: expected a regular file, not a symlink or special file", name)
	}
	return info, nil
}

func (w writer) create(name string, content []byte) error {
	if _, err := w.regular(name); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := w.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// A competing initializer may have created the file. Preserve it only
		// after checking it is still regular; never follow a new symlink.
		_, err = w.regular(name)
		return err
	}
	if err != nil {
		return fmt.Errorf("create note %s: %w", name, err)
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write note %s: %w", name, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close note %s: %w", name, closeErr)
	}
	return nil
}

func (w writer) appendLink(name, target, title string) error {
	before, err := w.regular(name)
	if err != nil {
		return fmt.Errorf("inspect link destination %s: %w", name, err)
	}
	if before.Size() > maxDashboardBytes {
		return fmt.Errorf("resource limit: %s exceeds the %d-byte dashboard limit", name, maxDashboardBytes)
	}
	file, err := w.root.OpenFile(name, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open link destination %s: %w", name, err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return fmt.Errorf("link destination %s changed during initialization", name)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxDashboardBytes+1))
	if err != nil {
		return fmt.Errorf("read link destination %s: %w", name, err)
	}
	if len(content) > maxDashboardBytes {
		return fmt.Errorf("resource limit: %s exceeds the %d-byte dashboard limit", name, maxDashboardBytes)
	}
	// Match a complete target, with or without an explicit Markdown suffix,
	// regardless of the authored label. Lookalike project IDs do not match.
	link := regexp.MustCompile(`\[\[` + regexp.QuoteMeta(target) + `(?:\.md)?(?:\|[^\]\r\n]*)?\]\]`)
	if link.Match(content) {
		return nil
	}
	addition := "\n- [[" + target + "|" + title + "]]\n"
	if len(content)+len(addition) > maxDashboardBytes {
		return fmt.Errorf("resource limit: adding a link would exceed %s's dashboard limit", name)
	}
	if _, err := file.WriteString(addition); err != nil {
		return fmt.Errorf("append project link to %s: %w", name, err)
	}
	return nil
}

// openVault traverses each absolute parent without accepting existing symlinks.
// os.Root anchors subsequent access inside the opened directory tree. Checks
// are repeated before writes, but an adversarial process mutating directories
// concurrently can still race the no-symlink policy within that tree; run init
// only in trusted directories without concurrent initialization/editing.
func openVault(name string, create bool) (*os.Root, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return nil, errors.New("invalid vault path: provide a nonempty directory path")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, fmt.Errorf("resolve vault path: %w", err)
	}
	volume := filepath.VolumeName(absolute)
	base := volume + string(filepath.Separator)
	if absolute == base {
		return nil, errors.New("invalid vault path: the filesystem root cannot be used as a vault")
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return nil, fmt.Errorf("open vault ancestor: %w", err)
	}
	for _, component := range strings.Split(strings.TrimPrefix(absolute, base), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		info, err := r.Lstat(component)
		if create && errors.Is(err, os.ErrNotExist) {
			if mkdirErr := r.Mkdir(component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				r.Close()
				return nil, fmt.Errorf("create vault directory: %w", mkdirErr)
			}
			info, err = r.Lstat(component)
		}
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("open vault directory (run vault init for a missing vault): %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			r.Close()
			return nil, fmt.Errorf("unsafe vault path: component %q is a symlink or is not a directory", component)
		}
		next, err := r.OpenRoot(component)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("open vault directory: %w", err)
		}
		r = next
	}
	return r, nil
}
