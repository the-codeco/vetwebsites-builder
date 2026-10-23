package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"webbuilder/internal/blocks"
	"webbuilder/internal/contact"
	"webbuilder/internal/content"
	"webbuilder/internal/media"
	"webbuilder/internal/menus"
	"webbuilder/internal/newsletter"
	"webbuilder/internal/site"
	"webbuilder/internal/themes"
)

// Invalid is returned for a file that is not a usable backup. Its message is meant for the person who chose the
// file, so it says what is wrong in plain words.
type Invalid struct{ Msg string }

func (e *Invalid) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &Invalid{fmt.Sprintf(format, a...)} }

// Archive is a backup file that has been checked and cleaned. Content holds only values that are safe to store.
type Archive struct {
	Manifest Manifest
	Content  Content
	// Warnings lists things that were left out because they were not valid. The rest of the backup is still usable.
	Warnings []string
	// Images maps each image's path to what it really is, found by reading its bytes.
	Images map[string]media.Info
	// Sizes maps each image's path to its size in bytes.
	Sizes map[string]int64
}

type warnings struct {
	list    []string
	dropped int
}

const maxWarnings = 40

func (w *warnings) add(format string, a ...any) {
	if len(w.list) < maxWarnings {
		w.list = append(w.list, fmt.Sprintf(format, a...))
	} else {
		w.dropped++
	}
}

func (w *warnings) all() []string {
	if w.dropped > 0 {
		return append(w.list, fmt.Sprintf("…and %d more.", w.dropped))
	}
	return w.list
}

// readEntry reads one file of the ZIP, never more than max bytes whatever the ZIP claims about its size.
func readEntry(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("%s can't be read: the backup file is damaged.", f.Name)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, invalid("%s can't be read: the backup file is damaged.", f.Name)
	}
	if int64(len(data)) > max {
		return nil, invalid("%s is larger than a backup may hold.", f.Name)
	}
	return data, nil
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// decodeStrict reads JSON and refuses fields it doesn't know and anything after the value.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected data after the end")
	}
	return nil
}

// entries checks the ZIP's table of contents: only the files a backup holds, with safe names, of sane sizes.
func entries(zr *zip.Reader) (map[string]*zip.File, error) {
	if len(zr.File) > MaxEntries {
		return nil, invalid("This file holds too many files to be a backup of this site.")
	}
	files := make(map[string]*zip.File, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") && f.UncompressedSize64 == 0 {
			continue // folders carry nothing
		}
		if !f.FileInfo().Mode().IsRegular() {
			return nil, invalid("This file holds something that isn't an ordinary file (%q), so it isn't a backup.", name)
		}
		if f.NonUTF8 || !utf8.ValidString(name) {
			return nil, invalid("This file holds a file with an unreadable name, so it isn't a backup.")
		}
		var limit uint64
		switch {
		case name == ManifestName:
			limit = MaxManifestBytes
		case name == SiteName:
			limit = MaxJSONBytes
		case strings.HasPrefix(name, MediaPrefix) && mediaPathRe.MatchString(strings.TrimPrefix(name, MediaPrefix)):
			limit = MaxMediaBytes
		default:
			return nil, invalid("This file holds a file a backup doesn't have (%q), so it isn't a backup of this site.", trunc(name, 60))
		}
		if _, dup := files[name]; dup {
			return nil, invalid("This file lists %q twice, so it isn't a backup.", name)
		}
		if f.UncompressedSize64 > limit {
			return nil, invalid("%s is larger than a backup may hold.", name)
		}
		total += f.UncompressedSize64
		if total > MaxTotalBytes {
			return nil, invalid("This backup is larger than this site accepts.")
		}
		files[name] = f
	}
	return files, nil
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Inspect reads a backup file and checks everything in it. Nothing is changed. The result holds the cleaned
// content, or an *Invalid error saying why the file can't be used.
func (s *Service) Inspect(ctx context.Context, ra io.ReaderAt, size int64) (*Archive, error) {
	a, _, err := s.inspect(ctx, ra, size)
	return a, err
}

