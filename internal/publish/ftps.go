package publish

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
)

type ftpsRemote struct {
	c   *ftp.ServerConn
	dir string
}

func connectFTPS(ctx context.Context, t Target, o ConnectOptions) (Remote, error) {
	dir, err := CleanRemoteDir(t.Dir)
	if err != nil {
		return nil, err
	}
	tlsCfg := o.TLSConfig
	if tlsCfg == nil {
		name := t.TLSName
		if name == "" {
			name = t.Host
		}
		tlsCfg = &tls.Config{ServerName: name, MinVersion: tls.VersionTLS12}
	}
	// servers often insist that file transfers reuse the control connection's TLS session
	tlsCfg = tlsCfg.Clone()
	if tlsCfg.ClientSessionCache == nil {
		tlsCfg.ClientSessionCache = tls.NewLRUClientSessionCache(8)
	}
	// Every connection goes through the guard. The library only secures the control connection itself when it is given a
	// dial function of its own, so the connections for file transfers (every one after the first) are secured here.
	var mu sync.Mutex
	opened := 0
	dial := func(network, address string) (net.Conn, error) {
		conn, err := o.Guard.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		opened++
		control := opened == 1
		mu.Unlock()
		if control {
			return conn, nil
		}
		return tls.Client(conn, tlsCfg), nil
	}
	c, err := ftp.Dial(net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
		ftp.DialWithContext(ctx), ftp.DialWithDialFunc(dial), ftp.DialWithExplicitTLS(tlsCfg), ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		var cert *tls.CertificateVerificationError
		if errors.As(err, &cert) {
			return nil, fmt.Errorf("the hosting's security certificate is not valid for %q: %v. Hosting often uses a certificate in the server's own name: enter it as the certificate name in the hosting settings", tlsCfg.ServerName, cert.Err)
		}
		return nil, fmt.Errorf("could not connect over FTPS: %w", err)
	}
	if err := c.Login(t.User, t.Password); err != nil {
		c.Quit()
		var te *textproto.Error
		if errors.As(err, &te) && (te.Code == 530 || te.Code == 430) {
			return nil, ErrAuth
		}
		return nil, fmt.Errorf("could not log in over FTPS: %w", err)
	}
	return &ftpsRemote{c: c, dir: dir}, nil
}

func (r *ftpsRemote) path(name string) (string, error) { return remotePath(r.dir, name) }

// mkdirAll makes each folder along the way; a folder that exists is fine.
func (r *ftpsRemote) mkdirAll(full string) error {
	parts := strings.Split(full, "/")
	cur := ""
	if strings.HasPrefix(full, "/") {
		cur = "/"
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		cur = path.Join(cur, p)
		if err := r.c.MakeDir(cur); err != nil {
			var te *textproto.Error
			if errors.As(err, &te) && te.Code == 550 { // already there (or not allowed: the upload will say so)
				continue
			}
			return err
		}
	}
	return nil
}

func (r *ftpsRemote) MkdirAll(dir string) error {
	full := r.dir
	if dir != "" {
		var err error
		if full, err = r.path(dir); err != nil {
			return err
		}
	}
	return r.mkdirAll(full)
}

func (r *ftpsRemote) WriteFile(name string, data []byte) error {
	full, err := r.path(name)
	if err != nil {
		return err
	}
	if err := r.mkdirAll(path.Dir(full)); err != nil {
		return err
	}
	tmp := full + ".wb-tmp"
	if err := r.c.Stor(tmp, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := r.c.Rename(tmp, full); err != nil {
		// many FTP servers will not rename over an existing file
		r.c.Delete(full)
		if err := r.c.Rename(tmp, full); err != nil {
			r.c.Delete(tmp)
			return err
		}
	}
	return nil
}

func (r *ftpsRemote) ReadFile(name string, max int64) ([]byte, bool, error) {
	full, err := r.path(name)
	if err != nil {
		return nil, false, err
	}
	resp, err := r.c.Retr(full)
	if err != nil {
		var te *textproto.Error
		if errors.As(err, &te) && te.Code == 550 {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer resp.Close()
	b, err := io.ReadAll(io.LimitReader(resp, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > max {
		return nil, false, errors.New("the file on the hosting is larger than expected")
	}
	return b, true, nil
}

func (r *ftpsRemote) Remove(name string) error {
	full, err := r.path(name)
	if err != nil {
		return err
	}
	if err := r.c.Delete(full); err != nil {
		var te *textproto.Error
		if errors.As(err, &te) && te.Code == 550 {
			return nil
		}
		return err
	}
	return nil
}

func (r *ftpsRemote) Close() error { return r.c.Quit() }
