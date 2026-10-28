//go:build unix

package vault

import (
	"os"
	"syscall"
)

// A nonblocking open keeps a concurrent swap from a regular note to a FIFO
// from hanging the process before the post-open regular-file check.
func openNote(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
