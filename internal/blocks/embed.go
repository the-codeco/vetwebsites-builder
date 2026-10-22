package blocks

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Hosts a page may load into an iframe. The public Content-Security-Policy lists exactly these
// (see FrameSources), so even a block that got a different address past validation would not load.
const (
	hostYouTube = "www.youtube-nocookie.com"
	hostVimeo   = "player.vimeo.com"
	hostOSM     = "www.openstreetmap.org"
	hostGoogle  = "www.google.com" // Google Maps, only for map blocks set to use it
)

// FrameSources is the CSP frame-src list for public pages.
const FrameSources = "https://" + hostYouTube + " https://" + hostVimeo + " https://" + hostOSM + " https://" + hostGoogle

// Video is an embedded player for a video block.
type Video struct {
	Src string // iframe address
}

var (
	youTubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vimeoID   = regexp.MustCompile(`^[0-9]{1,12}$`)
	vimeoHash = regexp.MustCompile(`^[0-9a-f]{6,20}$`)
)

// VideoOf works out how to show the address an author pasted. It accepts YouTube and Vimeo page
// links, shown through their privacy-friendly players.
// Anything else gives ok == false, so nothing is embedded.
func VideoOf(raw string) (v Video, ok bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.User != nil {
		return Video{}, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch host {
	case "youtu.be":
		if len(segs) == 1 && youTubeID.MatchString(segs[0]) {
			return Video{Src: youTubeSrc(segs[0], u.Query().Get("t"))}, true
		}
	case "youtube.com", "m.youtube.com", "youtube-nocookie.com":
		id := ""
		switch {
		case len(segs) == 1 && segs[0] == "watch":
			id = u.Query().Get("v")
		case len(segs) == 2 && (segs[0] == "embed" || segs[0] == "shorts" || segs[0] == "live"):
			id = segs[1]
		}
		if youTubeID.MatchString(id) {
			return Video{Src: youTubeSrc(id, u.Query().Get("t"))}, true
		}
	case "vimeo.com", "player.vimeo.com":
		// vimeo.com/123456789, vimeo.com/123456789/abcdef1234 (unlisted), player.vimeo.com/video/123456789
		if len(segs) > 0 && segs[0] == "video" {
			segs = segs[1:]
		}
		if len(segs) >= 1 && len(segs) <= 2 && vimeoID.MatchString(segs[0]) {
			src := "https://" + hostVimeo + "/video/" + segs[0] + "?dnt=1"
			h := u.Query().Get("h")
			if len(segs) == 2 {
				h = segs[1]
			}
			if h != "" {
				if !vimeoHash.MatchString(h) {
					return Video{}, false
				}
				src += "&h=" + h
			}
			return Video{Src: src}, true
		}
	}
	return Video{}, false
}

func youTubeSrc(id, start string) string {
	src := "https://" + hostYouTube + "/embed/" + id + "?rel=0"
	if n, err := strconv.Atoi(strings.TrimSuffix(start, "s")); err == nil && n > 0 && n < 86400*2 {
		src += "&start=" + strconv.Itoa(n)
	}
	return src
}

// Map is a place on an OpenStreetMap map: the embedded view (Src) and a link to the full map (Link).
type Map struct {
	Src, Link string
}

// MapOf builds the map for "latitude, longitude" (decimal degrees, as copied from any maps site)
// at a zoom of 1–19. Anything that isn't a real position gives ok == false.
func MapOf(coords string, zoom float64) (m Map, ok bool) {
	parts := strings.Split(strings.NewReplacer(";", ",", "\t", " ").Replace(coords), ",")
	if len(parts) == 1 {
		parts = strings.Fields(parts[0])
	}
	if len(parts) != 2 {
		return Map{}, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 85 || math.Abs(lon) > 180 {
		return Map{}, false
	}
	z := int(math.Min(math.Max(math.Round(zoom), 1), 19))

	// the visible area: a web-map tile is 256px wide at this zoom, and the frame is about 3 tiles across
	lonSpan := 360 / math.Pow(2, float64(z)) * 1.5
	latSpan := lonSpan / 2 * math.Cos(lat*math.Pi/180)
	left, right := math.Max(lon-lonSpan/2, -180), math.Min(lon+lonSpan/2, 180)
	bottom, top := math.Max(lat-latSpan/2, -85), math.Min(lat+latSpan/2, 85)

	f := func(x float64) string { return strconv.FormatFloat(x, 'f', 6, 64) }
	return Map{
		Src: fmt.Sprintf("https://%s/export/embed.html?bbox=%s%%2C%s%%2C%s%%2C%s&layer=mapnik&marker=%s%%2C%s",
			hostOSM, f(left), f(bottom), f(right), f(top), f(lat), f(lon)),
		Link: fmt.Sprintf("https://%s/?mlat=%s&mlon=%s#map=%d/%s/%s", hostOSM, f(lat), f(lon), z, f(lat), f(lon)),
	}, true
}

// Lines splits a text area into its non-empty lines (a pricing plan's features).
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Items reads the numbered slots of a repeated-item block ("q1_question", "q2_question"…) as a list.
// prefix is "q", keys are the fields of one item ("question", "answer"), and a slot whose first key
// is empty is skipped. limit (a number, or the text of one such as "2") caps how many slots are read.
func Items(props map[string]any, prefix string, limit any, keys ...string) []map[string]any {
	max := 0
	switch x := limit.(type) {
	case int:
		max = x
	case float64:
		max = int(x)
	case string:
		max, _ = strconv.Atoi(x)
	}
	var out []map[string]any
	for i := 1; i <= max && len(keys) > 0; i++ {
		item := map[string]any{"N": i}
		for _, k := range keys {
			item[k] = props[prefix+strconv.Itoa(i)+"_"+k]
		}
		if s, _ := item[keys[0]].(string); strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

// Map providers a map block can use.
const (
	MapOSM    = "osm"    // OpenStreetMap, placed by latitude and longitude; no tracking
	MapGoogle = "google" // Google Maps, found by street address; Google sets its own cookies
)

// maxPlace is the longest address sent to Google Maps.
const maxPlace = 200

// GoogleMapOf builds a Google Maps embed for a street address or place name, at a zoom of 1–19.
// The address becomes a search for the embed, so it is URL-escaped and nothing else of the author's goes
// into the address. Empty or unreadable text (control characters, too long) gives ok == false.
func GoogleMapOf(place string, zoom float64) (m Map, ok bool) {
	place = strings.Join(strings.Fields(place), " ")
	if place == "" || len([]rune(place)) > maxPlace {
		return Map{}, false
	}
	for _, r := range place {
		if r < 0x20 || r == 0x7f {
			return Map{}, false
		}
	}
	z := int(math.Min(math.Max(math.Round(zoom), 1), 19))
	q := url.QueryEscape(place)
	return Map{
		Src:  fmt.Sprintf("https://%s/maps?q=%s&z=%d&output=embed", hostGoogle, q, z),
		Link: fmt.Sprintf("https://%s/maps/search/?api=1&query=%s", hostGoogle, q),
	}, true
}

// MapFor picks the right kind of map for a block's settings.
func MapFor(provider, coords, place string, zoom float64) (Map, bool) {
	if provider == MapGoogle {
		return GoogleMapOf(place, zoom)
	}
	return MapOf(coords, zoom)
}
