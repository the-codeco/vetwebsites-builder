package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestName is the file kept on the hosting listing what was uploaded, so the next publish uploads only what changed and
// removes only what this program put there. It starts with ".ht", which web servers refuse to show to visitors.
const ManifestName = ".htwb-manifest.json"

const maxManifestBytes = 8 << 20

// File is one file of a site to publish: its path in the site, its size and its checksum.
type File struct {
	Path   string
	Size   int64
	SHA256 string
}

// Manifest is what was last published.
type Manifest struct {
	Version     string            `json:"version"`
	PublishedAt time.Time         `json:"published_at"`
	Files       map[string]string `json:"files"` // path -> sha256
}

// Report says what a publish did.
type Report struct {
	Uploaded, Deleted, Unchanged int
	Bytes                        int64
}

// Progress is told how far a publish has got.
type Progress func(done, total int, message string)

// Options change how a publish runs.
type Options struct {
	// Force uploads every file again, whatever the hosting's list says is already there.
	Force bool
	// Version names this publish in the manifest.
	Version string
}

// validName says whether a file path from a manifest is safe to put on somebody's hosting: a plain relative path.
func validName(name string) bool {
	if name == "" || len(name) > 400 || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "\\\x00\r\n:*?\"<>|") {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > 255 {
			return false
		}
	}
	return name != ManifestName
}

// order puts files in the order they are uploaded: pictures, scripts and styles first, then the server settings, then the
// pages, with the home page last, so a visitor mid-publish never gets a page whose pictures are not there yet.
func order(name string) int {
	switch {
	case name == ".htaccess":
		return 1
	case name == "index.html":
		return 3
	case strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".txt") || name == "search-index.json":
		return 2
	default:
		return 0
	}
}

// Run publishes the files in dir to the remote. files lists them with their checksums (from the export), and each file is
// checked against its checksum as it is read, so what is uploaded is exactly what was exported. Files already on the hosting
// with the same checksum are skipped; files this program uploaded before that are no longer part of the site are removed;
// nothing else on the hosting is touched.
func Run(ctx context.Context, r Remote, dir string, files []File, opt Options, prog Progress) (*Report, error) {
	if prog == nil {
		prog = func(int, int, string) {}
	}
	want := map[string]File{}
	for _, f := range files {
		if !validName(f.Path) {
			return nil, fmt.Errorf("refusing to publish a file with the name %q", f.Path)
		}
		want[f.Path] = f
	}
	prev := Manifest{Files: map[string]string{}}
	if !opt.Force {
		data, ok, err := r.ReadFile(ManifestName, maxManifestBytes)
		if err != nil {
			return nil, fmt.Errorf("could not read the hosting's list of published files: %w", err)
		}
		if ok {
			if json.Unmarshal(data, &prev) != nil || prev.Files == nil {
				prev = Manifest{Files: map[string]string{}} // damaged: treat the hosting as empty (nothing is deleted)
			}
		}
	} else if data, ok, err := r.ReadFile(ManifestName, maxManifestBytes); err == nil && ok {
		var old Manifest
		if json.Unmarshal(data, &old) == nil && old.Files != nil {
			prev = Manifest{Files: old.Files} // remembered, so files that are gone from the site are still removed
		}
	}

	var upload []File
	rep := &Report{}
	for _, f := range files {
		if !opt.Force && prev.Files[f.Path] == f.SHA256 {
			rep.Unchanged++
			continue
		}
		upload = append(upload, f)
	}
	sort.SliceStable(upload, func(i, j int) bool {
		if oi, oj := order(upload[i].Path), order(upload[j].Path); oi != oj {
			return oi < oj
		}
		return upload[i].Path < upload[j].Path
	})
	var remove []string
	for name := range prev.Files {
		if _, keep := want[name]; !keep && validName(name) {
			remove = append(remove, name)
		}
	}
	sort.Strings(remove)

	total := len(upload) + len(remove) + 1
	done := 0
	if err := r.MkdirAll(""); err != nil {
		return rep, fmt.Errorf("could not open the folder on the hosting: %w", err)
	}
	for _, f := range upload {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		data, err := readChecked(dir, f)
		if err != nil {
			return rep, err
		}
		if err := r.WriteFile(f.Path, data); err != nil {
			return rep, fmt.Errorf("could not upload %s: %w", f.Path, err)
		}
		rep.Uploaded++
		rep.Bytes += int64(len(data))
		done++
		prog(done, total, "Uploaded "+f.Path)
	}
	for _, name := range remove {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if err := r.Remove(name); err != nil {
			return rep, fmt.Errorf("could not remove %s: %w", name, err)
		}
		rep.Deleted++
		done++
		prog(done, total, "Removed "+name)
	}
	m := Manifest{Version: opt.Version, PublishedAt: time.Now().UTC(), Files: map[string]string{}}
	for _, f := range files {
		m.Files[f.Path] = f.SHA256
	}
	data, _ := json.Marshal(m)
	if err := r.WriteFile(ManifestName, data); err != nil {
		return rep, fmt.Errorf("could not save the list of published files: %w", err)
	}
	prog(total, total, "Done")
	return rep, nil
}

// readChecked reads a file of the export and checks it is what the export said it was.
func readChecked(dir string, f File) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	h, err := root.Open(filepath.FromSlash(f.Path))
	if err != nil {
		return nil, fmt.Errorf("the exported file %s is missing: %w", f.Path, err)
	}
	defer h.Close()
	buf, err := io.ReadAll(h)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf)
	if hex.EncodeToString(sum[:]) != f.SHA256 {
		return nil, errors.New("the exported file " + f.Path + " changed after it was made; build the site again")
	}
	return buf, nil
}
