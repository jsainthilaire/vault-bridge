//go:build !unix

package vault

import "os"

func openNote(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
