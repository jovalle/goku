package ui

import "embed"

//go:embed templates/*.html
var Templates embed.FS

//go:embed assets/logo.png
var Logo []byte