// inspect is Inspect, also returning the ZIP's files so a restore can read the images again.
func (s *Service) inspect(ctx context.Context, ra io.ReaderAt, size int64) (*Archive, map[string]*zip.File, error) {
	if size > MaxArchiveBytes {
		return nil, nil, invalid("This file is larger than a backup this site accepts (%d MB).", MaxArchiveBytes>>20)
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return nil, nil, invalid("This isn't a backup file. Choose a .zip made by the Backup page.")
	}
	files, err := entries(zr)
	if err != nil {
		return nil, nil, err
	}

	mf, ok := files[ManifestName]
	if !ok {
		return nil, nil, invalid("This ZIP has no manifest.json, so it isn't a backup made by this program.")
	}
	raw, err := readEntry(mf, MaxManifestBytes)
	if err != nil {
		return nil, nil, err
	}
	var man Manifest
	if err := decodeStrict(raw, &man); err != nil {
		return nil, nil, invalid("The backup's manifest.json can't be read, so the file is damaged or isn't a backup.")
	}
	switch {
	case man.Format != Format:
		return nil, nil, invalid("This ZIP isn't a backup made by this program.")
	case man.Version < 1 || man.Version > Version:
		return nil, nil, invalid("This backup was made by a newer version of the program (format %d). Update the program, then try again.", man.Version)
	}

	// every file must be listed, and every listed file must be there
	for name := range files {
		if name != ManifestName && man.Files[name] == "" {
			return nil, nil, invalid("%s is in the file but not in its manifest, so the file was changed after it was made.", name)
		}
	}
	for name := range man.Files {
		if _, ok := files[name]; !ok {
			return nil, nil, invalid("%s is listed in the manifest but missing, so the file is incomplete.", name)
		}
	}
	if _, ok := files[SiteName]; !ok {
		return nil, nil, invalid("This backup has no site.json, so it holds no site.")
	}

	siteRaw, err := readEntry(files[SiteName], MaxJSONBytes)
	if err != nil {
		return nil, nil, err
	}
	if sumHex(siteRaw) != man.Files[SiteName] {
		return nil, nil, invalid("site.json doesn't match its checksum: the file is damaged or was changed.")
	}
	var c Content
	if err := decodeStrict(siteRaw, &c); err != nil {
		return nil, nil, invalid("site.json can't be read (%s), so the file is damaged or isn't a backup.", trunc(err.Error(), 100))
	}
	if len(c.Pages) > MaxPages || len(c.Posts) > MaxPosts || len(c.Categories) > MaxCategories || len(c.Media) > MaxMedia ||
		len(c.Subscribers) > MaxSubscribers || len(c.Messages) > MaxMessages {
		return nil, nil, invalid("This backup holds more than this site accepts.")
	}
	if !man.Personal && (len(c.Subscribers) > 0 || len(c.Messages) > 0) {
		return nil, nil, invalid("This backup holds personal data its manifest doesn't mention, so it was changed after it was made.")
	}

	w := &warnings{}
	a := &Archive{Manifest: man}
	a.Content.Settings = s.cleanSettings(c.Settings, c.Menus, w)

	imgs, err := s.checkImages(ctx, files, man, c.Media, w)
	if err != nil {
		return nil, nil, err
	}
	a.Content.Media, a.Images, a.Sizes = imgs.records, imgs.info, imgs.sizes

	a.Content.Reusables = cleanReusables(c.Reusables, w)
	a.Content.Redirects = cleanRedirects(c.Redirects, w)
	a.Content.Fonts = cleanFonts(c.Fonts, w)
	a.Content.Pages = cleanPages(c.Pages, a.Content.Reusables, w)
	a.Content.Categories = cleanCategories(c.Categories, w)
	a.Content.Posts = cleanPosts(c.Posts, a.Content.Categories, w)
	a.Content.Menus = cleanMenus(c.Menus, w)
	if man.Personal {
		a.Content.Subscribers = cleanSubscribers(c.Subscribers, w)
		a.Content.Messages = cleanMessages(c.Messages, w)
	}
	a.Warnings = w.all()
	a.Manifest.Counts = counts(a.Content) // what will really be restored, not what the file claims
	return a, files, nil
}

