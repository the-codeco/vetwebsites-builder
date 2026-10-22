package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"time"
)

// Kinds of backup file kept on the server.
const (
	KindManual     = "manual"      // made on request, kept until deleted
	KindAuto       = "auto"        // made on a schedule, the oldest are removed
	KindPreRestore = "pre-restore" // made just before a restore, so it can be undone

	keepPreRestore = 3
	autoEvery      = 23 * time.Hour
	incomingDir    = "incoming"
	incomingMaxAge = time.Hour
)

var (
	nameRe    = regexp.MustCompile(`^(manual|auto|pre-restore)-(\d{8})-(\d{6})\.zip$`)
	stagedRe  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	ErrNoFile = errors.New("no such backup")
)

// File is a backup kept on the server.
type File struct {
	Name    string
	Kind    string
	Size    int64
	Created time.Time
}

// SizeLabel is the size for people.
func (f File) SizeLabel() string {
	switch {
	case f.Size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(f.Size)/(1<<30))
	case f.Size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(f.Size)/(1<<20))
	case f.Size >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(f.Size)/(1<<10))
	}
	return fmt.Sprintf("%d B", f.Size)
}

// ValidName says whether name is one of this program's backup file names (and so safe to open in the folder).
func ValidName(name string) bool { return nameRe.MatchString(name) }

func (s *Service) fileName(kind string) (string, error) {
	t := s.Now().UTC()
	for i := 0; i < 120; i++ { // two backups in the same second get the next free second
		name := fmt.Sprintf("%s-%s.zip", kind, t.Add(time.Duration(i)*time.Second).Format("20060102-150405"))
		if _, err := s.dir.Stat(name); errors.Is(err, os.ErrNotExist) {
			return name, nil
		}
	}
	return "", errors.New("could not find a free name for the backup")
}

// Save writes a backup into the server's backup folder. Nothing is left behind if it fails.
func (s *Service) Save(ctx context.Context, kind string, personal bool) (File, error) {
	name, err := s.fileName(kind)
	if err != nil {
		return File{}, err
	}
	tmp := ".tmp-" + randomHex()[:16]
	f, err := s.dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return File{}, err
	}
	_, err = s.Export(ctx, f, personal)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.dir.Rename(tmp, name)
	}
	if err != nil {
		s.dir.Remove(tmp)
		return File{}, err
	}
	return s.stat(name)
}

func (s *Service) stat(name string) (File, error) {
	m := nameRe.FindStringSubmatch(name)
	if m == nil {
		return File{}, ErrNoFile
	}
	st, err := s.dir.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, ErrNoFile
	}
	if err != nil {
		return File{}, err
	}
	created, err := time.Parse("20060102150405", m[2]+m[3])
	if err != nil {
		created = st.ModTime()
	}
	return File{Name: name, Kind: m[1], Size: st.Size(), Created: created}, nil
}

// List returns the saved backups, newest first.
func (s *Service) List() ([]File, error) {
	d, err := s.dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, n := range names {
		if f, err := s.stat(n); err == nil {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name }) // names sort by date within a kind
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Open opens a saved backup for reading.
func (s *Service) Open(name string) (*os.File, File, error) {
	info, err := s.stat(name)
	if err != nil {
		return nil, File{}, err
	}
	f, err := s.dir.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, File{}, ErrNoFile
	}
	return f, info, err
}

// Remove deletes a saved backup.
func (s *Service) Remove(name string) error {
	if _, err := s.stat(name); err != nil {
		return err
	}
	return s.dir.Remove(name)
}

// --- uploaded files waiting to be restored ---

