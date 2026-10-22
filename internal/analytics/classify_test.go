package analytics

import "testing"

const (
	chromeWin  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	edgeWin    = chromeWin + " Edg/120.0.0.0"
	operaWin   = chromeWin + " OPR/105.0.0.0"
	firefoxMac = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:121.0) Gecko/20100101 Firefox/121.0"
	safariMac  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15"
	iphone     = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1"
	ipad       = "Mozilla/5.0 (iPad; CPU OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1"
	androidPh  = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36"
	androidTab = "Mozilla/5.0 (Linux; Android 13; SM-X700) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

func TestIsBot(t *testing.T) {
	for _, ua := range []string{"", "Googlebot/2.1 (+http://www.google.com/bot.html)", "curl/8.4.0", "Go-http-client/1.1",
		"Mozilla/5.0 (compatible; bingbot/2.0)", "python-requests/2.31", "Mozilla/5.0 HeadlessChrome/120", "Slackbot-LinkExpanding 1.0"} {
		if !IsBot(ua) {
			t.Errorf("%q should be a bot", ua)
		}
	}
	for _, ua := range []string{chromeWin, edgeWin, firefoxMac, safariMac, iphone, androidPh} {
		if IsBot(ua) {
			t.Errorf("%q is a person", ua)
		}
	}
}

func TestDeviceAndBrowser(t *testing.T) {
	for ua, want := range map[string][2]string{
		chromeWin: {"desktop", "Chrome"}, edgeWin: {"desktop", "Edge"}, operaWin: {"desktop", "Opera"},
		firefoxMac: {"desktop", "Firefox"}, safariMac: {"desktop", "Safari"}, iphone: {"mobile", "Safari"},
		ipad: {"tablet", "Safari"}, androidPh: {"mobile", "Chrome"}, androidTab: {"tablet", "Chrome"},
		"something odd": {"desktop", "Other"},
	} {
		if d, b := Device(ua), Browser(ua); d != want[0] || b != want[1] {
			t.Errorf("%q: got %s/%s, want %s/%s", ua, d, b, want[0], want[1])
		}
	}
}

func TestReferrerHost(t *testing.T) {
	own := []string{"localhost:8080", "example.com"}
	for ref, want := range map[string]string{
		"":                                  "",
		"https://www.Google.com/search?q=x": "google.com",
		"http://news.ycombinator.com/item":  "news.ycombinator.com",
		"https://example.com/about":         "", // from this site
		"https://www.example.com/":          "", // www of this site
		"http://localhost:8080/blog":        "",
		"android-app://com.google.android":  "", // not a web page
		"javascript:alert(1)":               "",
		"not a url \x7f":                    "",
	} {
		if got := ReferrerHost(ref, own...); got != want {
			t.Errorf("ReferrerHost(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestVisitorHash(t *testing.T) {
	a := VisitorHash("secret", "2026-10-12", "203.0.113.5", chromeWin)
	if a != VisitorHash("secret", "2026-10-12", "203.0.113.5", chromeWin) {
		t.Error("same visitor, same day must give the same id")
	}
	for name, other := range map[string]string{
		"next day":     VisitorHash("secret", "2026-10-13", "203.0.113.5", chromeWin),
		"other ip":     VisitorHash("secret", "2026-10-12", "203.0.113.6", chromeWin),
		"other agent":  VisitorHash("secret", "2026-10-12", "203.0.113.5", firefoxMac),
		"other secret": VisitorHash("other", "2026-10-12", "203.0.113.5", chromeWin),
		"shifted join": VisitorHash("secret", "2026-10-12", "203.0.113.", "5"+chromeWin),
	} {
		if other == a {
			t.Errorf("%s must change the id", name)
		}
	}
	if len(a) != 24 {
		t.Errorf("unexpected id %q", a)
	}
}

func TestCountable(t *testing.T) {
	for p, want := range map[string]bool{
		"/": true, "/about": true, "/blog/hello": true, "/administrators": true, "/setup-guide": true,
		"/admin": false, "/admin/pages": false, "/setup": false, "/invite/abc": false,
		"/static/admin.css": false, "/media/a.jpg": false, "/theme/default/x.css": false,
	} {
		if Countable(p) != want {
			t.Errorf("Countable(%q) = %v, want %v", p, !want, want)
		}
	}
	long := "/"
	for len(long) < 250 {
		long += "a"
	}
	if Countable(long) {
		t.Error("absurdly long paths must not be stored")
	}
}
