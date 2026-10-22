package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"webbuilder/internal/auth"
	"webbuilder/internal/export"
	"webbuilder/internal/rewrite"
)

// PreviewBuildBase is where the latest static copy of a website is shown to its owner before it is published.
const PreviewBuildBase = "/preview/build"

// KeepBuilds is how many static copies of a website are kept (the latest, and a few older ones to go back to).
const KeepBuilds = 5

// placeholderURL is the address used for a copy made before the owner has said where the site will be published. Its links
// point at example.com, so it is good for looking at, and the publisher refuses to upload a copy made for it.
const placeholderURL = "https://example.com"

// BuildInfo describes a static copy.
type BuildInfo struct {
	Version  string    `json:"version"`
	Made     time.Time `json:"made"`
	Changes  int64     `json:"changes"`
	Files    int       `json:"files"`
	Bytes    int64     `json:"bytes"`
	Warnings []string  `json:"warnings,omitempty"`
	URL      string    `json:"public_url"`
}

func (p *Platform) buildsDir(siteID int64) string {
	return filepath.Join(p.cfg.DataDir, "builds", fmt.Sprint(siteID))
}

// BuildDir is the folder of one static copy.
func (p *Platform) BuildDir(siteID int64, version string) string {
	return filepath.Join(p.buildsDir(siteID), version)
}

// validVersion keeps a version name (from the database or a person) to what the program makes.
func validVersion(v string) bool {
	if len(v) < 3 || len(v) > 40 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r == '-') {
			return false
		}
	}
	return v[0] == 'b'
}

// Build makes a new static copy of a website from its current content and remembers it as the latest.
func (p *Platform) Build(ctx context.Context, s Site) (BuildInfo, error) {
	rt, release, err := p.mgr.Acquire(ctx, s.ID, s.Schema)
	if err != nil {
		return BuildInfo{}, err
	}
	defer release()
	public := s.PublicURL
	if public == "" {
		public = placeholderURL
	}
	made := time.Now().UTC()
	if err := os.MkdirAll(p.buildsDir(s.ID), 0o755); err != nil {
		return BuildInfo{}, err
	}
	// two copies made in the same second must not share a folder: the older one may be what the hosting was just given
	version := "b" + made.Format("20060102-150405")
	for n := 2; ; n++ {
		if _, err := os.Stat(p.BuildDir(s.ID, version)); os.IsNotExist(err) {
			break
		}
		version = fmt.Sprintf("b%s-%d", made.Format("20060102-150405"), n)
	}
	dir := p.BuildDir(s.ID, version)
	res, err := export.Build(ctx, rt.App.ExportSource(), export.Options{PublicURL: public, BuilderURL: p.cfg.BaseURL, SiteToken: s.Token}, dir)
	if err != nil {
		os.RemoveAll(dir)
		return BuildInfo{}, err
	}
	info := BuildInfo{Version: version, Made: made, Changes: res.Changes, Files: len(res.Files), Warnings: res.Warnings, URL: public}
	for _, f := range res.Files {
		info.Bytes += f.Size
	}
	meta, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(dir, ".wb-build.json"), meta, 0o644); err != nil {
		return BuildInfo{}, err
	}
	if _, err := p.pool.Exec(ctx, `UPDATE sites SET build_version = $2, build_changes = $3, built_at = $4 WHERE id = $1`, s.ID, version, res.Changes, made); err != nil {
		return BuildInfo{}, err
	}
	p.pruneBuilds(s.ID, version)
	return info, nil
}

// pruneBuilds deletes all but the newest KeepBuilds copies (never the one just made).
func (p *Platform) pruneBuilds(siteID int64, keep string) {
	entries, err := os.ReadDir(p.buildsDir(siteID))
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && validVersion(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > KeepBuilds {
		if names[0] != keep {
			os.RemoveAll(filepath.Join(p.buildsDir(siteID), names[0]))
		}
		names = names[1:]
	}
}

// unpublished says how many changes the website has that are not in its latest copy (all of them when there is no copy).
func (p *Platform) unpublished(ctx context.Context, s Site) (int64, error) {
	rt, release, err := p.mgr.Acquire(ctx, s.ID, s.Schema)
	if err != nil {
		return 0, err
	}
	defer release()
	n, err := rt.App.ChangeCount(ctx)
	if err != nil {
		return 0, err
	}
	if s.PublishedChanges < 0 {
		return n, nil
	}
	return max(n-s.PublishedChanges, 0), nil
}

// handleBuild makes a new copy of the website and shows it. A person can only build their own website (or the one an
// admin is acting on), and the copy is made from what is saved now.
func (p *Platform) handleBuild(w http.ResponseWriter, r *http.Request, rt *Runtime) {
	id := auth.CurrentIdentity(r)
	s, err := p.siteByID(r.Context(), rt.SiteID)
	if err != nil {
		serverError(w, err)
		return
	}
	info, err := p.Build(r.Context(), s)
	if err != nil {
		serverError(w, err)
		return
	}
	p.audit(r.Context(), actorOf(&auth.Identity{Email: id.Email}), &s, "made a preview copy", fmt.Sprintf("%d files", info.Files))
	http.Redirect(w, r, PreviewBuildBase+"/", http.StatusSeeOther)
}

// previewBuild shows the latest static copy exactly as it will be published: the files as they are, answered the way the
// hosting will answer them, under /preview/build.
func (p *Platform) previewBuild(w http.ResponseWriter, r *http.Request, rt *Runtime) {
	s, err := p.siteByID(r.Context(), rt.SiteID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !validVersion(s.BuildVersion) {
		http.Error(w, "There is no preview copy yet. Make one first.", http.StatusNotFound)
		return
	}
	dir := p.BuildDir(s.ID, s.BuildVersion)
	base := ""
	if u := s.PublicURL; u != "" {
		if i := strings.Index(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/"); i >= 0 {
			base = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")[i:], "/")
		}
	}
	h, err := export.Serve(dir, base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "The preview copy is gone. Make a new one.", http.StatusNotFound)
			return
		}
		serverError(w, err)
		return
	}
	defer h.Close()
	r2 := r.Clone(r.Context())
	r2.URL.Path = strings.TrimPrefix(r.URL.Path, PreviewBuildBase)
	if r2.URL.Path == "" {
		r2.URL.Path = "/"
	}
	r2.RequestURI = ""
	w.Header().Set("Cache-Control", "no-store")
	rewrite.Base(PreviewBuildBase, h).ServeHTTP(w, r2)
}