// --- settings ---

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) > max {
		return string([]rune(s)[:max])
	}
	return s
}

func (s *Service) cleanSettings(in Settings, menuList []Menu, w *warnings) Settings {
	// a backup from before the footer builder kept the footer's links as a menu: they move into the footer
	var footerMenu []menus.Item
	for _, m := range menuList {
		if m.Location == menus.FooterKey {
			footerMenu = m.Items
			break // the first one, as cleanMenus keeps the first of each
		}
	}
	out := Settings{
		Title:      strings.TrimSpace(clip(in.Title, 100)),
		ShowSearch: in.ShowSearch,
		CustomCSS:  in.CustomCSS,
		SEO:        content.CleanSEO(in.SEO),
		Consent:    content.CleanConsent(in.Consent),
		Footer:     content.CleanFooter(in.Footer, footerMenu, content.CleanDetails(in.Details)["in_footer"] == true),
		Brand:      content.CleanBrand(in.Brand),
		Details:    content.CleanDetails(in.Details),
		Tracking:   content.CleanTracking(in.Tracking),
		Tokens:     map[string]map[string]string{},
	}
	if out.Title == "" {
		out.Title = "My Site"
	}
	delete(out.SEO, "title") // the site itself has no title override
	if _, ok := s.engine.Lookup(in.ActiveTheme); ok {
		out.ActiveTheme = in.ActiveTheme
	} else {
		out.ActiveTheme = themes.DefaultTheme
		w.add("The theme %q isn't installed here, so the default theme is used.", trunc(in.ActiveTheme, 40))
	}
	if themes.ValidColorMode(in.ColorMode) {
		out.ColorMode = in.ColorMode
	} else {
		out.ColorMode = themes.ColorToggle
	}
	if themes.ValidColorSwitch(in.ColorSwitch) {
		out.ColorSwitch = in.ColorSwitch
	} else {
		out.ColorSwitch = themes.SwitchFloating // a backup from before the switch had a place of its own
	}
	if utf8.RuneCountInString(in.CustomCSS) > 20000 {
		out.CustomCSS = ""
		w.add("The custom CSS was longer than allowed, so it was left out.")
	}

	skipped := 0
	for name, toks := range in.Tokens {
		th, ok := s.engine.Lookup(name)
		if !ok {
			skipped += len(toks)
			continue
		}
		clean := map[string]string{}
		for key, val := range toks {
			if v, ok := themeToken(th, key, val); ok {
				clean[key] = v
			} else {
				skipped++
			}
		}
		if len(clean) > 0 {
			out.Tokens[name] = clean
		}
	}
	if skipped > 0 {
		w.add("%d appearance settings weren't valid for the themes installed here and were skipped.", skipped)
	}
	return out
}

// themeToken checks a saved appearance value against a theme and returns it in its stored form (colours in lower
// case): a colour, size or font the theme has, or a dark-mode colour.
func themeToken(th *themes.Theme, key, val string) (string, bool) {
	if base, ok := strings.CutPrefix(key, themes.DarkPrefix); ok {
		for _, tk := range th.DarkColors() {
			if tk.Key == base && themes.ValidToken("color", strings.ToLower(val)) {
				return strings.ToLower(val), true
			}
		}
		return "", false
	}
	for _, tk := range th.Tokens {
		if tk.Key != key {
			continue
		}
		if tk.Kind == "color" {
			val = strings.ToLower(val)
		}
		return val, themes.ValidToken(tk.Kind, val)
	}
	return "", false
}

// --- images ---

type images struct {
	records []Media
	info    map[string]media.Info
	sizes   map[string]int64
}

// checkImages reads every image in the backup, proves its checksum, and decides what it is from its bytes.
func (s *Service) checkImages(ctx context.Context, files map[string]*zip.File, man Manifest, recs []Media, w *warnings) (images, error) {
	out := images{info: map[string]media.Info{}, sizes: map[string]int64{}}
	listed := map[string]bool{}
	for _, r := range recs {
		if !mediaPathRe.MatchString(r.Path) {
			return out, invalid("The backup lists an image with an invalid name (%q).", trunc(r.Path, 60))
		}
		if listed[r.Path] {
			return out, invalid("The backup lists the image %s twice.", r.Path)
		}
		listed[r.Path] = true
		if _, ok := files[MediaPrefix+r.Path]; !ok {
			return out, invalid("The backup lists the image %s but doesn't contain it.", r.Path)
		}
	}
	for name := range files {
		if p, ok := strings.CutPrefix(name, MediaPrefix); ok && !listed[p] {
			return out, invalid("The backup contains the image %s without listing it, so it was changed after it was made.", p)
		}
	}
	for _, r := range recs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		f := files[MediaPrefix+r.Path]
		data, err := readEntry(f, MaxMediaBytes)
		if err != nil {
			return out, err
		}
		if sumHex(data) != man.Files[MediaPrefix+r.Path] {
			return out, invalid("The image %s doesn't match its checksum: the file is damaged or was changed.", r.Path)
		}
		info, err := media.Inspect(data)
		if err == nil {
			_, err = media.MakeThumb(data, info) // a full decode: proves it is a real picture
		}
		if err != nil {
			w.add("The image %s isn't a usable picture and was left out.", r.Path)
			continue
		}
		if !strings.HasSuffix(r.Path, info.Ext) {
			w.add("The image %s isn't the kind of file its name says and was left out.", r.Path)
			continue
		}
		r.OriginalName = media.CleanName(r.OriginalName)
		r.Alt = clip(strings.TrimSpace(r.Alt), 300)
		if r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now().Add(24*time.Hour)) {
			r.CreatedAt = time.Now()
		}
		out.records = append(out.records, r)
		out.info[r.Path] = info
		out.sizes[r.Path] = int64(len(data))
	}
	return out, nil
}

// --- pages, posts, categories ---

func status(s string) string {
	if s == "published" {
		return "published"
	}
	return "draft"
}

