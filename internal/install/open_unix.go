//go:build unix

package install

import (
	"os"
	"syscall"
)

const readFlags = os.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW
