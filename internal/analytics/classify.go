// Package analytics counts page views without cookies, scripts or personal data.
//
// Every public page view becomes one row: the path, the referring site's host,
// a rough device and browser, and a visitor hash. The hash is derived from the
// IP address and browser string with a secret and the current day, and then the
// inputs are discarded, so nothing stored identifies anyone and the same person
// looks like a different visitor tomorrow.
package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// botWords marks user agents that aren't people. It is a courtesy filter to keep
// the numbers honest, not a security measure.
var botWords = []string{
	"bot", "crawl", "spider", "slurp", "curl", "wget", "python", "go-http-client", "java/", "libwww",
	"headless", "lighthouse", "pingdom", "uptime", "monitor", "preview", "facebookexternalhit", "scrapy",
	"httpclient", "okhttp", "axios", "node-fetch", "feedfetcher", "feedly", "validator",
}

// IsBot reports whether a User-Agent looks automated. An empty one counts as a bot.
func IsBot(ua string) bool {
	ua = strings.ToLower(ua)
	if ua == "" {
		return true
	}
	for _, w := range botWords {
		if strings.Contains(ua, w) {
			return true
		}
	}
	return false
}

// Device is "mobile", "tablet" or "desktop".
func Device(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "ipad"), strings.Contains(ua, "tablet"),
		strings.Contains(ua, "android") && !strings.Contains(ua, "mobile"):
		return "tablet"
	case strings.Contains(ua, "mobi"), strings.Contains(ua, "iphone"), strings.Contains(ua, "android"):
		return "mobile"
	}
	return "desktop"
}

// Browser names the family. Order matters: Chrome's string contains "Safari",
// and Edge's and Opera's contain "Chrome".
func Browser(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"), strings.Contains(ua, "EdgA/"), strings.Contains(ua, "EdgiOS/"):
		return "Edge"
	case strings.Contains(ua, "OPR/"), strings.Contains(ua, "Opera"):
		return "Opera"
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS/"):
		return "Firefox"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS/"):
		return "Chrome"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	}
	return "Other"
}

// ReferrerHost returns the host a visitor came from, or "" when they came
// directly, from a non-web source, or from this very site (own, as written in
// the Host header or the saved domain; "www." is ignored on both sides).
func ReferrerHost(referer string, own ...string) string {
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host == "" || len(host) > 100 {
		return ""
	}
	for _, o := range own {
		o = strings.TrimPrefix(strings.ToLower(o), "www.")
		if i := strings.LastIndexByte(o, ':'); i > 0 && !strings.Contains(o[i:], "]") {
			o = o[:i] // drop a port
		}
		if o != "" && o == host {
			return ""
		}
	}
	return host
}

// VisitorHash is an opaque id for "this browser on this day". The secret never
// leaves the database, and the day changes the result, so ids can't be matched
// across days.
func VisitorHash(secret, day, ip, ua string) string {
	h := sha256.New()
	for _, part := range []string{secret, day, ip, ua} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// Countable says which paths are public pages worth counting. A page that merely
// starts with one of these words (/administrators) still counts.
func Countable(path string) bool {
	for _, p := range []string{"/admin", "/setup", "/invite", "/static", "/media", "/theme", "/internal"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return false
		}
	}
	return len(path) <= 200
}
