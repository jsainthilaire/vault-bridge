package config

import (
	"os"
	"slices"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

const MaxSharedPaths = 32

// SharedGrants returns all shared read scopes, retaining overlapping selectors
// so callers can continue using an exact existing shared:<path> section.
func (s *Scope) SharedGrants() []string {
	paths := append(append([]string{}, s.SharedReadPaths...), s.SharedPaths...)
	slices.Sort(paths)
	return slices.Compact(paths)
}

func (s *Scope) CanWriteShared(name string) bool {
	if !s.AllowWrites {
		return false
	}
	for _, shared := range s.SharedPaths {
		if name != shared && scope.Beneath(shared, name) {
			return true
		}
	}
	return false
}

// AddShared validates trusted CLI grants before changing the scope. Unlike
// legacy YAML read grants, --shared may explicitly select all of 05-Knowledge.
func AddShared(s *Scope, paths []string) error {
	if len(paths) > MaxSharedPaths || len(s.SharedPaths) > MaxSharedPaths {
		return scope.Err("resource_limit", "at most 32 shared folders may be granted")
	}
	merged := append(append([]string{}, s.SharedPaths...), paths...)
	slices.Sort(merged)
	merged = slices.Compact(merged)
	copy := *s
	copy.SharedPaths = merged
	if len(copy.SharedGrants()) > MaxSharedPaths {
		return scope.Err("resource_limit", "at most 32 shared folders may be granted")
	}
	if len(merged) == 0 {
		return nil
	}
	root, err := os.OpenRoot(s.VaultRoot)
	if err != nil {
		return scope.FSError(err, "open vault for shared grants")
	}
	defer root.Close()
	for _, name := range merged {
		if err := scope.RelativePath(name); err != nil {
			return err
		}
		if !scope.Visible(name) || !scope.Beneath("05-Knowledge", name) {
			return scope.Err("permission_denied", "--shared must name 05-Knowledge or a visible folder beneath it")
		}
		if _, err := scope.CheckComponents(root, name, true); err != nil {
			return err
		}
	}
	s.SharedPaths = merged
	return nil
}
