//go:build !unix

package install

import "os"

const readFlags = os.O_RDONLY
