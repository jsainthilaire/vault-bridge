// Package install adds an MCP connection bound to one project to a coding client.
// It maintains project agent guidance but never initializes or changes vault notes.
package install

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

const maxConfigBytes = 1 << 20

type Options struct {
	Client, Repo, Config, Project, Target, Binary string
	ProjectsDir, SourceDir                        string
	Global, AllowWrites                           bool
	SharedPaths                                   []string
}

type Result struct {
	Path                string
	Project             string
	Changed             bool
	Source              string // Matched source folder, populated only with --source-dir.
	Target              string // Absolute vault project folder for bulk installations.
	InstructionsPath    string
	InstructionsChanged bool
	Binary              string
	Args                []string
	IndexPath           string
	AllowWrites         bool
	SharedPaths         []string // All readable shared selectors, including YAML grants.
	SharedWritablePaths []string // Explicit CLI grants when AllowWrites is enabled.
}

// Project validates the explicit vault mapping before touching client settings.
// Existing matching entries are a no-op; conflicts require manual resolution.
// Run only in trusted repositories without concurrent configuration edits.
func Project(o Options) (Result, error) {
	var result Result
	if o.ProjectsDir != "" || o.SourceDir != "" {
		return result, errors.New("use Projects for --projects-dir or --source-dir installation")
	}
	if o.Client != "codex" && o.Client != "claude" {
		return result, errors.New("--client must be codex or claude")
	}
	if o.Global && o.Repo != "" {
		return result, errors.New("--global cannot be combined with a code repository path or --repo")
	}
	if o.Target != "" && o.Config != "" {
		return result, errors.New("--target and --config are mutually exclusive")
	}
	var cfg *config.Scope
	var err error
	if o.Target != "" {
		cfg, err = config.FromTarget(o.Target, o.Project)
	} else {
		cfg, err = config.Load(o.Config, o.Project)
	}
	if err != nil {
		return result, err
	}
	if err := config.AddShared(cfg, o.SharedPaths); err != nil {
		return result, err
	}
	result.Project = cfg.ProjectID
	binary, err := executable(o.Binary)
	if err != nil {
		return result, err
	}
	var instructions *instructionEdit
	if !o.Global {
		instructions, err = prepareInstructions(o.Repo, o.Client, "devvault", cfg.ProjectID, cfg.VaultRoot, o.AllowWrites)
		if err != nil {
			return result, err
		}
	}
	var root *os.Root
	var absolute, name string
	if o.Global {
		var destination string
		destination, err = globalConfigPath(o.Client)
		if err == nil {
			root, absolute, err = openDirectory(filepath.Dir(destination), true)
			name = filepath.Base(destination)
		}
	} else {
		root, absolute, err = openRepo(o.Repo)
		name = ".mcp.json"
	}
	if err != nil {
		return result, err
	}
	defer root.Close()
	if o.Client == "codex" && !o.Global {
		name = ".codex/config.toml"
		if err := root.Mkdir(".codex", 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return result, err
		}
		info, err := root.Lstat(".codex")
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return result, errors.New(".codex must be a real directory, not a symlink")
		}
		clientRoot, err := root.OpenRoot(".codex")
		if err != nil {
			return result, err
		}
		defer clientRoot.Close()
		root = clientRoot
	}
	result.Path = filepath.Join(absolute, filepath.FromSlash(name))
	name = filepath.Base(name)
	before, mode, exists, err := readConfig(root, name)
	if err != nil {
		return result, err
	}
	args := []string{"serve", "--config", o.Config, "--project", o.Project, "--transport", "stdio"}
	if o.Target != "" {
		absoluteTarget := filepath.Join(cfg.VaultRoot, filepath.FromSlash(cfg.ProjectRoot))
		args = []string{"serve", "--target", absoluteTarget, "--project", cfg.ProjectID, "--transport", "stdio"}
	}
	if o.AllowWrites {
		args = append(args, "--allow-writes")
	}
	args, err = sharedLaunchArgs(before, o.Client, "devvault", binary, args, cfg)
	if err != nil {
		return result, err
	}
	if instructions != nil {
		if err := instructions.setShared(cfg, o.AllowWrites); err != nil {
			return result, err
		}
	}
	var after []byte
	if o.Client == "codex" {
		after, err = mergeCodex(before, binary, args)
	} else {
		after, err = mergeClaude(before, binary, args)
	}
	if err != nil {
		return result, err
	}
	if len(after) > maxConfigBytes {
		return result, errors.New("client configuration would exceed 1048576 bytes")
	}
	result.Binary, result.Args = binary, args
	result.IndexPath, result.AllowWrites = cfg.ProjectRoot+"/index.md", o.AllowWrites
	result.SharedPaths = cfg.SharedGrants()
	if o.AllowWrites {
		result.SharedWritablePaths = append([]string(nil), cfg.SharedPaths...)
	}
	if !bytes.Equal(before, after) {
		if err := writeConfig(root, name, before, after, mode, exists); err != nil {
			return result, err
		}
		result.Changed = true
	}
	if instructions != nil {
		if err := instructions.apply(&result); err != nil {
			return result, fmt.Errorf("connection saved, but agent instructions failed; fix the error and rerun install: %w", err)
		}
	}
	return result, nil
}

