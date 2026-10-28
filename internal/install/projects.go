package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

// Bound discovery independently of the client configuration size limit.
const maxProjectsEntries = 1024

type ProjectsResult struct {
	Path     string
	Projects []Result
}

// Projects registers each immediate project directory as a separate global
// connection. Validation and merging finish before one atomic settings write;
// an invalid folder or conflicting entry leaves the settings unchanged.
func Projects(o Options) (ProjectsResult, error) {
	var result ProjectsResult
	if !o.Global {
		return result, errors.New("--projects-dir requires --global")
	}
	if o.Repo != "" {
		return result, errors.New("--global cannot be combined with a code repository path or --repo")
	}
	if o.Target != "" || o.Config != "" || o.Project != "" {
		return result, errors.New("--projects-dir cannot be combined with --target, --config or --project")
	}
	destination, err := globalConfigPath(o.Client)
	if err != nil {
		return result, err
	}
	scopes, err := projectDirectories(o.ProjectsDir)
	if err != nil {
		return result, err
	}
	binary, err := executable(o.Binary)
	if err != nil {
		return result, err
	}
	result.Path = destination
	name := filepath.Base(destination)
	root, _, err := openDirectory(filepath.Dir(destination), false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ProjectsResult{}, err
	}
	if root != nil {
		defer root.Close()
	}
	var before []byte
	mode, exists := os.FileMode(0o600), false
	if root != nil {
		before, mode, exists, err = readConfig(root, name)
		if err != nil {
			return ProjectsResult{}, err
		}
	}
	after := before
	for _, cfg := range scopes {
		args := []string{"serve", "--target", filepath.Join(cfg.VaultRoot, filepath.FromSlash(cfg.ProjectRoot)), "--project", cfg.ProjectID, "--transport", "stdio"}
		if o.AllowWrites {
			args = append(args, "--allow-writes")
		}
		server := "devvault-" + cfg.ProjectID
		var merged []byte
		if o.Client == "codex" {
			merged, err = mergeCodexNamed(after, server, binary, args)
		} else {
			merged, err = mergeClaudeNamed(after, server, binary, args)
		}
		if err != nil {
			return ProjectsResult{}, err
		}
		if len(merged) > maxConfigBytes {
			return ProjectsResult{}, errors.New("client configuration would exceed 1048576 bytes")
		}
		result.Projects = append(result.Projects, Result{Path: destination, Project: cfg.ProjectID, Changed: !bytes.Equal(after, merged)})
		after = merged
	}
	if bytes.Equal(before, after) {
		return result, nil
	}
	if root == nil {
		root, _, err = openDirectory(filepath.Dir(destination), true)
		if err != nil {
			return ProjectsResult{}, err
		}
		defer root.Close()
	}
	if err := writeConfig(root, name, before, after, mode, exists); err != nil {
		return ProjectsResult{}, err
	}
	return result, nil
}

func projectDirectories(name string) ([]*config.Scope, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return nil, errors.New("--projects-dir must name a vault's 03-Projects directory")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	if filepath.Base(absolute) != "03-Projects" {
		return nil, errors.New("--projects-dir must name a vault's 03-Projects directory")
	}
	root, _, err := openDirectory(absolute, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxProjectsEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxProjectsEntries {
		return nil, errors.New("projects directory exceeds 1024 entries")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var scopes []*config.Scope
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("project entry %q must not be a symlink", entry.Name())
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("project entry %q must be a directory or regular file", entry.Name())
			}
			continue
		}
		if !config.ValidProjectID(entry.Name()) {
			return nil, fmt.Errorf("project folder %q must be a lowercase kebab-case ID (maximum 64 bytes)", entry.Name())
		}
		cfg, err := config.FromTarget(filepath.Join(absolute, entry.Name()), "")
		if err != nil {
			return nil, fmt.Errorf("validate project %q: %w", entry.Name(), err)
		}
		scopes = append(scopes, cfg)
	}
	if len(scopes) == 0 {
		return nil, errors.New("projects directory contains no project folders")
	}
	return scopes, nil
}
