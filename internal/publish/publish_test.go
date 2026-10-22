package publish

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"webbuilder/internal/publish/fakehost"
)

// siteFiles writes a small site into a folder and lists it the way the exporter does.
func siteFiles(t *testing.T, content map[string]string) (string, []File) {
	t.Helper()
	dir := t.TempDir()
	var files []File
	for name, body := range content {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		files = append(files, File{Path: name, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return dir, files
}

func baseSite() map[string]string {
	return map[string]string{
		"index.html": "<h1>Home</h1>", "about.html": "<h1>About</h1>", "blog/post-1.html": "<p>One</p>", "site.css": "body{}",
		"media/2026/10/a.jpg": "JPEGDATA", ".htaccess": "Options -Indexes\n", "404.html": "<h1>Missing</h1>",
	}
}

type protoCase struct {
	name  string
	start func(*testing.T) *fakehost.Host
	opts  func(*fakehost.Host) ConnectOptions
}

var protocols = []protoCase{
	{SFTP, fakehost.StartSFTP, func(*fakehost.Host) ConnectOptions { return ConnectOptions{Guard: Guard{AllowPrivate: true}} }},
	{FTPS, fakehost.StartFTPS, func(h *fakehost.Host) ConnectOptions {
		return ConnectOptions{Guard: Guard{AllowPrivate: true}, TLSConfig: &tls.Config{RootCAs: h.CAs, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}}
	}},
}

func (p protoCase) connect(t *testing.T, h *fakehost.Host, mod ...func(*Target)) Remote {
	t.Helper()
	tg := targetOf(h, p.name)
	for _, m := range mod {
		m(&tg)
	}
	r, _, err := Connect(context.Background(), tg, p.opts(h))
	if err != nil {
		t.Fatalf("%s connect: %v", p.name, err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestPublishFirstTimeAndAgain(t *testing.T) {
	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			h := p.start(t)
			// somebody else's files are already on the hosting
			os.MkdirAll(h.Path("cgi-bin"), 0o755)
			os.WriteFile(h.Path("cgi-bin/script.cgi"), []byte("not ours"), 0o644)
			os.WriteFile(h.Path("wp-config.php"), []byte("secret"), 0o644)

			content := baseSite()
			dir, files := siteFiles(t, content)
			r := p.connect(t, h)
			var order []string
			rep, err := Run(context.Background(), r, dir, files, Options{Version: "b1"}, func(done, total int, msg string) {
				if strings.HasPrefix(msg, "Uploaded ") {
					order = append(order, strings.TrimPrefix(msg, "Uploaded "))
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Uploaded != len(content) || rep.Deleted != 0 || rep.Unchanged != 0 {
				t.Errorf("first publish: %+v", rep)
			}
			for name, body := range content {
				if got := h.Read(t, name); got != body {
					t.Errorf("%s on the hosting: %q", name, got)
				}
			}
			var m Manifest
			if err := json.Unmarshal([]byte(h.Read(t, ManifestName)), &m); err != nil || m.Version != "b1" || len(m.Files) != len(content) {
				t.Errorf("manifest: %v %+v", err, m)
			}
			// pictures and styles go first, the server settings and pages next, the home page last
			idx := func(n string) int {
				for i, o := range order {
					if o == n {
						return i
					}
				}
				return -1
			}
			if !(idx("media/2026/10/a.jpg") < idx(".htaccess") && idx(".htaccess") < idx("about.html") && idx("about.html") < idx("index.html") && idx("index.html") == len(order)-1) {
				t.Errorf("upload order: %v", order)
			}
			// no temporary files are left behind
			for _, f := range h.Files(t) {
				if strings.HasSuffix(f, ".wb-tmp") {
					t.Errorf("a temporary file was left: %s", f)
				}
			}

			// again with nothing changed: nothing is uploaded
			h.Reset()
			rep, err = Run(context.Background(), r, dir, files, Options{Version: "b2"}, nil)
			if err != nil || rep.Uploaded != 0 || rep.Unchanged != len(content) || rep.Deleted != 0 {
				t.Errorf("nothing changed: %+v %v", rep, err)
			}

			// one page changes, one is removed, one is added
			content["about.html"] = "<h1>About, updated</h1>"
			delete(content, "blog/post-1.html")
			content["contact.html"] = "<h1>Contact</h1>"
			dir2, files2 := siteFiles(t, content)
			rep, err = Run(context.Background(), r, dir2, files2, Options{Version: "b3"}, nil)
			if err != nil || rep.Uploaded != 2 || rep.Deleted != 1 || rep.Unchanged != len(content)-2 {
				t.Errorf("incremental publish: %+v %v", rep, err)
			}
			if h.Read(t, "about.html") != "<h1>About, updated</h1>" || !h.Exists("contact.html") || h.Exists("blog/post-1.html") {
				t.Error("the hosting follows the site")
			}
			// what was not ours is untouched
			if h.Read(t, "cgi-bin/script.cgi") != "not ours" || h.Read(t, "wp-config.php") != "secret" {
				t.Error("files this program did not upload are never touched")
			}

			// force uploads everything again
			rep, err = Run(context.Background(), r, dir2, files2, Options{Force: true, Version: "b4"}, nil)
			if err != nil || rep.Uploaded != len(content) {
				t.Errorf("force: %+v %v", rep, err)
			}
		})
	}
}

func TestPublishSurvivesAnInterruptionAndNeverLeavesHalfFiles(t *testing.T) {
	p := protocols[1] // FTPS: the fake can refuse a chosen upload
	h := p.start(t)
	content := baseSite()
	dir, files := siteFiles(t, content)
	r := p.connect(t, h)
	h.FailNext("about.html", 2) // the first try and the fallback
	if _, err := Run(context.Background(), r, dir, files, Options{Version: "b1"}, nil); err == nil || !strings.Contains(err.Error(), "about.html") {
		t.Fatalf("the interrupted publish says which file failed: %v", err)
	}
	if h.Exists(ManifestName) {
		t.Error("the list of published files is only written when everything is in place")
	}
	if h.Exists("index.html") {
		t.Error("the home page goes last, so it is not there yet")
	}
	// run it again: it finishes
	r2 := p.connect(t, h)
	rep, err := Run(context.Background(), r2, dir, files, Options{Version: "b1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Uploaded == 0 || !h.Exists(ManifestName) {
		t.Errorf("second try: %+v", rep)
	}
	for name, body := range content {
		if h.Read(t, name) != body {
			t.Errorf("%s", name)
		}
	}
}

func TestPublishOnlyRemovesWhatItUploaded(t *testing.T) {
	p := protocols[0]
	h := p.start(t)
	dir, files := siteFiles(t, map[string]string{"index.html": "x", "keep.html": "k"})
	r := p.connect(t, h)
	// a hosting with a damaged or hostile list: names that climb out, absolute paths, the list itself
	evil := Manifest{Version: "evil", Files: map[string]string{"../outside.txt": "x", "/etc/passwd": "x", "a/../../b": "x", ManifestName: "x", "old.html": "x", "bad\\name": "x"}}
	data, _ := json.Marshal(evil)
	os.WriteFile(h.Path(ManifestName), data, 0o644)
	os.WriteFile(h.Path("old.html"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(h.Root, "outside.txt"), []byte("outside"), 0o644)
	if _, err := Run(context.Background(), r, dir, files, Options{Version: "b1"}, nil); err != nil {
		t.Fatal(err)
	}
	if h.Exists("old.html") {
		t.Error("a valid name from the hosting's list that is no longer in the site is removed")
	}
	if b, err := os.ReadFile(filepath.Join(h.Root, "outside.txt")); err != nil || string(b) != "outside" {
		t.Error("a name that climbs out of the folder is never acted on")
	}
	// a damaged list means the hosting is treated as empty: nothing is deleted
	os.WriteFile(h.Path(ManifestName), []byte("{not json"), 0o644)
	os.WriteFile(h.Path("stranger.html"), []byte("s"), 0o644)
	if _, err := Run(context.Background(), r, dir, files, Options{Version: "b2"}, nil); err != nil {
		t.Fatal(err)
	}
	if !h.Exists("stranger.html") {
		t.Error("with no usable list, nothing is deleted")
	}
}

func TestPublishChecksWhatItUploads(t *testing.T) {
	p := protocols[0]
	h := p.start(t)
	dir, files := siteFiles(t, map[string]string{"index.html": "good"})
	r := p.connect(t, h)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("changed after the export"), 0o644)
	if _, err := Run(context.Background(), r, dir, files, Options{}, nil); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Errorf("a file that is not what the export said: %v", err)
	}
	if h.Exists("index.html") {
		t.Error("nothing was uploaded")
	}
	for _, bad := range []string{"../evil.html", "/abs.html", "a/../../b.html", "a\\b.html", "", ".", "x/./y", "a//b", ManifestName, "con:fuse", "a\x00b"} {
		if _, err := Run(context.Background(), r, dir, []File{{Path: bad, SHA256: "0"}}, Options{}, nil); err == nil {
			t.Errorf("the file name %q should be refused", bad)
		}
	}
	if _, err := Run(context.Background(), r, dir, []File{{Path: "missing.html", SHA256: "0"}}, Options{}, nil); err == nil {
		t.Error("a file that is not in the export folder")
	}
}

func TestConnectionProblemsAreReportedClearly(t *testing.T) {
	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			h := p.start(t)
			_, _, err := Connect(context.Background(), targetOf(h, p.name), p.opts(h))
			if err != nil {
				t.Fatal(err)
			}
			bad := targetOf(h, p.name)
			bad.Password = "wrong"
			if _, _, err := Connect(context.Background(), bad, p.opts(h)); !errors.Is(err, ErrAuth) {
				t.Errorf("a wrong password: %v", err)
			}
			bad = targetOf(h, p.name)
			bad.User = "nobody"
			if _, _, err := Connect(context.Background(), bad, p.opts(h)); !errors.Is(err, ErrAuth) {
				t.Errorf("a wrong user: %v", err)
			}
			bad = targetOf(h, p.name)
			bad.Port = 1
			if _, _, err := Connect(context.Background(), bad, p.opts(h)); err == nil {
				t.Error("nothing listens there")
			}
		})
	}
	// FTPS: a certificate for another name is explained
	h := fakehost.StartFTPS(t)
	_, _, err := Connect(context.Background(), targetOf(h, FTPS), ConnectOptions{Guard: Guard{AllowPrivate: true}, TLSConfig: &tls.Config{RootCAs: h.CAs, ServerName: "another.example", MinVersion: tls.VersionTLS12}})
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("a certificate for another name: %v", err)
	}
	// and one nobody vouches for is refused (the default settings verify)
	_, _, err = Connect(context.Background(), targetOf(h, FTPS), ConnectOptions{Guard: Guard{AllowPrivate: true}})
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("an untrusted certificate: %v", err)
	}
}

func TestSFTPHostKeyIsPinned(t *testing.T) {
	h := fakehost.StartSFTP(t)
	o := ConnectOptions{Guard: Guard{AllowPrivate: true}}
	tg := targetOf(h, SFTP)
	r, seen, err := Connect(context.Background(), tg, o)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if seen != h.Fingerprint || !strings.HasPrefix(seen, "SHA256:") {
		t.Fatalf("the first connection reports the key: %q vs %q", seen, h.Fingerprint)
	}
	tg.HostKey = seen
	if r, _, err := Connect(context.Background(), tg, o); err != nil {
		t.Errorf("the same key: %v", err)
	} else {
		r.Close()
	}
	tg.HostKey = "SHA256:someoneElsesKey"
	_, got, err := Connect(context.Background(), tg, o)
	var hk HostKeyError
	if !errors.Is(err, ErrHostKeyChanged) || !errors.As(err, &hk) || hk.Got != h.Fingerprint || got != h.Fingerprint {
		t.Errorf("a different key is refused, and the new one is reported: %v", err)
	}
}

func TestValidateTargetAndFolders(t *testing.T) {
	ok := Target{Protocol: SFTP, Host: "ftp.example.com", Port: 22, User: "acme", Password: "x", Dir: "public_html"}
	if err := ValidateTarget(ok); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*Target){
		"no protocol": func(t *Target) { t.Protocol = "" }, "http": func(t *Target) { t.Protocol = "http" }, "ftp": func(t *Target) { t.Protocol = "ftp" },
		"no host": func(t *Target) { t.Host = "" }, "host with port": func(t *Target) { t.Host = "example.com:21" }, "host with slash": func(t *Target) { t.Host = "example.com/x" },
		"host with space": func(t *Target) { t.Host = "ex ample.com" }, "host with newline": func(t *Target) { t.Host = "a.com\nb" }, "dot dot": func(t *Target) { t.Host = "a..com" }, "leading dash": func(t *Target) { t.Host = "-a.com" },
		"url": func(t *Target) { t.Host = "ftp://example.com" }, "port 0": func(t *Target) { t.Port = 0 }, "port big": func(t *Target) { t.Port = 70000 },
		"no user": func(t *Target) { t.User = "" }, "user newline": func(t *Target) { t.User = "a\nb" }, "password newline": func(t *Target) { t.Password = "a\r\nb" },
		"no folder": func(t *Target) { t.Dir = "" }, "dot folder": func(t *Target) { t.Dir = "." }, "climbing folder": func(t *Target) { t.Dir = "../x" }, "climbing deeper": func(t *Target) { t.Dir = "a/../../x" },
		"backslash": func(t *Target) { t.Dir = `a\b` }, "bad tls name": func(t *Target) { t.TLSName = "a b" },
	} {
		tg := ok
		mod(&tg)
		if err := ValidateTarget(tg); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
	for in, want := range map[string]string{"public_html": "public_html", " public_html/ ": "public_html", "/home/acme/public_html": "/home/acme/public_html", "public_html//shop/": "public_html/shop", "./public_html": "public_html"} {
		if got, err := CleanRemoteDir(in); err != nil || got != want {
			t.Errorf("CleanRemoteDir(%q) = %q, %v", in, got, err)
		}
	}
	if (Target{Protocol: FTPS, Host: "2001:db8::1", Port: 21, User: "a", Dir: "x"}).Host != "" && ValidateTarget(Target{Protocol: FTPS, Host: "2001:db8::1", Port: 21, User: "a", Dir: "x"}) != nil {
		t.Error("an IPv6 address is a host")
	}
}

func TestTheServerRefusesItsOwnNetwork(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.1.2.3", "10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1", "255.255.255.255",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "ff02::1", "2001:db8::1", "64:ff9b::7f00:1",
	} {
		if !Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "93.184.216.34", "1.1.1.1", "172.32.0.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s is an ordinary public address", s)
		}
	}
	g := Guard{}
	for _, host := range []string{"127.0.0.1", "localhost", "10.1.2.3", "169.254.169.254", "[::1]"} {
		addr := host + ":22"
		if host == "[::1]" {
			addr = "[::1]:22"
		}
		if c, err := g.DialContext(context.Background(), "tcp", addr); err == nil {
			c.Close()
			t.Errorf("a connection to %s was allowed", host)
		}
	}
	if _, err := g.DialContext(context.Background(), "tcp", "127.0.0.1:0"); err == nil {
		t.Error("port 0")
	}
	if _, err := g.DialContext(context.Background(), "tcp", "example.com"); err == nil {
		t.Error("no port")
	}
	// and through the real clients: a hosting on the server's own network is refused before any connection
	h := fakehost.StartSFTP(t)
	if _, _, err := Connect(context.Background(), targetOf(h, SFTP), ConnectOptions{}); !errors.Is(err, ErrBlocked) {
		t.Errorf("SFTP to a local address: %v", err)
	}
	f := fakehost.StartFTPS(t)
	if _, _, err := Connect(context.Background(), targetOf(f, FTPS), ConnectOptions{TLSConfig: &tls.Config{RootCAs: f.CAs, ServerName: "127.0.0.1"}}); !errors.Is(err, ErrBlocked) {
		t.Errorf("FTPS to a local address: %v", err)
	}
}