func cleanPages(in []Page, shared []Reusable, w *warnings) []Page {
	var out []Page
	known := map[int64]bool{}
	for _, r := range shared {
		known[r.ID] = true
	}
	// a shared block whose library block isn't in the file can't be shown, so it is left out
	usable := func(b Block) bool { return b.Type != blocks.ReusableType || known[blocks.RefOf(b.Props)] }
	seen := map[string]bool{}
	for _, p := range in {
		if msg := site.CheckSlug(p.Slug); msg != "" {
			w.add("A page with the address %q was left out: %s", trunc(p.Slug, 40), strings.ToLower(strings.TrimSuffix(msg, ".")))
			continue
		}
		if seen[p.Slug] {
			w.add("A second page with the address %q was left out.", p.Slug)
			continue
		}
		seen[p.Slug] = true
		title := strings.TrimSpace(clip(p.Title, 200))
		if title == "" {
			title = p.Slug
		}
		np := Page{Slug: p.Slug, Title: title, Status: status(p.Status), SEO: content.CleanSEO(p.SEO), UpdatedAt: sane(p.UpdatedAt), Blocks: []Block{}}
		np.PublishAt = cleanSchedule(np.Status, p.PublishAt)
		skipped, total := 0, 0 // total counts blocks inside layouts too: the page limit is for all of them
		for _, b := range p.Blocks {
			bt, ok := blocks.Get(b.Type)
			if !ok || total >= MaxBlocksPerPage || !usable(Block{Type: b.Type, Props: bt.Clean(b.Props)}) {
				skipped += 1 + countChildren(b)
				continue
			}
			total++
			nb := Block{Type: b.Type, Props: bt.Clean(b.Props), Style: blocks.CleanStyleMap(b.Style)}
			if b.Type == blocks.LayoutType {
				cols := blocks.LayoutColumns(nb.Props)
				nb.Children = make([][]Block, cols)
				for i, col := range b.Children {
					to := min(i, cols-1) // blocks of a column the layout doesn't have go in the last one
					for _, ch := range col {
						ct, ok := blocks.Get(ch.Type)
						if !ok || !blocks.Nestable(ch.Type) || total >= MaxBlocksPerPage || len(nb.Children[to]) >= blocks.MaxBlocksPerColumn || !usable(Block{Type: ch.Type, Props: ct.Clean(ch.Props)}) {
							skipped++
							continue
						}
						total++
						nb.Children[to] = append(nb.Children[to], Block{Type: ch.Type, Props: ct.Clean(ch.Props), Style: blocks.CleanStyleMap(ch.Style)})
					}
				}
			} else {
				skipped += countChildren(b) // only a layout has blocks inside it
			}
			np.Blocks = append(np.Blocks, nb)
		}
		if skipped > 0 {
			w.add("%d blocks on the page %q weren't a kind this version knows and were left out.", skipped, p.Slug)
		}
		out = append(out, np)
	}
	return out
}

// cleanFonts checks the uploaded fonts: each must look like a WOFF2 file, with a safe name and a known weight, one
// per family and weight, and no more than the site keeps.
func cleanFonts(in []Font, w *warnings) []Font {
	var out []Font
	seen := map[string]bool{}
	for _, f := range in {
		family, ok := content.CleanFamily(f.Family)
		key := fmt.Sprintf("%s|%d", strings.ToLower(family), f.Weight)
		switch {
		case !ok || (f.Weight != 400 && f.Weight != 700) || content.CheckFont(f.Data) != nil:
			w.add("A font called %q was left out because it is not a valid WOFF2 file.", trunc(f.Family, 40))
			continue
		case seen[key]:
			w.add("A second font file for %q was left out.", family)
			continue
		case len(out) >= content.MaxFonts:
			w.add("Fonts beyond the first %d were left out.", content.MaxFonts)
			return out
		}
		seen[key] = true
		out = append(out, Font{Family: family, Weight: f.Weight, Data: f.Data})
	}
	return out
}

// cleanRedirects checks the redirect list: both addresses are cleaned the way the editor cleans them, each old
// address appears once, and a redirect that would lead back to itself is left out.
func cleanRedirects(in []Redirect, w *warnings) []Redirect {
	var out []Redirect
	seen := map[string]int{}
	for _, r := range in {
		from, err1 := content.CleanRedirectFrom(r.From)
		to, err2 := content.CleanRedirectTo(r.To)
		switch {
		case err1 != nil || err2 != nil:
			w.add("A redirect from %q was left out because an address in it is not valid.", trunc(r.From, 40))
			continue
		case seen[from] > 0:
			w.add("A second redirect from %q was left out.", from)
			continue
		case len(out) >= content.MaxRedirects:
			w.add("Redirects beyond the first %d were left out.", content.MaxRedirects)
			return dropRedirectLoops(out, w)
		}
		seen[from]++
		out = append(out, Redirect{From: from, To: to, Permanent: r.Permanent, Auto: r.Auto})
	}
	return dropRedirectLoops(out, w)
}

