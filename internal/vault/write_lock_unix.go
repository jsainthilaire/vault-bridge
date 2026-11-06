//go:build linux || darwin

package vault

import (
	"context"
	"errors"
	"os"
	"syscall"

	"github.com/jsainthilaire/vault-bridge/internal/scope"
)

func openProjectWriteLock(root *os.Root) (*os.File, error) {
	before, err := root.Lstat(writeLockName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, scope.FSError(err, "inspect write lock")
	}
	if err == nil && !before.Mode().IsRegular() {
		return nil, scope.Err("permission_denied", "write lock must be a regular file")
	}
	// Open existing locks without O_CREATE. Creating exclusively and retrying
	// after EEXIST keeps simultaneous first writers on the same lock inode.
	var file *os.File
	for attempt := 0; attempt < 3; attempt++ {
		file, err = root.OpenFile(writeLockName, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if !errors.Is(err, os.ErrNotExist) {
			break
		}
		file, err = root.OpenFile(writeLockName, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return nil, scope.FSError(err, "open write lock")
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || (before != nil && !os.SameFile(before, opened)) {
		file.Close()
		return nil, scope.Err("permission_denied", "write lock changed during access")
	}
	return file, nil
}

func openWriteSnapshot(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func lockWriteFile(ctx context.Context, file *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return scope.FSError(err, "lock Markdown writes")
		}
		if err := waitForWriteLock(ctx); err != nil {
			return err
		}
	}
}

func unlockWriteFile(file *os.File) {
	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
