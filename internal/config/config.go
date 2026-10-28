// Package config loads a trusted launch configuration and binds one project.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

const (
	MaxConfigBytes         = 1 << 20
	HardMaxDocumentBytes   = 1 << 20
	HardMaxSearchResults   = 100
	HardMaxSearchScanFiles = 50000
)

type Limits struct {
	MaxDocumentBytes   int `yaml:"max_document_bytes" json:"max_document_bytes"`
	MaxSearchResults   int `yaml:"max_search_results" json:"max_search_results"`
	MaxSearchScanFiles int `yaml:"max_search_scan_files" json:"max_search_scan_files"`
}

func DefaultLimits() Limits {
	return Limits{MaxDocumentBytes: 262144, MaxSearchResults: 25, MaxSearchScanFiles: 5000}
}

func ValidateLimits(l Limits) error {
	if l.MaxDocumentBytes < 1 || l.MaxDocumentBytes > HardMaxDocumentBytes {
		return scope.Err("invalid_argument", "max_document_bytes must be between 1 and 1048576")
	}
	if l.MaxSearchResults < 1 || l.MaxSearchResults > HardMaxSearchResults {
		return scope.Err("invalid_argument", "max_search_results must be between 1 and 100")
	}
	if l.MaxSearchScanFiles < 1 || l.MaxSearchScanFiles > HardMaxSearchScanFiles {
		return scope.Err("invalid_argument", "max_search_scan_files must be between 1 and 50000")
	}
	return nil
}

type Scope struct {
	VaultRoot       string
	ProjectID       string
	ProjectRoot     string
	SharedReadPaths []string
	Limits          Limits
	AllowWrites     bool
}

type project struct {
	Folder          string   `yaml:"folder"`
	SharedReadPaths []string `yaml:"shared_read_paths"`
}

type fileConfig struct {
	Version   int                `yaml:"version"`
	VaultRoot string             `yaml:"vault_root"`
	Projects  map[string]project `yaml:"projects"`
	Limits    struct {
		MaxDocumentBytes   *int `yaml:"max_document_bytes"`
		MaxSearchResults   *int `yaml:"max_search_results"`
		MaxSearchScanFiles *int `yaml:"max_search_scan_files"`
	} `yaml:"limits"`
}

var projectID = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func ValidProjectID(id string) bool { return len(id) <= 64 && projectID.MatchString(id) }