// Stage keeps an uploaded backup on disk under a random token, so it can be shown to the owner and restored after
// they confirm. It reads at most MaxArchiveBytes.
func (s *Service) Stage(r io.Reader) (token string, size int64, err error) {
	if err := s.dir.MkdirAll(incomingDir, 0o700); err != nil {
		return "", 0, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", 0, err
	}
	token = hex.EncodeToString(b)
	f, err := s.dir.OpenFile(incomingDir+"/"+token+".zip", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	size, err = io.Copy(f, io.LimitReader(r, MaxArchiveBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && size > MaxArchiveBytes {
		err = invalid("This file is larger than a backup this site accepts (%d MB).", MaxArchiveBytes>>20)
	}
	if err != nil {
		s.dir.Remove(incomingDir + "/" + token + ".zip")
		return "", 0, err
	}
	return token, size, nil
}

// OpenStaged opens an uploaded backup by its token.
func (s *Service) OpenStaged(token string) (*os.File, int64, error) {
	if !stagedRe.MatchString(token) {
		return nil, 0, ErrNoFile
	}
	f, err := s.dir.Open(incomingDir + "/" + token + ".zip")
	if err != nil {
		return nil, 0, ErrNoFile
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Unstage deletes an uploaded backup.
func (s *Service) Unstage(token string) {
	if stagedRe.MatchString(token) {
		s.dir.Remove(incomingDir + "/" + token + ".zip")
	}
}

// --- housekeeping ---

// Housekeep makes the scheduled backup if one is due (keepAuto > 0), removes the oldest scheduled and pre-restore
// backups, and clears uploads nobody confirmed.
func (s *Service) Housekeep(ctx context.Context, keepAuto int, personal bool) error {
	files, err := s.List()
	if err != nil {
		return err
	}
	if keepAuto > 0 {
		due := true
		for _, f := range files {
			if f.Kind == KindAuto {
				due = s.Now().Sub(f.Created) >= autoEvery
				break // newest first
			}
		}
		if due {
			if _, err := s.Save(ctx, KindAuto, personal); err != nil {
				return fmt.Errorf("scheduled backup: %w", err)
			}
			if files, err = s.List(); err != nil {
				return err
			}
		}
	}
	seen := map[string]int{}
	for _, f := range files {
		seen[f.Kind]++
		switch {
		case f.Kind == KindAuto && keepAuto > 0 && seen[f.Kind] > keepAuto, // turning scheduled backups off keeps the ones already made
			f.Kind == KindPreRestore && seen[f.Kind] > keepPreRestore:
			s.dir.Remove(f.Name)
		}
	}
	s.clearIncoming()
	return nil
}

func (s *Service) clearIncoming() {
	d, err := s.dir.Open(incomingDir)
	if err != nil {
		return
	}
	defer d.Close()
	entries, _ := d.ReadDir(-1)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && s.Now().Sub(info.ModTime()) > incomingMaxAge {
			s.dir.Remove(incomingDir + "/" + e.Name())
		}
	}
}

// Run does the housekeeping once shortly after start and then every hour, until ctx ends.
func (s *Service) Run(ctx context.Context, keepAuto int, personal bool, logf func(string, ...any)) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.Housekeep(ctx, keepAuto, personal); err != nil && ctx.Err() == nil {
				logf("backup: %v", err)
			}
			timer.Reset(time.Hour)
		}
	}
}

// Temp makes a backup in a temporary file for downloading and returns it open, with a function that closes and
// deletes it. The download only starts once the whole backup has been made, so a failure is an error page, never
// a cut-off file.
func (s *Service) Temp(ctx context.Context, personal bool) (*os.File, func(), error) {
	tmp := ".tmp-" + randomHex()[:16]
	f, err := s.dir.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { f.Close(); s.dir.Remove(tmp) }
	if _, err := s.Export(ctx, f, personal); err != nil {
		cleanup()
		return nil, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, nil, err
	}
	return f, cleanup, nil
}

// Created is when a backup file was made, as the filename for a download.
func (s *Service) DownloadName(personal bool) string {
	suffix := ""
	if personal {
		suffix = "-with-subscribers"
	}
	return "webbuilder-backup-" + s.Now().UTC().Format("2006-01-02-1504") + suffix + ".zip"
}
