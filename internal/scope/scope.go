// Package scope contains the shared, deny-by-default path policy.
package scope

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Error is safe to show to a client: callers never place note contents or raw
// filesystem errors (which may contain absolute paths) in Message.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func Err(code, message string) error { return &Error{Code: code, Message: message} }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return "internal_error"
}

// FSError deliberately does not expose an operating system path or error text.
func FSError(err error, operation string) error {
	if errors.Is(err, fs.ErrNotExist) {
		return Err("not_found", operation+": path does not exist")
	}
	if errors.Is(err, fs.ErrPermission) {
		return Err("permission_denied", operation+": path is inaccessible")
	}
	return Err("internal_error", operation+": filesystem access failed")
}

// RelativePath accepts one portable, already-normalized vault-relative path.
// Percent escapes are rejected rather than decoded; there is no URL-path API.
func RelativePath(name string) error {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00%:") || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return Err("invalid_argument", "expected a nonempty portable vault-relative path (maximum 1024 bytes)")
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return Err("invalid_argument", "control characters are not allowed in paths")
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return Err("invalid_argument", "empty and traversal path components are not allowed")
		}
	}
	if path.Clean(name) != name {
		return Err("invalid_argument", "path must be normalized")
	}
	return nil
}

// Visible excludes private and attachment subtrees even if they contain .md
// files. Explicit shared grants never override this exclusion policy.
func Visible(name string) bool {
	for _, part := range strings.Split(name, "/") {
		lower := strings.ToLower(part)
		if strings.HasPrefix(part, ".") {
			return false
		}
		switch lower {
		case "assets", "attachments", "attachment", "secrets", "secret", "node_modules", "vendor", "credentials", "private-keys":
			return false
		}
	}
	return true
}

// Contains uses filesystem-aware boundaries; lookalike directory prefixes are
// not contained. Both names must be normalized absolute filesystem paths.
func Contains(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Beneath applies those same boundaries to slash-separated vault paths.
func Beneath(root, target string) bool {
	return Contains(filepath.FromSlash(root), filepath.FromSlash(target))
}

// CheckComponents rejects every symlink below a retained root handle. os.Root
// additionally prevents a concurrent symlink swap from escaping this root.
// It cannot stop an attacker replacing a note with another file inside the same
// root, hard links, bind mounts, or an in-place concurrent edit.
func CheckComponents(root *os.Root, name string, directory bool) (fs.FileInfo, error) {
	if name == "." {
		info, err := root.Stat(".")
		if err != nil {
			return nil, FSError(err, "inspect root")
		}
		return info, nil
	}
	if err := RelativePath(name); err != nil {
		return nil, err
	}
	parts := strings.Split(name, "/")
	var info fs.FileInfo
	for i := range parts {
		var err error
		info, err = root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, FSError(err, "inspect path")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, Err("permission_denied", "symbolic links are not permitted")
		}
		if (i < len(parts)-1 || directory) && !info.IsDir() {
			return nil, Err("not_found", "expected a directory")
		}
	}
	return info, nil
}