// dropRedirectLoops removes redirects that lead, through others, back to their own address.
func dropRedirectLoops(list []Redirect, w *warnings) []Redirect {
	to := map[string]string{}
	for _, r := range list {
		if strings.HasPrefix(r.To, "/") {
			p, _, _ := strings.Cut(r.To, "?")
			p, _, _ = strings.Cut(p, "#")
			to[r.From] = content.NormalizePath(p)
		}
	}
	loops := func(from string) bool {
		at := to[from]
		for hops := 0; at != "" && hops < 30; hops++ {
			if at == from {
				return true
			}
			at = to[at]
		}
		return false
	}
	var out []Redirect
	for _, r := range list {
		if loops(r.From) {
			w.add("The redirect from %q was left out because it leads back to itself.", r.From)
			continue
		}
		out = append(out, r)
	}
	return out
}

// cleanReusables checks the shared block library: only kinds that can be shared, cleaned like anything typed in,
// each with its own id, and no more than the library holds.
func cleanReusables(in []Reusable, w *warnings) []Reusable {
	var out []Reusable
	seen := map[int64]bool{}
	for _, r := range in {
		bt, ok := blocks.Get(r.Type)
		switch {
		case !ok || !blocks.CanBeShared(r.Type):
			w.add("A shared block %q wasn't a kind of block that can be shared and was left out.", trunc(r.Name, 40))
			continue
		case r.ID < 1 || seen[r.ID]:
			w.add("A shared block %q had a missing or repeated id and was left out.", trunc(r.Name, 40))
			continue
		case len(out) >= content.MaxReusable:
			w.add("Shared blocks beyond the first %d were left out.", content.MaxReusable)
			return out
		}
		seen[r.ID] = true
		name := content.CleanReusableName(r.Name)
		if name == "" {
			name = "Shared block"
		}
		out = append(out, Reusable{ID: r.ID, Name: name, Type: r.Type, Props: bt.Clean(r.Props), Style: blocks.CleanStyleMap(r.Style)})
	}
	return out
}

// countChildren is how many blocks a block holds inside its columns.
func countChildren(b Block) int {
	n := 0
	for _, col := range b.Children {
		n += len(col)
	}
	return n
}

// cleanSchedule keeps a draft's publish time only if it is still in the future (and not absurdly far off): a time
// that has passed would publish the page the moment the backup is restored, which nobody asked for.
func cleanSchedule(status string, at *time.Time) *time.Time {
	if status != "draft" || at == nil {
		return nil
	}
	t := at.UTC().Truncate(time.Minute)
	if content.CheckSchedule(&t, time.Now()) != nil {
		return nil
	}
	return &t
}

func sane(t time.Time) time.Time {
	if t.IsZero() || t.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || t.After(time.Now().Add(24*time.Hour)) {
		return time.Now().UTC().Truncate(time.Second)
	}
	return t
}

func cleanCategories(in []Category, w *warnings) []Category {
	var out []Category
	seen := map[string]bool{}
	for _, c := range in {
		name := strings.TrimSpace(clip(c.Name, 100))
		if msg := site.CheckSlugFormat(c.Slug); msg != "" || name == "" || seen[c.Slug] {
			w.add("A category %q was left out because it isn't valid or is a duplicate.", trunc(c.Slug, 40))
			continue
		}
		seen[c.Slug] = true
		out = append(out, Category{Slug: c.Slug, Name: name})
	}
	return out
}

