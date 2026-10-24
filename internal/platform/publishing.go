package platform

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"webbuilder/internal/admin"
	"webbuilder/internal/export"
	"webbuilder/internal/publish"
)

const (
	publishWorkers = 3
	jobHistory     = 10
)

// publisher runs publishes in the background: a few at a time, never two for the same website.
type publisher struct {
	queue  chan int64
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func (p *Platform) startPublisher() {
	ctx, cancel := context.WithCancel(context.Background())
	p.pub = &publisher{queue: make(chan int64, 256), cancel: cancel}
	// a publish that was running when the server stopped did not finish: say so, and queue again those that had not started
	p.pool.Exec(ctx, `UPDATE publish_jobs SET status = 'failed', error = 'This publish was interrupted when the server restarted. Publish again.', finished_at = now() WHERE status = 'running'`)
	if rows, err := p.pool.Query(ctx, `SELECT id FROM publish_jobs WHERE status = 'queued' ORDER BY id`); err == nil {
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			p.pub.queue <- id
		}
	}
	for i := 0; i < publishWorkers; i++ {
		p.pub.wg.Add(1)
		go func() {
			defer p.pub.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-p.pub.queue:
					p.runJob(ctx, id)
				}
			}
		}()
	}
}

func (p *Platform) stopPublisher() {
	if p.pub != nil {
		p.pub.cancel()
		p.pub.wg.Wait()
	}
}

// publisherFor is the publish screens of one website.
func (p *Platform) publisherFor(siteID int64) admin.PublishService { return &sitePublisher{p, siteID} }

type sitePublisher struct {
	p  *Platform
	id int64
}

// --- target (hosting settings) ---

type storedTarget struct {
	publish.Target
	hasPassword bool
	passwordEnc []byte
	AutoPublish bool
}

func (p *Platform) loadTarget(ctx context.Context, siteID int64) (storedTarget, bool, error) {
	var t storedTarget
	err := p.pool.QueryRow(ctx, `SELECT protocol, host, port, username, password_enc, remote_dir, host_key, tls_name, auto_publish FROM publish_targets WHERE site_id = $1`, siteID).
		Scan(&t.Protocol, &t.Host, &t.Port, &t.User, &t.passwordEnc, &t.Dir, &t.HostKey, &t.TLSName, &t.AutoPublish)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedTarget{}, false, nil
	}
	t.hasPassword = len(t.passwordEnc) > 0
	return t, err == nil, err
}

func (p *Platform) secretContext(siteID int64) string {
	return fmt.Sprintf("site:%d:hosting-password", siteID)
}

// target returns the saved hosting settings with the password decrypted.
func (p *Platform) target(ctx context.Context, siteID int64) (publish.Target, bool, error) {
	st, ok, err := p.loadTarget(ctx, siteID)
	if err != nil || !ok {
		return publish.Target{}, ok, err
	}
	if p.cipher == nil {
		return publish.Target{}, true, publish.ErrNoKey
	}
	pw, err := p.cipher.Decrypt(st.passwordEnc, p.secretContext(siteID))
	if err != nil {
		return publish.Target{}, true, err
	}
	st.Password = pw
	return st.Target, true, nil
}

var urlPathRe = regexp.MustCompile(`^(/[A-Za-z0-9._~%\-]+)*$`)

// cleanPublicURL checks the address the website is published at: https://www.example.com, or one with a folder.
func cleanPublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	bad := errors.New("enter your website's address as it will be seen by visitors, such as https://www.example.com (or https://example.com/folder if it goes in a folder)")
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 300 {
		return "", bad
	}
	host := u.Hostname()
	if host == "" || !regexp.MustCompile(`^[A-Za-z0-9.\-]+$`).MatchString(host) || strings.Contains(host, "..") || strings.HasPrefix(host, "-") || strings.HasPrefix(host, ".") {
		return "", bad
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" && !regexp.MustCompile(`^[0-9]{1,5}$`).MatchString(p) {
		return "", bad
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if path != "" && (!urlPathRe.MatchString(path) || strings.Contains(path, "..")) {
		return "", bad
	}
	return u.Scheme + "://" + strings.ToLower(u.Host) + path, nil
}

