package ui

import "embed"

//go:embed templates/*.html
var Templates embed.FS

//go:embed assets/logo.png
var Logo []byte

//go:embed assets/logo-dark.png
var LogoDark []byte

//go:embed assets/favicon.png
var Favicon []byte
