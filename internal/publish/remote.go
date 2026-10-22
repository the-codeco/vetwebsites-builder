package publish

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
)

// Protocols a website can be published over.
const (
	SFTP = "sftp"
	FTPS = "ftps"
)

// Target is where a website is published: an account on somebody's web hosting.
type Target struct {
	Protocol string // SFTP or FTPS
	Host     string
	Port     int
	User     string
	Password string
	// Dir is the folder the site goes in, on the hosting (such as public_html, or public_html/shop).
	Dir string
	// HostKey is the fingerprint of the SFTP server's key as first seen; a later connection to a different key is refused.
	HostKey string
	// TLSName is the name the server's FTPS certificate must be valid for, when it is not the host name itself (shared hosting
	// often presents a certificate for the server's own name).
	TLSName string
}

// Remote is a folder on the hosting that files can be put in and taken out of. Paths are relative to the target's Dir.
type Remote interface {
	// MkdirAll makes a folder and the ones above it.
	MkdirAll(dir string) error
	// WriteFile puts a file in place. The new content is written under a temporary name and renamed over the old, so a visitor
	// never sees half a file.
	WriteFile(name string, data []byte) error
	// ReadFile reads a file; ok is false when it does not exist.
	ReadFile(name string, max int64) (data []byte, ok bool, err error)
	// Remove deletes a file; a file that is already gone is not an error.
	Remove(name string) error
	Close() error
}

// Errors people can act on.
var (
	ErrHostKeyChanged = errors.New("the hosting server's identity has changed since this site was first published. This can mean the hosting moved servers, or that someone is intercepting the connection. If you are sure it moved, trust the new identity in the hosting settings")
	ErrAuth           = errors.New("the hosting refused the username or password")
)

// HostKeyError carries the new fingerprint so the screen can offer to trust it.
type HostKeyError struct{ Got string }

func (e HostKeyError) Error() string { return ErrHostKeyChanged.Error() }
func (e HostKeyError) Unwrap() error { return ErrHostKeyChanged }

// ConnectOptions are for tests.
type ConnectOptions struct {
	Guard Guard
	// TLSConfig, when set, replaces the FTPS TLS settings (a test server uses a certificate of its own).
	TLSConfig *tls.Config
}

// Connect opens the target. It also returns the SFTP server's key fingerprint as seen on this connection ("" for FTPS), so
// the first one can be remembered.
func Connect(ctx context.Context, t Target, o ConnectOptions) (Remote, string, error) {
	if err := ValidateTarget(t); err != nil {
		return nil, "", err
	}
	switch t.Protocol {
	case SFTP:
		return connectSFTP(ctx, t, o)
	case FTPS:
		r, err := connectFTPS(ctx, t, o)
		return r, "", err
	}
	return nil, "", fmt.Errorf("unknown protocol %q", t.Protocol)
}

// ValidateTarget checks what a person typed for the hosting.
func ValidateTarget(t Target) error {
	switch {
	case t.Protocol != SFTP && t.Protocol != FTPS:
		return errors.New("choose SFTP or FTPS")
	case !validHost(t.Host):
		return errors.New("enter the hosting's address, such as ftp.example.com (letters, numbers, dots and dashes only)")
	case t.Port < 1 || t.Port > 65535:
		return errors.New("the port must be between 1 and 65535")
	case t.User == "" || len(t.User) > 200 || strings.ContainsAny(t.User, "\r\n\x00"):
		return errors.New("enter the hosting username")
	case len(t.Password) > 500 || strings.ContainsAny(t.Password, "\r\n\x00"):
		return errors.New("the password has characters that cannot be used")
	}
	if _, err := CleanRemoteDir(t.Dir); err != nil {
		return err
	}
	if t.TLSName != "" && !validHost(t.TLSName) {
		return errors.New("the certificate name must be a host name")
	}
	return nil
}

func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if _, _, err := net.SplitHostPort(h); err == nil {
		return false // the port has its own box
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == ':' && strings.Contains(h, ":") && strings.Count(h, ":") > 1 || r == '[' || r == ']') {
			return false
		}
	}
	return !strings.HasPrefix(h, "-") && !strings.HasPrefix(h, ".") && !strings.Contains(h, "..")
}

// CleanRemoteDir checks the folder on the hosting the site goes in. It may be relative (public_html) or absolute
// (/home/user/public_html); it may not climb out with "..", and it may not hold characters that mean something to a server.
func CleanRemoteDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("enter the folder on the hosting that the site goes in, usually public_html")
	}
	if len(dir) > 300 || strings.ContainsAny(dir, "\\\x00\r\n:*?\"<>|") {
		return "", errors.New("the folder has characters that cannot be used")
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == ".." {
			return "", errors.New("the folder cannot contain ..")
		}
	}
	clean := path.Clean(dir)
	if clean == "." {
		return "", errors.New("enter the folder on the hosting that the site goes in, usually public_html")
	}
	return clean, nil
}

// remotePath joins the target's folder and a file's path in the site, checking that the file stays inside the folder.
func remotePath(dir, name string) (string, error) {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || strings.ContainsAny(name, "\x00\r\n") {
		return "", fmt.Errorf("refusing the file name %q", name)
	}
	return path.Join(dir, name), nil
}