func TestSecretsAreEncryptedAndBoundToWhereTheyBelong(t *testing.T) {
	key := strings.Repeat("ab", 32)
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("hunter2", "site:1:password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "hunter2") {
		t.Error("the secret is not in the ciphertext")
	}
	if got, err := c.Decrypt(enc, "site:1:password"); err != nil || got != "hunter2" {
		t.Errorf("round trip: %q %v", got, err)
	}
	if _, err := c.Decrypt(enc, "site:2:password"); err == nil {
		t.Error("moved to another website's row")
	}
	enc2, _ := c.Encrypt("hunter2", "site:1:password")
	if string(enc) == string(enc2) {
		t.Error("each encryption uses a fresh nonce")
	}
	tampered := append([]byte(nil), enc...)
	tampered[len(tampered)-1] ^= 1
	if _, err := c.Decrypt(tampered, "site:1:password"); err == nil {
		t.Error("a changed ciphertext is refused")
	}
	if _, err := c.Decrypt(enc[:5], "x"); err == nil {
		t.Error("a short one")
	}
	other, _ := NewCipher(strings.Repeat("cd", 32))
	if _, err := other.Decrypt(enc, "site:1:password"); err == nil {
		t.Error("a different key")
	}
	for _, bad := range []string{"", "short", strings.Repeat("zz", 32), strings.Repeat("ab", 31), strings.Repeat("ab", 33)} {
		if _, err := NewCipher(bad); err == nil {
			t.Errorf("key %q should be refused", bad)
		}
	}
	if _, err := NewCipher(""); !errors.Is(err, ErrNoKey) {
		t.Error("no key")
	}
	empty, _ := c.Encrypt("", "ctx")
	if got, err := c.Decrypt(empty, "ctx"); err != nil || got != "" {
		t.Errorf("an empty secret: %q %v", got, err)
	}
}

// targetOf is where to publish to a fake hosting account, over a protocol.
func targetOf(h *fakehost.Host, proto string) Target {
	return Target{Protocol: proto, Host: h.Host, Port: h.Port, User: h.User, Password: h.Password, Dir: "public_html"}
}