func mergeCodex(before []byte, binary string, args []string) ([]byte, error) {
	return mergeCodexNamed(before, "devvault", binary, args)
}

func mergeCodexNamed(before []byte, server, binary string, args []string) ([]byte, error) {
	var current map[string]any
	if err := toml.Unmarshal(before, &current); err != nil {
		return nil, errors.New("invalid existing Codex TOML; configuration left unchanged")
	}
	entry := map[string]any{"command": binary, "args": args, "startup_timeout_sec": 10, "tool_timeout_sec": 30}
	var block bytes.Buffer
	fmt.Fprintf(&block, "[mcp_servers.%s]\n", server)
	if err := toml.NewEncoder(&block).Encode(entry); err != nil {
		return nil, err
	}
	var desired map[string]any
	if err := toml.Unmarshal(block.Bytes(), &desired); err != nil {
		return nil, err
	}
	if servers, ok := current["mcp_servers"]; ok {
		table, ok := servers.(map[string]any)
		if !ok {
			return nil, errors.New("Codex mcp_servers must be a table")
		}
		if existing, ok := table[server]; ok {
			// Per-tool policy belongs to the client/user, not the installer.
			// Preserve it verbatim when the installed launch settings match.
			if fields, ok := existing.(map[string]any); ok {
				fields = maps.Clone(fields)
				existing = fields
				if _, ok := fields["tools"].(map[string]any); ok {
					delete(fields, "tools")
				}
				want := desired["mcp_servers"].(map[string]any)[server].(map[string]any)
				if extra, ok := sharedArgsExtension(fields["args"], want["args"]); ok {
					fields["args"] = want["args"]
					if reflect.DeepEqual(fields, want) {
						return appendCodexShared(before, server, extra)
					}
				}
			}
			if reflect.DeepEqual(existing, desired["mcp_servers"].(map[string]any)[server]) {
				return before, nil
			}
			return nil, namedConflict(server)
		}
	}
	after := append(append(append([]byte{}, before...), '\n'), block.Bytes()...)
	// This also rejects attempts to extend an existing inline TOML table.
	if err := toml.Unmarshal(after, new(map[string]any)); err != nil {
		return nil, errors.New("cannot append devvault to this TOML layout; configuration left unchanged")
	}
	return after, nil
}

func mergeClaude(before []byte, binary string, args []string) ([]byte, error) {
	return mergeClaudeNamed(before, "devvault", binary, args)
}

