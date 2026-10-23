// Package backup saves the whole site (pages, posts, menus, appearance, images, and optionally the subscriber list
// and contact messages) into one ZIP file, and restores a site from such a file.
//
// A backup is a ZIP holding:
//
//	manifest.json   what it is, when it was made, how many of everything, and a SHA-256 for every other file
//	site.json       all the content, as plain JSON
//	media/…         the original uploaded images, under the names the pages use
//
// Nothing in it is a database dump, and user accounts are never included (their password hashes would travel
// with the file). A file being restored is not trusted: it is checked entry by entry, every value is cleaned the
// same way the editor cleans what a person types, and every image is decoded, before anything is changed.
package backup

import (
	"encoding/json"
	"regexp"
	"time"

	"webbuilder/internal/menus"
)

const (
	// Format and Version identify a backup file. A file with another format, or a newer version, is refused.
	Format  = "webbuilder-backup"
	Version = 6 // 2: blocks can hold child blocks (layouts); 3: the shared block library; 4: the footer builder; 5: page and header layout; 6: page sections and the sidebar

	ManifestName = "manifest.json"
	SiteName     = "site.json"
	MediaPrefix  = "media/"
)

// Limits on what is accepted when restoring, so a hostile or damaged file can't use up memory, disk or time.
const (
	MaxArchiveBytes  = 1 << 30  // the uploaded file
	MaxEntries       = 20000    // files in the ZIP
	MaxJSONBytes     = 64 << 20 // site.json
	MaxManifestBytes = 4 << 20  // manifest.json
	MaxMediaBytes    = 10 << 20 // one image (the upload limit)
	MaxTotalBytes    = 4 << 30  // everything, once unpacked
	MaxPages         = 5000
	MaxBlocksPerPage = 200
	MaxPosts         = 50000
	MaxCategories    = 1000
	MaxMedia         = 10000
	MaxSubscribers   = 500000
	MaxMessages      = 100000
)

// Manifest describes a backup file.
type Manifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	SiteTitle string    `json:"site_title"`
	// Personal says whether the subscriber list and contact messages are inside.
	Personal bool   `json:"personal"`
	Counts   Counts `json:"counts"`
	// Missing lists images the site knew about but whose files were not on disk, so they could not be saved.
	Missing []string `json:"missing,omitempty"`
	// Files maps every other file in the ZIP to its SHA-256 (hex).
	Files map[string]string `json:"files"`
}

// Counts is how much of each thing a backup holds.
type Counts struct {
	Pages       int `json:"pages"`
	Blocks      int `json:"blocks"`
	Posts       int `json:"posts"`
	Categories  int `json:"categories"`
	Media       int `json:"media"`
	Shared      int `json:"shared,omitempty"`
	Subscribers int `json:"subscribers,omitempty"`
	Messages    int `json:"messages,omitempty"`
}

// Content is site.json.
type Content struct {
	Settings    Settings     `json:"settings"`
	Pages       []Page       `json:"pages"`
	Reusables   []Reusable   `json:"reusables,omitempty"`
	Redirects   []Redirect   `json:"redirects,omitempty"`
	Fonts       []Font       `json:"fonts,omitempty"`
	Posts       []Post       `json:"posts"`
	Categories  []Category   `json:"categories"`
	Menus       []Menu       `json:"menus"`
	Media       []Media      `json:"media"`
	Subscribers []Subscriber `json:"subscribers,omitempty"`
	Messages    []Message    `json:"messages,omitempty"`
}

// Settings are the site's look and search settings. The domain, email settings and who may log in belong to the
// server rather than to the site's content, so they are neither saved nor restored.
type Settings struct {
	Title       string                       `json:"title"`
	ActiveTheme string                       `json:"active_theme"`
	Tokens      map[string]map[string]string `json:"tokens"`
	CustomCSS   string                       `json:"custom_css"`
	SEO         map[string]any               `json:"seo"`
	ShowSearch  bool                         `json:"show_search"`
	ColorMode   string                       `json:"color_mode"`
	ColorSwitch string                       `json:"color_switch,omitempty"`
	Consent     map[string]any               `json:"consent"`
	Footer      map[string]any               `json:"footer"`
	Brand       map[string]any               `json:"brand"`
	Details     map[string]any               `json:"details"`
	Tracking    map[string]any               `json:"tracking"`
	Layout      map[string]any               `json:"layout,omitempty"`  // format 5
	Sidebar     map[string]any               `json:"sidebar,omitempty"` // format 6
}

type Page struct {
	Slug      string         `json:"slug"`
	Title     string         `json:"title"`
	Status    string         `json:"status"`
	SEO       map[string]any `json:"seo"`
	UpdatedAt time.Time      `json:"updated_at"`
	PublishAt *time.Time     `json:"publish_at,omitempty"` // a draft that goes live at this time
	Parent    string         `json:"parent,omitempty"`     // the slug of the page this one belongs under (format 6)
	Position  int            `json:"position,omitempty"`
	NoSidebar bool           `json:"no_sidebar,omitempty"`
	Blocks    []Block        `json:"blocks"`
}

// Font is an uploaded WOFF2 font file; its bytes travel inside site.json (they are small).
type Font struct {
	Family string `json:"family"`
	Weight int    `json:"weight"`
	Data   []byte `json:"data"`
}

// Redirect sends an old address to a new one.
type Redirect struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Permanent bool   `json:"permanent"`
	Auto      bool   `json:"auto,omitempty"`
}

// Reusable is a block in the shared library. Blocks on pages that show it hold its ID in their "ref" setting; the
// IDs only tie the two together inside the file, and are replaced when it is restored.
type Reusable struct {
	ID    int64          `json:"id"`
	Name  string         `json:"name"`
	Type  string         `json:"type"`
	Props map[string]any `json:"props"`
	Style map[string]any `json:"style"`
}

type Block struct {
	Type  string         `json:"type"`
	Props map[string]any `json:"props"`
	Style map[string]any `json:"style"`
	// Children are the blocks in each column of a layout block.
	Children [][]Block `json:"children,omitempty"`
}

type Post struct {
	Slug        string         `json:"slug"`
	Title       string         `json:"title"`
	Excerpt     string         `json:"excerpt"`
	Body        string         `json:"body"`
	Cover       string         `json:"cover"`
	Status      string         `json:"status"`
	SEO         map[string]any `json:"seo"`
	PublishedAt *time.Time     `json:"published_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	PublishAt   *time.Time     `json:"publish_at,omitempty"`
	Categories  []string       `json:"categories"` // by slug
}

type Category struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type Menu struct {
	Location string       `json:"location"`
	Items    []menus.Item `json:"items"`
	Sticky   bool         `json:"sticky"`
	Style    menus.Style  `json:"style,omitempty"`
}

type Media struct {
	Path         string    `json:"path"`
	OriginalName string    `json:"original_name"`
	Alt          string    `json:"alt"`
	CreatedAt    time.Time `json:"created_at"`
}

type Subscriber struct {
	Email       string     `json:"email"`
	CreatedAt   time.Time  `json:"created_at"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	// UnsubToken keeps the unsubscribe link in newsletters already sent working after a restore.
	UnsubToken string `json:"unsub_token,omitempty"`
}

type Message struct {
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
	Replies   []Reply   `json:"replies,omitempty"`
}

type Reply struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// mediaPathRe is the name of an original image as the media library stores it: year/month/random.ext.
var mediaPathRe = regexp.MustCompile(`^\d{4}/(0[1-9]|1[0-2])/[0-9a-f]{24}\.(jpg|png|gif|webp)$`)

func marshal(v any) ([]byte, error) { return json.MarshalIndent(v, "", " ") }