func (s *sitePublisher) SaveHosting(ctx context.Context, actor string, in admin.HostingInput) error {
	p := s.p
	site, err := p.siteByID(ctx, s.id)
	if err != nil {
		return err
	}
	if p.cipher == nil {
		return admin.UserError("Publishing is not switched on for this server yet. Ask the administrator to set the server's secret key.")
	}
	if in.Port == 0 {
		in.Port = 21
		if in.Protocol == publish.SFTP {
			in.Port = 22
		}
	}
	existing, have, err := p.loadTarget(ctx, s.id)
	if err != nil {
		return err
	}
	tg := publish.Target{Protocol: in.Protocol, Host: in.Host, Port: in.Port, User: in.User, Password: "kept", Dir: in.Dir, TLSName: in.TLSName}
	if err := publish.ValidateTarget(tg); err != nil {
		return admin.UserError(capitalise(err.Error()) + ".")
	}
	dir, _ := publish.CleanRemoteDir(in.Dir)
	public, err := cleanPublicURL(in.PublicURL)
	if err != nil {
		return admin.UserError(capitalise(err.Error()) + ".")
	}
	enc := existing.passwordEnc
	switch {
	case in.Password != "":
		if len(in.Password) > 500 || strings.ContainsAny(in.Password, "\r\n\x00") {
			return admin.UserError("The password has characters that cannot be used.")
		}
		if enc, err = p.cipher.Encrypt(in.Password, p.secretContext(s.id)); err != nil {
			return err
		}
	case !have || len(enc) == 0:
		return admin.UserError("Enter the password for the hosting account.")
	}
	hostKey := existing.HostKey
	if !have || existing.Host != in.Host || existing.Port != in.Port || existing.Protocol != in.Protocol {
		hostKey = "" // a different server: its identity is learnt again
	}
	if _, err := p.pool.Exec(ctx, `
		INSERT INTO publish_targets (site_id, protocol, host, port, username, password_enc, remote_dir, host_key, tls_name, auto_publish, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (site_id) DO UPDATE SET protocol = EXCLUDED.protocol, host = EXCLUDED.host, port = EXCLUDED.port, username = EXCLUDED.username,
			password_enc = EXCLUDED.password_enc, remote_dir = EXCLUDED.remote_dir, host_key = EXCLUDED.host_key, tls_name = EXCLUDED.tls_name, auto_publish = EXCLUDED.auto_publish, updated_at = now()`,
		s.id, in.Protocol, in.Host, in.Port, in.User, enc, dir, hostKey, in.TLSName, in.AutoPublish); err != nil {
		return err
	}
	if _, err := p.pool.Exec(ctx, `UPDATE sites SET public_url = $2 WHERE id = $1`, s.id, public); err != nil {
		return err
	}
	p.audit(ctx, actor, &site, "saved the hosting settings", in.Protocol+" "+in.Host)
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (s *sitePublisher) form(ctx context.Context) (admin.HostingForm, error) {
	site, err := s.p.siteByID(ctx, s.id)
	if err != nil {
		return admin.HostingForm{}, err
	}
	f := admin.HostingForm{PublicURL: site.PublicURL}
	t, ok, err := s.p.loadTarget(ctx, s.id)
	if err != nil {
		return f, err
	}
	if ok {
		f.Protocol, f.Host, f.Port, f.User, f.Dir, f.TLSName, f.HostKey, f.HasPassword, f.AutoPublish = t.Protocol, t.Host, t.Port, t.User, t.Dir, t.TLSName, t.HostKey, t.hasPassword, t.AutoPublish
	}
	return f, nil
}

func (p *Platform) connectOptions() publish.ConnectOptions {
	return publish.ConnectOptions{Guard: publish.Guard{AllowPrivate: p.cfg.AllowPrivateHosts}, TLSConfig: p.tlsConfig}
}

// friendly turns a connection error into words for the customer, never including the password.
func friendly(err error, secret string) string {
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "***")
	}
	switch {
	case errors.Is(err, publish.ErrAuth):
		return "The hosting refused the username or password. Check them in cPanel (FTP Accounts) and save them again."
	case errors.Is(err, publish.ErrBlocked):
		return "That address is not allowed. The hosting must be a public server on the internet."
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "The hosting did not answer in time. Check the server address and port."
	}
	return capitalise(msg)
}