// Load intentionally performs no ~ or environment-variable expansion. Paths
// are absolute so launching the same configuration from another cwd is stable.
func Load(configPath, id string) (*Scope, error) {
	if !filepath.IsAbs(configPath) || strings.ContainsRune(configPath, 0) {
		return nil, scope.Err("invalid_argument", "--config must be an absolute path; ~ and environment variables are not expanded")
	}
	if !ValidProjectID(id) {
		return nil, scope.Err("invalid_argument", "--project must be an explicit lowercase kebab-case ID (maximum 64 bytes)")
	}
	// Inspect before opening so special files are rejected without blocking.
	// Unix also opens nonblocking in case a regular file is swapped to a FIFO
	// between this check and open; the descriptor is checked again below.
	info, err := os.Stat(configPath)
	if err != nil {
		return nil, scope.FSError(err, "inspect configuration")
	}
	if !info.Mode().IsRegular() {
		return nil, scope.Err("invalid_argument", "configuration must be a regular file")
	}
	f, err := openConfig(configPath)
	if err != nil {
		return nil, scope.FSError(err, "open configuration")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, scope.FSError(err, "inspect configuration")
	}
	if !info.Mode().IsRegular() {
		return nil, scope.Err("invalid_argument", "configuration must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return nil, scope.FSError(err, "read configuration")
	}
	if len(data) > MaxConfigBytes {
		return nil, scope.Err("resource_limit", "configuration exceeds 1048576 bytes")
	}
	var raw fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return nil, scope.Err("invalid_argument", "invalid configuration YAML: use the version-1 schema and known field names")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, scope.Err("invalid_argument", "configuration must contain exactly one YAML document")
	}
	if raw.Version != 1 {
		return nil, scope.Err("invalid_argument", "configuration version must be 1")
	}
	if !filepath.IsAbs(raw.VaultRoot) || strings.ContainsRune(raw.VaultRoot, 0) {
		return nil, scope.Err("invalid_argument", "vault_root must be absolute; ~ and environment variables are not expanded")
	}
	canonical, err := filepath.EvalSymlinks(raw.VaultRoot)
	if err != nil {
		return nil, scope.FSError(err, "resolve vault_root (run vault init if missing)")
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, scope.Err("invalid_argument", "cannot canonicalize vault_root")
	}
	vault, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, scope.FSError(err, "open vault_root")
	}
	defer vault.Close()
	if len(raw.Projects) == 0 {
		return nil, scope.Err("invalid_argument", "projects must explicitly map at least one project ID")
	}
	if len(raw.Projects) > 1024 {
		return nil, scope.Err("resource_limit", "configuration has more than 1024 projects")
	}
	limits := DefaultLimits()
	if raw.Limits.MaxDocumentBytes != nil {
		limits.MaxDocumentBytes = *raw.Limits.MaxDocumentBytes
	}
	if raw.Limits.MaxSearchResults != nil {
		limits.MaxSearchResults = *raw.Limits.MaxSearchResults
	}
	if raw.Limits.MaxSearchScanFiles != nil {
		limits.MaxSearchScanFiles = *raw.Limits.MaxSearchScanFiles
	}
	if err := ValidateLimits(limits); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(raw.Projects))
	for key := range raw.Projects {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	validatedFolders := make([]string, 0, len(ids))
	for _, key := range ids {
		p := raw.Projects[key]
		if !ValidProjectID(key) {
			return nil, scope.Err("invalid_argument", "every project key must be lowercase kebab-case (maximum 64 bytes)")
		}
		if err := validateDirectory(vault, canonical, p.Folder, "03-Projects"); err != nil {
			return nil, fmt.Errorf("project %s folder: %w", key, err)
		}
		for _, previous := range validatedFolders {
			if scope.Beneath(previous, p.Folder) || scope.Beneath(p.Folder, previous) {
				return nil, scope.Err("invalid_argument", "project folder mappings must not overlap")
			}
		}
		validatedFolders = append(validatedFolders, p.Folder)
		if len(p.SharedReadPaths) > 32 {
			return nil, scope.Err("resource_limit", "a project may grant at most 32 shared subtrees")
		}
		seen := map[string]bool{}
		for _, shared := range p.SharedReadPaths {
			if seen[shared] {
				return nil, scope.Err("invalid_argument", "duplicate shared_read_paths entry")
			}
			seen[shared] = true
			if err := validateDirectory(vault, canonical, shared, "05-Knowledge"); err != nil {
				return nil, fmt.Errorf("project %s shared_read_paths: %w", key, err)
			}
		}
	}
	p, ok := raw.Projects[id]
	if !ok {
		return nil, scope.Err("not_found", "unknown project ID: add an explicit projects mapping before serving")
	}
	shared := append([]string(nil), p.SharedReadPaths...)
	sort.Strings(shared)
	return &Scope{VaultRoot: canonical, ProjectID: id, ProjectRoot: p.Folder, SharedReadPaths: shared, Limits: limits}, nil
}

func validateDirectory(root *os.Root, canonical, name, parent string) error {
	if err := scope.RelativePath(name); err != nil {
		return err
	}
	if !scope.Visible(name) {
		return scope.Err("permission_denied", "hidden, secret and attachment paths cannot be granted")
	}
	if name == parent || !scope.Beneath(parent, name) {
		return scope.Err("invalid_argument", "grant must name an explicit subtree below "+parent)
	}
	absolute := filepath.Join(canonical, filepath.FromSlash(name))
	if !scope.Contains(canonical, absolute) {
		return scope.Err("permission_denied", "configured path must remain inside vault_root")
	}
	_, err := scope.CheckComponents(root, name, true)
	return err
}
