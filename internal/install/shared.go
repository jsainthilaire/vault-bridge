package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/jsainthilaire/vault-bridge/internal/config"
)

func stringArgs(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	args := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		args[i] = text
	}
	return args, true
}

func splitSharedArgs(args []string) (base, shared []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--shared" {
			if i+1 == len(args) {
				return nil, nil, errors.New("installed --shared flag has no folder")
			}
			i++
			shared = append(shared, args[i])
		} else if value, ok := strings.CutPrefix(arg, "--shared="); ok {
			shared = append(shared, value)
		} else {
			base = append(base, arg)
		}
	}
	return base, shared, nil
}

// Retain previously installed grants only when executable and all other launch
// arguments match. Reconfiguration must not silently change identity or writes.
func sharedLaunchArgs(before []byte, client, server, binary string, base []string, cfg *config.Scope) ([]string, error) {
	var doc map[string]any
	key := "mcp_servers"
	if client == "codex" {
		if err := toml.Unmarshal(before, &doc); err != nil {
			return nil, errors.New("invalid existing Codex TOML; configuration left unchanged")
		}
	} else {
		key = "mcpServers"
		if len(before) > 0 {
			if err := json.Unmarshal(before, &doc); err != nil {
				return nil, errors.New("invalid existing Claude JSON; configuration left unchanged")
			}
		}
	}
	args := append([]string{}, base...)
	var installedShared []string
	if servers, ok := doc[key].(map[string]any); ok {
		if value, exists := servers[server]; exists {
			entry, ok := value.(map[string]any)
			if !ok || entry["command"] != binary {
				return nil, namedConflict(server)
			}
			installed, ok := stringArgs(entry["args"])
			if !ok {
				return nil, namedConflict(server)
			}
			installedBase, shared, err := splitSharedArgs(installed)
			if err != nil {
				return nil, err
			}
			if !slices.Equal(base, installedBase) {
				return nil, namedConflict(server)
			}
			args, installedShared = installed, shared
		}
	}
	if err := config.AddShared(cfg, installedShared); err != nil {
		return nil, err
	}
	for _, name := range cfg.SharedPaths {
		if !slices.Contains(installedShared, name) {
			args = append(args, "--shared", name)
		}
	}
	return args, nil
}

// Merge functions allow only appending explicit shared grants. Full launch
// comparisons still reject permission changes, another project, or another binary.
func sharedArgsExtension(old, desired any) ([]string, bool) {
	a, ok := stringArgs(old)
	if !ok {
		return nil, false
	}
	b, ok := stringArgs(desired)
	if !ok || len(b) <= len(a) || !slices.Equal(a, b[:len(a)]) {
		return nil, false
	}
	extra := b[len(a):]
	if len(extra)%2 != 0 {
		return nil, false
	}
	for i := 0; i < len(extra); i += 2 {
		if extra[i] != "--shared" || extra[i+1] == "" {
			return nil, false
		}
	}
	return extra, true
}

// Append after the last string token, preserving all existing TOML bytes,
// including multiline arrays, comments, quoted keys and nested policy tables.
// Using the pinned TOML parser avoids matching strings or comments as syntax.
func appendCodexShared(before []byte, server string, extra []string) ([]byte, error) {
	var parser unstable.Parser
	parser.KeepComments = true
	parser.Reset(before)
	var table []string
	insertAt := -1
	var visit func(*unstable.Node, []string)
	visit = func(node *unstable.Node, prefix []string) {
		if node.Kind != unstable.KeyValue {
			return
		}
		keys := append([]string{}, prefix...)
		it := node.Key()
		for it.Next() {
			keys = append(keys, string(it.Node().Data))
		}
		value := node.Value()
		if slices.Equal(keys, []string{"mcp_servers", server, "args"}) && value.Kind == unstable.Array {
			children := value.Children()
			for children.Next() {
				child := children.Node()
				if child.Kind == unstable.String {
					insertAt = int(child.Raw.Offset + child.Raw.Length)
				}
			}
		} else if value.Kind == unstable.InlineTable {
			children := value.Children()
			for children.Next() {
				visit(children.Node(), keys)
			}
		}
	}
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = nil
			keys := node.Key()
			for keys.Next() {
				table = append(table, string(keys.Node().Data))
			}
		case unstable.KeyValue:
			visit(node, table)
		}
	}
	if parser.Error() != nil || insertAt < 0 || insertAt > len(before) {
		return nil, errors.New("cannot safely extend the existing Codex args array; configuration left unchanged")
	}
	encoded, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	var after bytes.Buffer
	after.Write(before[:insertAt])
	after.WriteString(", ")
	after.Write(encoded[1 : len(encoded)-1])
	after.Write(before[insertAt:])
	if err := toml.Unmarshal(after.Bytes(), new(map[string]any)); err != nil {
		return nil, errors.New("cannot safely extend the existing Codex args array; configuration left unchanged")
	}
	return after.Bytes(), nil
}
