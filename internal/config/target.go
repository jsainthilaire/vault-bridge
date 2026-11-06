package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

// FromTarget binds a project directly from a trusted launch path instead of a
// YAML map. Only the explicitly selected project folder is granted; default
// resource limits apply and shared access remains empty until AddShared is called. Relative paths are
// resolved against the installer's/server's cwd, never an MCP client root.
func FromTarget(target, id string) (*Scope, error) {
	if target == "" || strings.ContainsRune(target, 0) {
		return nil, scope.Err("invalid_argument", "--target must name a project docs folder")
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return nil, scope.Err("invalid_argument", "cannot resolve --target")
	}
	parent := filepath.Dir(absolute)
	if filepath.Base(parent) != "03-Projects" {
		return nil, scope.Err("invalid_argument", "--target must be a project folder directly under <vault>/03-Projects")
	}
	if id == "" {
		id = filepath.Base(absolute)
	}
	if !ValidProjectID(id) {
		return nil, scope.Err("invalid_argument", "target folder name must be a lowercase kebab-case ID, or supply --project explicitly")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Dir(parent))
	if err != nil {
		return nil, scope.FSError(err, "resolve target vault")
	}
	vault, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, scope.FSError(err, "open target vault")
	}
	defer vault.Close()
	folder := "03-Projects/" + filepath.Base(absolute)
	if err := validateDirectory(vault, canonical, folder, "03-Projects"); err != nil {
		return nil, err
	}
	return &Scope{
		VaultRoot: canonical, ProjectID: id, ProjectRoot: folder,
		SharedReadPaths: []string{}, Limits: DefaultLimits(),
	}, nil
}
