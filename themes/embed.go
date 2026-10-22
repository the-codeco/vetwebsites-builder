// Package themefiles embeds every theme directory (themes/<name>/...).
package themefiles

import "embed"

//go:embed */*
var FS embed.FS
