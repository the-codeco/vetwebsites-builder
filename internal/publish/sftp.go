package publish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type sftpRemote struct {
	client *sftp.Client
	conn   *ssh.Client
	dir    string
}

// connectSFTP opens an SFTP connection. The server's key is remembered the first time (the fingerprint is returned for
// the caller to store) and every later connection must present the same key.
func connectSFTP(ctx context.Context, t Target, o ConnectOptions) (Remote, string, error) {
	dir, err := CleanRemoteDir(t.Dir)
	if err != nil {
		return nil, "", err
	}
	var seen string
	cfg := &ssh.ClientConfig{
		User: t.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(t.Password),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = t.Password
				}
				return ans, nil
			}),
		},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = ssh.FingerprintSHA256(key)
			if t.HostKey != "" && t.HostKey != seen {
				return HostKeyError{Got: seen}
			}
			return nil
		},
		Timeout: 20 * time.Second,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	raw, err := o.Guard.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, "", err
	}
	// the connection is closed if the handshake does not finish in time
	raw.SetDeadline(time.Now().Add(30 * time.Second))
	sc, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		raw.Close()
		var hk HostKeyError
		if errors.As(err, &hk) {
			return nil, seen, hk
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, seen, ErrAuth
		}
		return nil, seen, fmt.Errorf("could not connect over SFTP: %w", err)
	}
	raw.SetDeadline(time.Time{})
	conn := ssh.NewClient(sc, chans, reqs)
	client, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, seen, fmt.Errorf("the hosting did not start an SFTP session (is SFTP enabled for this account?): %w", err)
	}
	return &sftpRemote{client: client, conn: conn, dir: dir}, seen, nil
}

func (r *sftpRemote) path(name string) (string, error) { return remotePath(r.dir, name) }

func (r *sftpRemote) MkdirAll(dir string) error {
	full := r.dir
	if dir != "" {
		var err error
		if full, err = r.path(dir); err != nil {
			return err
		}
	}
	return r.client.MkdirAll(full)
}

func (r *sftpRemote) WriteFile(name string, data []byte) error {
	full, err := r.path(name)
	if err != nil {
		return err
	}
	if err := r.client.MkdirAll(path.Dir(full)); err != nil {
		return err
	}
	tmp := full + ".wb-tmp"
	f, err := r.client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		r.client.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		r.client.Remove(tmp)
		return err
	}
	// a rename over an existing file is atomic where the server allows it; otherwise the old file is removed first
	if err := r.client.PosixRename(tmp, full); err != nil {
		r.client.Remove(full)
		if err := r.client.Rename(tmp, full); err != nil {
			r.client.Remove(tmp)
			return err
		}
	}
	return nil
}

func (r *sftpRemote) ReadFile(name string, max int64) ([]byte, bool, error) {
	full, err := r.path(name)
	if err != nil {
		return nil, false, err
	}
	f, err := r.client.Open(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > max {
		return nil, false, errors.New("the file on the hosting is larger than expected")
	}
	return b, true, nil
}

func (r *sftpRemote) Remove(name string) error {
	full, err := r.path(name)
	if err != nil {
		return err
	}
	if err := r.client.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (r *sftpRemote) Close() error {
	r.client.Close()
	return r.conn.Close()
}
