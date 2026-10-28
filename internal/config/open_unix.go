//go:build unix

package config

import (
	"os"
	"syscall"
)

// O_NONBLOCK prevents a regular-file-to-FIFO race from hanging before f.Stat
// can enforce the regular-file policy. It has no effect on regular files.
func openConfig(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
