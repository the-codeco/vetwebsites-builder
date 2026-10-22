// Package fakehost is a stand-in for somebody's web hosting, for tests: an SFTP or FTPS server over a folder of its own, in the
// test's process. Nothing leaves the machine.
package fakehost

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	ftpserver "github.com/fclairamb/ftpserverlib"
	"github.com/pkg/sftp"
	"github.com/spf13/afero"
	"golang.org/x/crypto/ssh"
)

// Host is a fake hosting account.
type Host struct {
	Host     string
	Port     int
	User     string
	Password string
	Root     string // the account's home folder on disk
	// Fingerprint is the SFTP server's host key fingerprint.
	Fingerprint string
	// CAs trusts the FTPS server's certificate.
	CAs *x509.CertPool // trusts the FTPS server's certificate

	mu       sync.Mutex
	failNext map[string]int // file name suffix -> how many uploads to refuse
	uploads  []string
}

// Uploaded lists the files that were put in place (FTPS only), in order.
func (h *Host) Uploaded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.uploads...)
}

// Reset forgets the list of uploads.
func (h *Host) Reset() {
	h.mu.Lock()
	h.uploads = nil
	h.mu.Unlock()
}

// Addr is where to connect.
func (h *Host) Addr() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }

// Path is where a file of the site is on disk.
func (h *Host) Path(rel string) string {
	return filepath.Join(h.Root, "public_html", filepath.FromSlash(rel))
}

// Read returns a file of the site, failing the test if it is not there.
func (h *Host) Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(h.Path(rel))
	if err != nil {
		t.Fatalf("%s is not on the hosting: %v", rel, err)
	}
	return string(b)
}

// Exists says whether a file of the site is there.
func (h *Host) Exists(rel string) bool {
	_, err := os.Stat(h.Path(rel))
	return err == nil
}

// files lists every file under the hosting's public_html, as paths.
// Files lists every file of the site.
func (h *Host) Files(t *testing.T) []string {
	t.Helper()
	var out []string
	root := filepath.Join(h.Root, "public_html")
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func newHost(t *testing.T) *Host {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public_html"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Host{Host: "127.0.0.1", User: "acme", Password: "s3cret-pass", Root: root, failNext: map[string]int{}}
}

// --- SFTP ---

// StartSFTP starts a fake SFTP hosting account.
func StartSFTP(t *testing.T) *Host {
	t.Helper()
	h := newHost(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	h.Fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == h.User && string(pw) == h.Password {
			return nil, nil
		}
		return nil, io.ErrUnexpectedEOF
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.Port = ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						nc.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range creqs {
							ok := req.Type == "subsystem" && len(req.Payload) >= 8 && string(req.Payload[4:]) == "sftp"
							req.Reply(ok, nil)
							if ok {
								srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(h.Root))
								if err == nil {
									srv.Serve()
								}
								ch.Close()
								return
							}
						}
					}()
				}
			}()
		}
	}()
	return h
}

// --- FTPS ---

type ftpDriver struct {
	h   *Host
	cfg *tls.Config
	fs  afero.Fs
}

func (d *ftpDriver) GetSettings() (*ftpserver.Settings, error) {
	return &ftpserver.Settings{ListenAddr: "127.0.0.1:0", PublicHost: "127.0.0.1", TLSRequired: ftpserver.MandatoryEncryption}, nil
}
func (d *ftpDriver) ClientConnected(ftpserver.ClientContext) (string, error) {
	return "fake hosting", nil
}
func (d *ftpDriver) ClientDisconnected(ftpserver.ClientContext) {}
func (d *ftpDriver) GetTLSConfig() (*tls.Config, error)         { return d.cfg, nil }
func (d *ftpDriver) AuthUser(_ ftpserver.ClientContext, user, pass string) (ftpserver.ClientDriver, error) {
	if user != d.h.User || pass != d.h.Password {
		return nil, io.ErrUnexpectedEOF
	}
	return &recordingFs{Fs: d.fs, h: d.h}, nil
}

// recordingFs notes what is uploaded, and can refuse a chosen upload (an interrupted publish).
type recordingFs struct {
	afero.Fs
	h *Host
}

func (r *recordingFs) Rename(oldname, newname string) error {
	r.h.mu.Lock()
	r.h.uploads = append(r.h.uploads, filepath.ToSlash(newname))
	for suffix, n := range r.h.failNext {
		if n > 0 && len(newname) >= len(suffix) && newname[len(newname)-len(suffix):] == suffix {
			r.h.failNext[suffix]--
			r.h.mu.Unlock()
			return os.ErrPermission
		}
	}
	r.h.mu.Unlock()
	return r.Fs.Rename(oldname, newname)
}

// StartFTPS starts a fake FTPS hosting account (explicit TLS, with a certificate of its own).
func StartFTPS(t *testing.T) *Host {
	t.Helper()
	h := newHost(t)
	cert, pool := selfSigned(t)
	srv := ftpserver.NewFtpServer(&ftpDriver{h: h, cfg: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, fs: afero.NewBasePathFs(afero.NewOsFs(), h.Root)})
	srv.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(srv.Addr())
	h.Port, _ = strconv.Atoi(port)
	h.CAs = pool
	go srv.Serve()
	t.Cleanup(func() { srv.Stop() })
	return h
}

// selfSigned makes a certificate for 127.0.0.1 and a pool that trusts it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake hosting"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"hosting.test"}, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// FailNext makes the next n uploads of a file ending in suffix fail (FTPS only), to test an interrupted publish.
func (h *Host) FailNext(suffix string, n int) {
	h.mu.Lock()
	h.failNext[suffix] = n
	h.mu.Unlock()
}

// WriteForeign puts a file in the hosting's site folder that the website never uploaded (somebody else's file).
func (h *Host) WriteForeign(t *testing.T, rel, content string) {
	t.Helper()
	p := h.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
