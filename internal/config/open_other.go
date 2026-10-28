//go:build !unix

package config

import "os"

func openConfig(name string) (*os.File, error) {
	return os.Open(name)
}
