// Package web embeds the admin's static assets (CSS, JS, htmx).
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var Static embed.FS

// NoDir hides directories so a file server built on fsys never lists them.
func NoDir(fsys fs.FS) fs.FS { return noDirFS{fsys} }

type noDirFS struct{ fs.FS }

func (n noDirFS) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}
