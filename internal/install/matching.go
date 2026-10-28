package install

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/jsainthilaire/vault-bridge/internal/config"
	"github.com/jsainthilaire/vault-bridge/internal/vault"
)

type selectedProject struct {
	scope  *config.Scope
	source string
}

func selectProjects(o Options) ([]selectedProject, []SkippedProject, error) {
	if o.SourceDir == "" {
		scopes, err := projectDirectories(o.ProjectsDir)
		if err != nil {
			return nil, nil, err
		}
		selected := make([]selectedProject, 0, len(scopes))
		for _, cfg := range scopes {
			selected = append(selected, selectedProject{scope: cfg})
		}
		return selected, nil, nil
	}
	projects, err := projectsDirectory(o.ProjectsDir)
	if err != nil {
		return nil, nil, err
	}
	docs, _, err := childDirectories(projects, "projects")
	if err != nil {
		return nil, nil, err
	}
	sources, sourceRoot, err := childDirectories(o.SourceDir, "source")
	if err != nil {
		return nil, nil, err
	}
	// Compare enumerated names, not filesystem lookups: matching stays case
	// sensitive even on a case-insensitive filesystem.
	sourceNames := make(map[string]bool, len(sources))
	for _, name := range sources {
		sourceNames[name] = true
	}
	var selected []selectedProject
	var skipped []SkippedProject
	for _, name := range docs {
		target := filepath.Join(projects, name)
		if !sourceNames[name] {
			skipped = append(skipped, SkippedProject{Project: name, Target: target, Reason: "no matching source folder"})
			continue
		}
		delete(sourceNames, name)
		source := filepath.Join(sourceRoot, name)
		// Recheck the matched source path before treating it as eligible. Only
		// directories are opened; source contents never become MCP grants.
		root, _, err := openDirectory(source, false)
		if err != nil {
			return nil, nil, fmt.Errorf("validate source %q: %w", name, err)
		}
		root.Close()
		cfg, err := projectScope(projects, name)
		if err != nil {
			return nil, nil, fmt.Errorf("validate project %q: %w", name, err)
		}
		exists, err := hasProjectIndex(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("validate index for %q: %w", name, err)
		}
		if !exists {
			skipped = append(skipped, SkippedProject{Project: name, Source: source, Target: target, Reason: "missing index.md"})
			continue
		}
		selected = append(selected, selectedProject{scope: cfg, source: source})
	}
	for _, name := range sources {
		if sourceNames[name] {
			skipped = append(skipped, SkippedProject{Project: name, Source: filepath.Join(sourceRoot, name), Reason: "no matching vault project"})
		}
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Project < skipped[j].Project })
	return selected, skipped, nil
}

func hasProjectIndex(cfg *config.Scope) (bool, error) {
	store, err := vault.New(cfg)
	if err != nil {
		return false, err
	}
	defer store.Close()
	// Use the serving read path to enforce regular Markdown, symlink denial,
	// UTF-8/frontmatter validation and resource limits without writing files.
	_, err = store.Read(context.Background(), cfg.ProjectRoot+"/index.md", 0)
	if vault.ErrorCode(err) == "not_found" {
		return false, nil
	}
	return err == nil, err
}