func (s *sitePublisher) TestHosting(ctx context.Context, actor string) (admin.TestResult, error) {
	p := s.p
	tg, ok, err := p.target(ctx, s.id)
	if !ok {
		return admin.TestResult{}, admin.UserError("Save the hosting settings first.")
	}
	if err != nil {
		return admin.TestResult{OK: false, Message: friendly(err, "")}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	remote, seen, err := publish.Connect(ctx, tg, p.connectOptions())
	var hk publish.HostKeyError
	if errors.As(err, &hk) {
		return admin.TestResult{OK: false, Message: publish.ErrHostKeyChanged.Error() + ".", HostKey: tg.HostKey, KeyChanged: true, NewKey: hk.Got}, nil
	}
	if err != nil {
		return admin.TestResult{OK: false, Message: friendly(err, tg.Password)}, nil
	}
	defer remote.Close()
	if err := remote.MkdirAll(""); err != nil {
		return admin.TestResult{OK: false, Message: "Connected, but the folder " + tg.Dir + " could not be opened: " + friendly(err, tg.Password)}, nil
	}
	if err := remote.WriteFile(".wb-probe", []byte("ok")); err != nil {
		return admin.TestResult{OK: false, Message: "Connected, but a file could not be put in " + tg.Dir + ": " + friendly(err, tg.Password)}, nil
	}
	remote.Remove(".wb-probe")
	res := admin.TestResult{OK: true, Message: "Connected, and files can be put in " + tg.Dir + "."}
	if tg.Protocol == publish.SFTP && seen != "" {
		if tg.HostKey == "" {
			if _, err := p.pool.Exec(ctx, `UPDATE publish_targets SET host_key = $2 WHERE site_id = $1`, s.id, seen); err != nil {
				return res, err
			}
			res.Message += " The server's identity was remembered."
		}
		res.HostKey = seen
	}
	return res, nil
}

func (s *sitePublisher) TrustHostKey(ctx context.Context, actor string) error {
	p := s.p
	tg, ok, err := p.target(ctx, s.id)
	if !ok || err != nil {
		return admin.UserError("Save the hosting settings first.")
	}
	if tg.Protocol != publish.SFTP {
		return admin.UserError("Only SFTP servers have an identity to trust.")
	}
	tg.HostKey = ""
	remote, seen, err := publish.Connect(ctx, tg, p.connectOptions())
	if remote != nil {
		remote.Close()
	}
	if err != nil && seen == "" {
		return admin.UserError(friendly(err, tg.Password))
	}
	if seen == "" {
		return admin.UserError("The server's identity could not be read.")
	}
	if _, err := p.pool.Exec(ctx, `UPDATE publish_targets SET host_key = $2 WHERE site_id = $1`, s.id, seen); err != nil {
		return err
	}
	if site, err := p.siteByID(ctx, s.id); err == nil {
		p.audit(ctx, actor, &site, "trusted the hosting server's new identity", seen)
	}
	return nil
}

// --- the bar and the screen ---

func (p *Platform) activeJob(ctx context.Context, siteID int64) (*admin.JobRow, error) {
	j, err := p.queryJob(ctx, `WHERE site_id = $1 AND status IN ('queued', 'running')`, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &j, err
}

const jobCols = `id, status, started_by, force, version, message, error, warnings, created_at, started_at, finished_at, files_total, files_done, uploaded, deleted, unchanged, bytes, restore_version, COALESCE(restore_of, 0)`

func scanJob(row pgx.Row) (admin.JobRow, error) {
	var j admin.JobRow
	var warnings string
	err := row.Scan(&j.ID, &j.Status, &j.By, &j.Force, &j.Version, &j.Message, &j.Error, &warnings, &j.Created, &j.Started, &j.Finished,
		&j.FilesTotal, &j.FilesDone, &j.Uploaded, &j.Deleted, &j.Unchanged, &j.Bytes, &j.RestoreVersion, &j.RestoreOf)
	if warnings != "" {
		j.Warnings = strings.Split(warnings, "\n")
	}
	return j, err
}

func (p *Platform) queryJob(ctx context.Context, where string, args ...any) (admin.JobRow, error) {
	return scanJob(p.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM publish_jobs `+where+` ORDER BY id DESC LIMIT 1`, args...))
}

func (s *sitePublisher) Job(ctx context.Context, id int64) (admin.JobRow, error) {
	j, err := s.p.queryJob(ctx, `WHERE id = $1 AND site_id = $2`, id, s.id)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, admin.UserError("That publish does not exist.")
	}
	return j, err
}

func (s *sitePublisher) Bar(ctx context.Context) (admin.PublishBar, error) {
	p := s.p
	site, err := p.siteByID(ctx, s.id)
	if err != nil {
		return admin.PublishBar{}, err
	}
	n, err := p.unpublished(ctx, site)
	if err != nil {
		return admin.PublishBar{}, err
	}
	_, configured, err := p.loadTarget(ctx, s.id)
	if err != nil {
		return admin.PublishBar{}, err
	}
	active, err := p.activeJob(ctx, s.id)
	if err != nil {
		return admin.PublishBar{}, err
	}
	return admin.PublishBar{Configured: configured && site.PublicURL != "", Unpublished: n, Publishing: active != nil, LastPublished: site.PublishedAt, NeverBuilt: site.BuildVersion == ""}, nil
}

func (s *sitePublisher) State(ctx context.Context) (admin.PublishState, error) {
	p := s.p
	site, err := p.siteByID(ctx, s.id)
	if err != nil {
		return admin.PublishState{}, err
	}
	f, err := s.form(ctx)
	if err != nil {
		return admin.PublishState{}, err
	}
	n, err := p.unpublished(ctx, site)
	if err != nil {
		return admin.PublishState{}, err
	}
	st := admin.PublishState{Hosting: f, Configured: f.Host != "" && site.PublicURL != "", Unpublished: n, LastBuild: site.BuiltAt}
	if st.Active, err = p.activeJob(ctx, s.id); err != nil {
		return st, err
	}
	rows, err := p.pool.Query(ctx, `SELECT `+jobCols+` FROM publish_jobs WHERE site_id = $1 ORDER BY id DESC LIMIT $2`, s.id, jobHistory)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return st, err
		}
		st.Jobs = append(st.Jobs, j)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	p.markRestorable(site, st.Jobs, st.Active != nil)
	return st, nil
}

// markRestorable says, for each publish in the history, whether its copy can be put back, and what a rollback came from.
func (p *Platform) markRestorable(s Site, jobs []admin.JobRow, busy bool) {
	done := map[int64]time.Time{}
	for _, j := range jobs {
		if j.Finished != nil {
			done[j.ID] = *j.Finished
		}
	}
	for i := range jobs {
		j := &jobs[i]
		j.Rollback = j.RestoreVersion != ""
		if j.Rollback {
			if t, ok := done[j.RestoreOf]; ok {
				j.RestoreOfDate = &t
			}
		}
		if j.Status != "done" || !validVersion(j.Version) {
			continue
		}
		j.Current = j.Version == s.PublishedVersion
		if j.Current || busy {
			continue
		}
		_, _, _, err := p.copyOf(s, j.Version)
		j.CanRestore = err == nil
	}
}

// --- starting a publish ---

func (s *sitePublisher) Start(ctx context.Context, actor string, force bool) (int64, error) {
	p := s.p
	site, err := p.siteByID(ctx, s.id)
	if err != nil {
		return 0, err
	}
	switch {
	case !site.Active:
		return 0, admin.UserError("This website is suspended.")
	case p.cipher == nil:
		return 0, admin.UserError("Publishing is not switched on for this server yet. Ask the administrator to set the server's secret key.")
	}
	if _, ok, err := p.loadTarget(ctx, s.id); err != nil {
		return 0, err
	} else if !ok || site.PublicURL == "" {
		return 0, admin.UserError("Set up your hosting first (the connection details and your website's address).")
	}
	var id int64
	err = p.pool.QueryRow(ctx, `INSERT INTO publish_jobs (site_id, status, started_by, force) VALUES ($1, 'queued', $2, $3) RETURNING id`, s.id, actor, force).Scan(&id)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" { // already queued or running: that one will publish everything
		if j, err := p.activeJob(ctx, s.id); err == nil && j != nil {
			return j.ID, nil
		}
		return 0, admin.UserError("A publish is already running.")
	}
	if err != nil {
		return 0, err
	}
	select {
	case p.pub.queue <- id:
	default:
		p.pool.Exec(ctx, `UPDATE publish_jobs SET status = 'failed', error = 'The server is busy. Try again in a few minutes.', finished_at = now() WHERE id = $1`, id)
		return 0, admin.UserError("The server is busy publishing other websites. Try again in a few minutes.")
	}
	p.audit(ctx, actor, &site, "started a publish", "")
	return id, nil
}

// Rollback puts an earlier publish's copy of the website back on the hosting. Every file is sent again (not just
// those that differ from the hosting's list), because a publish that failed half way leaves the hosting in a state its
// list does not describe.
func (s *sitePublisher) Rollback(ctx context.Context, actor string, jobID int64) (int64, error) {
	p := s.p
	site, err := p.siteByID(ctx, s.id)
	if err != nil {
		return 0, err
	}
	j, err := s.Job(ctx, jobID)
	if err != nil {
		return 0, err
	}
	switch {
	case !site.Active:
		return 0, admin.UserError("This website is suspended.")
	case p.cipher == nil:
		return 0, admin.UserError("Publishing is not switched on for this server yet. Ask the administrator to set the server's secret key.")
	case j.Status != "done" || !validVersion(j.Version):
		return 0, admin.UserError("Only a publish that finished can be put back.")
	case j.Version == site.PublishedVersion:
		return 0, admin.UserError("That is the version already on your hosting.")
	}
	if _, ok, err := p.loadTarget(ctx, s.id); err != nil {
		return 0, err
	} else if !ok || site.PublicURL == "" {
		return 0, admin.UserError("Set up your hosting first (the connection details and your website's address).")
	}
	if _, _, _, err := p.copyOf(site, j.Version); err != nil {
		return 0, admin.UserError(capitalise(err.Error()) + ".")
	}
	var id int64
	err = p.pool.QueryRow(ctx, `INSERT INTO publish_jobs (site_id, status, started_by, force, restore_version, restore_of) VALUES ($1, 'queued', $2, true, $3, $4) RETURNING id`, s.id, actor, j.Version, j.ID).Scan(&id)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return 0, admin.UserError("A publish is running. Wait for it to finish, then go back.")
	}
	if err != nil {
		return 0, err
	}
	select {
	case p.pub.queue <- id:
	default:
		p.pool.Exec(ctx, `UPDATE publish_jobs SET status = 'failed', error = 'The server is busy. Try again in a few minutes.', finished_at = now() WHERE id = $1`, id)
		return 0, admin.UserError("The server is busy publishing other websites. Try again in a few minutes.")
	}
	p.audit(ctx, actor, &site, "started putting an earlier version back", j.Version)
	return id, nil
}

// --- doing it ---

func (p *Platform) setJob(ctx context.Context, id int64, set string, args ...any) {
	if _, err := p.pool.Exec(ctx, `UPDATE publish_jobs SET `+set+` WHERE id = $1`, append([]any{id}, args...)...); err != nil {
		log.Printf("publish job %d: %v", id, err)
	}
}

func (p *Platform) failJob(ctx context.Context, id int64, site *Site, secret string, err error) {
	msg := friendly(err, secret)
	p.setJob(context.Background(), id, `status = 'failed', error = $2, finished_at = now()`, msg)
	if site != nil {
		p.audit(context.Background(), "system", site, "a publish failed", msg)
	}
}

func (p *Platform) runJob(ctx context.Context, id int64) {
	var siteID int64
	var force bool
	var status, by, restore string
	if err := p.pool.QueryRow(ctx, `UPDATE publish_jobs SET status = 'running', started_at = now(), message = 'Starting' WHERE id = $1 AND status = 'queued' RETURNING site_id, force, status, started_by, restore_version`, id).
		Scan(&siteID, &force, &status, &by, &restore); err != nil {
		return // not queued any more (cancelled, or taken by another worker)
	}
	site, err := p.siteByID(ctx, siteID)
	if err != nil {
		p.failJob(ctx, id, nil, "", err)
		return
	}
	if !site.Active {
		p.failJob(ctx, id, &site, "", errors.New("this website is suspended"))
		return
	}
	tg, ok, err := p.target(ctx, siteID)
	if !ok || err != nil {
		if err == nil {
			err = errors.New("the hosting settings are missing")
		}
		p.failJob(ctx, id, &site, "", err)
		return
	}
	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	// the files to publish: the latest copy if it is up to date, otherwise a new one
	p.setJob(jobCtx, id, `message = 'Preparing the site'`)
	var info BuildInfo
	var dir string
	var files []publish.File
	if restore != "" {
		info, dir, files, err = p.copyOf(site, restore)
	} else {
		info, dir, files, err = p.copyForPublish(jobCtx, site)
	}
	if err != nil {
		p.failJob(ctx, id, &site, tg.Password, err)
		return
	}
	p.setJob(jobCtx, id, `version = $2, files_total = $3, message = 'Connecting to your hosting'`, info.Version, len(files)+1)

	remote, seen, err := publish.Connect(jobCtx, tg, p.connectOptions())
	var hk publish.HostKeyError
	if errors.As(err, &hk) {
		p.failJob(ctx, id, &site, tg.Password, errors.New(publish.ErrHostKeyChanged.Error()+". Open the hosting settings and test the connection"))
		return
	}
	if err != nil {
		p.failJob(ctx, id, &site, tg.Password, err)
		return
	}
	defer remote.Close()
	if tg.Protocol == publish.SFTP && tg.HostKey == "" && seen != "" {
		p.pool.Exec(jobCtx, `UPDATE publish_targets SET host_key = $2 WHERE site_id = $1`, siteID, seen)
	}
	var last time.Time
	rep, err := publish.Run(jobCtx, remote, dir, files, publish.Options{Force: force, Version: info.Version}, func(done, total int, msg string) {
		if time.Since(last) < 400*time.Millisecond && done < total {
			return
		}
		last = time.Now()
		p.setJob(jobCtx, id, `files_done = $2, files_total = $3, message = $4`, done, total, msg)
	})
	if err != nil {
		p.failJob(ctx, id, &site, tg.Password, err)
		return
	}
	warnings := append([]string(nil), info.Warnings...)
	if w := p.verify(jobCtx, site.PublicURL); w != "" {
		warnings = append(warnings, w)
	}
	if len(warnings) > 20 {
		warnings = append(warnings[:20], fmt.Sprintf("…and %d more.", len(warnings)-20))
	}
	p.setJob(ctx, id, `status = 'done', finished_at = now(), uploaded = $2, deleted = $3, unchanged = $4, bytes = $5, message = 'Done', warnings = $6, files_done = files_total`,
		rep.Uploaded, rep.Deleted, rep.Unchanged, rep.Bytes, strings.Join(warnings, "\n"))
	p.pool.Exec(ctx, `UPDATE sites SET published_version = $2, published_changes = $3, published_at = now() WHERE id = $1`, siteID, info.Version, info.Changes)
	verb := "published the website"
	if restore != "" {
		verb = "put an earlier version of the website back"
	}
	p.audit(ctx, by, &site, verb, fmt.Sprintf("%d uploaded, %d removed", rep.Uploaded, rep.Deleted))
}

// copyForPublish returns the static copy to publish and its files. The latest copy is used when nothing has changed since
// and it was made for the address the site is published at; otherwise a new one is made, so everything saved so far is in it.
func (p *Platform) copyForPublish(ctx context.Context, s Site) (BuildInfo, string, []publish.File, error) {
	current, err := p.currentChanges(ctx, s)
	if err != nil {
		return BuildInfo{}, "", nil, err
	}
	var info BuildInfo
	reuse := validVersion(s.BuildVersion) && s.BuildChanges == current
	if reuse {
		if b, err := os.ReadFile(filepath.Join(p.BuildDir(s.ID, s.BuildVersion), ".wb-build.json")); err == nil {
			reuse = jsonInto(b, &info) == nil && info.URL == s.PublicURL
		} else {
			reuse = false
		}
	}
	if !reuse {
		if info, err = p.Build(ctx, s); err != nil {
			return BuildInfo{}, "", nil, err
		}
	}
	return p.filesOf(s.ID, info)
}

// filesOf lists the files of a static copy.
func (p *Platform) filesOf(siteID int64, info BuildInfo) (BuildInfo, string, []publish.File, error) {
	dir := p.BuildDir(siteID, info.Version)
	list, err := export.Manifest(dir)
	if err != nil {
		return BuildInfo{}, "", nil, err
	}
	files := make([]publish.File, 0, len(list))
	for _, f := range list {
		if f.Path == ".wb-build.json" { // the program's own note about the copy is not part of the website
			continue
		}
		files = append(files, publish.File{Path: f.Path, Size: f.Size, SHA256: f.SHA256})
	}
	return info, dir, files, nil
}

// copyOf is an earlier static copy, to put back. It must still exist and have been made for the address the website is
// published at now: a copy made for another address has that address in its links.
func (p *Platform) copyOf(s Site, version string) (BuildInfo, string, []publish.File, error) {
	var info BuildInfo
	if !validVersion(version) {
		return info, "", nil, errors.New("that version is not available")
	}
	b, err := os.ReadFile(filepath.Join(p.BuildDir(s.ID, version), ".wb-build.json"))
	if err != nil || jsonInto(b, &info) != nil {
		return info, "", nil, errors.New("that version is no longer kept")
	}
	if info.URL != s.PublicURL {
		return info, "", nil, errors.New("that version was made for another website address (" + info.URL + ") and cannot be put back")
	}
	return p.filesOf(s.ID, info)
}

func (p *Platform) currentChanges(ctx context.Context, s Site) (int64, error) {
	rt, release, err := p.mgr.Acquire(ctx, s.ID, s.Schema)
	if err != nil {
		return 0, err
	}
	defer release()
	return rt.App.ChangeCount(ctx)
}

// verify asks the published address for the home page, to catch a wrong address or a folder the web server does not serve.
// It only warns: a hosting can take a minute to show new files.
func (p *Platform) verify(ctx context.Context, public string) string {
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: publish.Guard{AllowPrivate: p.cfg.AllowPrivateHosts}.DialContext, TLSClientConfig: p.tlsConfig},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, public+"/", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "wb-publish-check")
	resp, err := client.Do(req)
	if err != nil {
		return "The published address did not answer when it was checked (" + public + "). If the address is right, the hosting may need a minute."
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("The published address answered with an error (%d) when it was checked. Check that %s is the address of the folder the site was uploaded to.", resp.StatusCode, public)
	}
	return ""
}

func jsonInto(b []byte, v any) error { return json.Unmarshal(b, v) }

// TrustHostingCertificates makes FTPS and the post-publish check accept certificates signed by these authorities, and no
// others. For tests, which run a fake hosting with a certificate of its own.
func (p *Platform) TrustHostingCertificates(pool *x509.CertPool, serverName string) {
	p.tlsConfig = &tls.Config{RootCAs: pool, ServerName: serverName, MinVersion: tls.VersionTLS12}
}
