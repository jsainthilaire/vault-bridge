package install

import (
	"errors"
	"os"
	"path/filepath"
)

func globalConfigPath(client string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate user home directory for --global")
	}
	return configPathForHome(client, home, os.Getenv("CODEX_HOME"), os.Getenv("CLAUDE_CONFIG_DIR"))
}

// Keep location selection separate from writes so default paths can be tested
// without changing HOME or accessing a developer's actual global configuration.
func configPathForHome(client, home, codexHome, claudeDir string) (string, error) {
	var dir, name string
	switch client {
	case "codex":
		dir, name = codexHome, "config.toml"
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
	case "claude":
		dir, name = claudeDir, ".claude.json"
		if dir == "" {
			dir = home
		}
	default:
		return "", errors.New("--client must be codex or claude")
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("global configuration directories must use absolute paths; ~ and variables are not expanded")
	}
	return filepath.Join(dir, name), nil
}
