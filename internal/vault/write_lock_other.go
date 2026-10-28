//go:build !linux && !darwin

package vault

import (
	"context"
	"os"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func openProjectWriteLock(*os.Root) (*os.File, error) {
	return nil, scope.Err("permission_denied", "project writes are supported only on Linux and macOS")
}

func openWriteSnapshot(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}

func lockWriteFile(context.Context, *os.File) error {
	return scope.Err("permission_denied", "project writes are supported only on Linux and macOS")
}

func unlockWriteFile(*os.File) {}