func cleanPosts(in []Post, cats []Category, w *warnings) []Post {
	known := map[string]bool{}
	for _, c := range cats {
		known[c.Slug] = true
	}
	var out []Post
	seen := map[string]bool{}
	for _, p := range in {
		if msg := site.CheckSlugFormat(p.Slug); msg != "" {
			w.add("A post with the address %q was left out: %s", trunc(p.Slug, 40), strings.ToLower(strings.TrimSuffix(msg, ".")))
			continue
		}
		if seen[p.Slug] {
			w.add("A second post with the address %q was left out.", p.Slug)
			continue
		}
		seen[p.Slug] = true
		title := strings.TrimSpace(clip(p.Title, 200))
		if title == "" {
			title = p.Slug
		}
		np := Post{
			Slug: p.Slug, Title: title, Excerpt: strings.TrimSpace(clip(p.Excerpt, 600)),
			Body:  blocks.SanitizeHTML(clip(p.Body, 500_000)),
			Cover: blocks.SafeImageURL(clip(p.Cover, 2000)), Status: status(p.Status), SEO: content.CleanSEO(p.SEO),
			UpdatedAt: sane(p.UpdatedAt), Categories: []string{},
		}
		np.PublishAt = cleanSchedule(np.Status, p.PublishAt)
		if np.Status == "published" {
			t := sane(p.UpdatedAt)
			if p.PublishedAt != nil {
				t = sane(*p.PublishedAt)
			}
			np.PublishedAt = &t
		} else if p.PublishedAt != nil {
			t := sane(*p.PublishedAt) // an unpublished post remembers when it first went out
			np.PublishedAt = &t
		}
		have := map[string]bool{}
		for _, c := range p.Categories {
			if known[c] && !have[c] {
				have[c] = true
				np.Categories = append(np.Categories, c)
			}
		}
		out = append(out, np)
	}
	return out
}

// --- menus ---

func cleanMenus(in []Menu, w *warnings) []Menu {
	var out []Menu
	seen := map[string]bool{}
	for _, m := range in {
		if m.Location == menus.FooterKey {
			continue // moved into the footer by cleanSettings
		}
		sec, ok := menus.SectionByKey(m.Location)
		if !ok || seen[m.Location] {
			w.add("A menu called %q isn't one this site has and was left out.", trunc(m.Location, 40))
			continue
		}
		seen[m.Location] = true
		items, err := menus.Validate(sec, m.Items)
		if err != nil {
			w.add("The %s menu wasn't valid (%s) and was left empty.", strings.ToLower(sec.Label), strings.TrimSuffix(trunc(err.Error(), 80), "."))
			items = nil
		}
		if items == nil {
			items = []menus.Item{}
		}
		style := menus.CleanStyle(m.Style)
		if !sec.Styled {
			style = menus.Style{}
		}
		out = append(out, Menu{Location: m.Location, Items: items, Sticky: m.Sticky, Style: style})
	}
	return out
}

// --- personal data ---

func cleanSubscribers(in []Subscriber, w *warnings) []Subscriber {
	var out []Subscriber
	seen := map[string]bool{}
	bad := 0
	for _, s := range in {
		email, ok := newsletter.Normalize(s.Email)
		if !ok || seen[email] {
			bad++
			continue
		}
		seen[email] = true
		ns := Subscriber{Email: email, CreatedAt: sane(s.CreatedAt)}
		if s.ConfirmedAt != nil {
			t := sane(*s.ConfirmedAt)
			ns.ConfirmedAt = &t
		}
		if newsletter.TokenRe.MatchString(s.UnsubToken) {
			ns.UnsubToken = s.UnsubToken
		}
		out = append(out, ns)
	}
	if bad > 0 {
		w.add("%d subscriber addresses weren't valid (or were repeated) and were left out.", bad)
	}
	return out
}

func cleanMessages(in []Message, w *warnings) []Message {
	var out []Message
	bad := 0
	for _, m := range in {
		v, problem := contact.Validate(contact.Input{Name: m.Name, Email: m.Email, Subject: m.Subject, Body: m.Body})
		if problem != "" {
			bad++
			continue
		}
		nm := Message{Name: v.Name, Email: v.Email, Subject: v.Subject, Body: v.Body, Read: m.Read, CreatedAt: sane(m.CreatedAt)}
		for _, r := range m.Replies {
			if body, problem := contact.CleanReply(r.Body); problem == "" {
				nm.Replies = append(nm.Replies, Reply{Body: body, CreatedAt: sane(r.CreatedAt)})
			}
		}
		out = append(out, nm)
	}
	if bad > 0 {
		w.add("%d contact messages weren't valid and were left out.", bad)
	}
	return out
}
