// Package templates contains the Markdown sources used by vault and project initialization.
package templates

import "embed"

// Files are embedded so the CLI does not depend on its working directory.
//
//go:embed *.tmpl
var Files embed.FS
