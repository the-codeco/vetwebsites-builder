// Package media is the image library: checking uploads, removing their hidden details, thumbnails, resized copies,
// the image editor and the records in the media table.
package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "golang.org/x/image/webp"
)

const (
	// MaxFileSize is the largest file accepted (and produced by the editor).
	MaxFileSize = 10 << 20
	// MaxDimension is the most pixels on either side; MaxPixels is the most in total.
	MaxDimension = 8000
	MaxPixels    = 24_000_000
	// MinCrop is the smallest crop box, in pixels each way.
	MinCrop = 16
	// ThumbMax is the longest side of a thumbnail; images no larger than this are their own thumbnail.
	ThumbMax = 400
)

var (
	ErrNotFound    = errors.New("media: not found")
	ErrTooLarge    = errors.New("media: file too large")
	ErrUnsupported = errors.New("media: not a supported image")
	ErrCorrupt     = errors.New("media: the image can't be read")
	ErrDimensions  = errors.New("media: too many pixels")
	ErrNotEditable = errors.New("media: this kind of image can't be edited")
	ErrBadEdit     = errors.New("media: invalid edit")
)

// Info describes an image decided from its bytes.
type Info struct {
	Mime          string
	Ext           string // with the dot: ".jpg", ".png", ".gif" or ".webp"
	Width, Height int
}

// Item is one image in the library.
type Item struct {
	ID           int64
	Path         string // below the media folder, e.g. 2026/10/<24 hex>.jpg
	ThumbPath    string // empty or equal to Path when the image is its own thumbnail
	Mime         string
	SizeBytes    int64
	OriginalName string
	Alt          string
	Width        int
	Height       int
	CreatedAt    time.Time
	UsedOn       int // number of pages whose blocks use the image
}

func (i Item) URL() string { return "/media/" + i.Path }

func (i Item) ThumbURL() string {
	if i.ThumbPath == "" {
		return i.URL()
	}
	return "/media/" + i.ThumbPath
}

func (i Item) SizeLabel() string {
	switch {
	case i.SizeBytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(i.SizeBytes)/(1<<20))
	case i.SizeBytes >= 1<<10:
		return fmt.Sprintf("%d KB", (i.SizeBytes+512)>>10)
	}
	return fmt.Sprintf("%d bytes", i.SizeBytes)
}

// Inspect decides what a file is from its bytes (never from its name) and checks its size and dimensions.
func Inspect(data []byte) (Info, error) {
	if len(data) > MaxFileSize {
		return Info{}, ErrTooLarge
	}
	var info Info
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		info.Mime, info.Ext = "image/jpeg", ".jpg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		info.Mime, info.Ext = "image/png", ".png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		info.Mime, info.Ext = "image/gif", ".gif"
	case len(data) > 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		info.Mime, info.Ext = "image/webp", ".webp"
	default:
		return Info{}, ErrUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return Info{}, ErrCorrupt
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension || cfg.Width*cfg.Height > MaxPixels {
		return Info{}, ErrDimensions
	}
	info.Width, info.Height = cfg.Width, cfg.Height
	return info, nil
}

// CleanName turns a client's file name into a safe display name: no folders, no control characters, not too long.
func CleanName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError || r == '<' || r == '>' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 100 {
		name = string(r[:100])
	}
	if name == "" || name == "." || name == ".." {
		return "image"
	}
	return name
}