func mergeClaudeNamed(before []byte, server, binary string, args []string) ([]byte, error) {
	// Claude expands environment references even in literal command/arg paths.
	for _, value := range append([]string{binary}, args...) {
		if strings.Contains(value, "${") {
			return nil, errors.New("Claude connection paths cannot contain ${ environment references")
		}
	}
	current := map[string]json.RawMessage{}
	if len(before) != 0 {
		var err error
		current, err = jsonObject(before)
		if err != nil {
			return nil, errors.New("invalid existing Claude JSON object; configuration left unchanged")
		}
	}
	servers := map[string]json.RawMessage{}
	if value, ok := current["mcpServers"]; ok {
		var err error
		servers, err = jsonObject(value)
		if err != nil {
			return nil, errors.New("Claude mcpServers must be an object")
		}
	}
	entry, err := json.Marshal(map[string]any{"type": "stdio", "command": binary, "args": args})
	if err != nil {
		return nil, err
	}
	if existing, ok := servers[server]; ok {
		var a, b map[string]any
		if json.Unmarshal(existing, &a) != nil || json.Unmarshal(entry, &b) != nil {
			return nil, namedConflict(server)
		}
		if reflect.DeepEqual(a, b) {
			return before, nil
		}
		if _, ok := sharedArgsExtension(a["args"], b["args"]); !ok {
			return nil, namedConflict(server)
		}
		a["args"] = b["args"]
		if !reflect.DeepEqual(a, b) {
			return nil, namedConflict(server)
		}
	}
	servers[server] = entry
	current["mcpServers"], err = json.Marshal(servers)
	if err != nil {
		return nil, err
	}
	after, err := json.MarshalIndent(current, "", "  ")
	return append(after, '\n'), err
}

// Reject duplicate keys at the two levels we merge rather than silently
// discarding an authored setting. Unrelated values remain raw JSON.
func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("expected a JSON object")
	}
	object := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("expected a JSON object key")
		}
		if _, exists := object[key]; exists {
			return nil, errors.New("duplicate JSON object key")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		object[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after JSON object")
	}
	return object, nil
}

func namedConflict(server string) error {
	return fmt.Errorf("%s already exists with different settings; edit or remove that entry manually before installing", server)
}

func executable(name string) (string, error) {
	binary, err := filepath.EvalSymlinks(name)
	if err != nil {
		return "", fmt.Errorf("resolve bridge executable: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("bridge executable must be a regular executable file")
	}
	return binary, nil
}

func readConfig(root *os.Root, name string) ([]byte, os.FileMode, bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0o600, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("client configuration must be a regular file, not a symlink or special file")
	}
	if info.Size() > maxConfigBytes {
		return nil, 0, false, errors.New("client configuration exceeds 1048576 bytes")
	}
	f, err := root.OpenFile(name, readFlags, 0)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, 0, false, errors.New("client configuration changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, 0, false, err
	}
	if len(data) > maxConfigBytes {
		return nil, 0, false, errors.New("client configuration exceeds 1048576 bytes")
	}
	return data, info.Mode().Perm(), true, nil
}

func writeConfig(root *os.Root, name string, before, after []byte, mode os.FileMode, exists bool) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	tmp := ".vault-bridge-" + hex.EncodeToString(token[:])
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, writeErr := f.Write(after)
	if writeErr == nil {
		writeErr = f.Chmod(mode)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	current, _, stillExists, err := readConfig(root, name)
	if err != nil {
		return err
	}
	if exists != stillExists || !bytes.Equal(before, current) {
		return errors.New("client configuration changed during installation; retry")
	}
	if !exists {
		// Linking the completed file provides exclusive creation without ever
		// exposing a partially written configuration or replacing another file.
		return root.Link(tmp, name)
	}
	return root.Rename(tmp, name)
}

// Traverse each ancestor to reject symlinks, then retain the repository handle
// so configuration access cannot escape it if directories are changed.
func openRepo(name string) (*os.Root, string, error) {
	if name == "" {
		return nil, "", errors.New("--repo must name an existing code repository")
	}
	return openDirectory(name, false)
}

// Only the installer creates missing configuration directories; project-local
// installation still requires an existing repository. Never follow symlinks.
func openDirectory(name string, create bool) (*os.Root, string, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, "", err
	}
	base := filepath.VolumeName(absolute) + string(filepath.Separator)
	if absolute == base {
		return nil, "", errors.New("configuration directory cannot be the filesystem root")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", err
	}
	for _, part := range strings.Split(strings.TrimPrefix(absolute, base), string(filepath.Separator)) {
		info, err := root.Lstat(part)
		if create && errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(part, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				root.Close()
				return nil, "", err
			}
			info, err = root.Lstat(part)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			if err != nil {
				return nil, "", fmt.Errorf("configuration directory must exist without symlink components: %w", err)
			}
			return nil, "", errors.New("configuration directory must exist without symlink components")
		}
		next, err := root.OpenRoot(part)
		root.Close()
		if err != nil {
			return nil, "", err
		}
		root = next
	}
	return root, absolute, nil
}
