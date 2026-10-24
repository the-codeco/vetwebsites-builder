package platform_test

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"webbuilder/internal/config"
	"webbuilder/internal/publish/fakehost"
)

// fakeMail is an SMTP server that accepts every message and keeps it.
type fakeMail struct {
	host, port string
	mu         sync.Mutex
	msgs       []string
}

func startFakeMail(t *testing.T) *fakeMail {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	f := &fakeMail{host: host, port: port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				say := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
				say("220 fake ESMTP")
				var rcpt []string
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "RCPT"):
						rcpt = append(rcpt, strings.TrimSpace(line))
						say("250 ok")
					case cmd == "DATA":
						say("354 go ahead")
						var data strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if l == ".\r\n" {
								break
							}
							data.WriteString(l)
						}
						say("250 queued")
						f.mu.Lock()
						f.msgs = append(f.msgs, strings.Join(rcpt, "\n")+"\n\n"+data.String())
						f.mu.Unlock()
						rcpt = nil
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}()
		}
	}()
	return f
}

func (f *fakeMail) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.msgs...)
}

// dueIn makes a draft page of a website look as if it had been scheduled for a few minutes ago.
func (e *env) scheduleOverdue(schema, slug string) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), `UPDATE `+schema+`.pages SET status = 'draft', publish_at = now() - interval '5 minutes' WHERE slug = $1`, slug); err != nil {
		e.t.Fatal(err)
	}
}

// A lone website does its own background work. On a platform one sweep does it for every website: scheduled pages
// and posts go live, queued newsletters are sent, a website that asked for it publishes itself, and the hourly
// chores run, all without anybody being logged in and only for websites that are active.
func TestSweepDoesEverySitesBackgroundWork(t *testing.T) {
	mail := startFakeMail(t)
	e := newEnv(t, withKey, func(c *config.Config) {
		c.SMTPHost, c.SMTPPort, c.SMTPFrom = mail.host, mail.port, "noreply@builder.example.test"
		c.BackupKeep = 3
	})
	ctx := t.Context()
	admin := e.setupAdmin("admin@example.com")
	ann := e.customer(admin, "ann@a.example", "Acme Dental")
	bob := e.customer(admin, "bob@b.example", "Beta Bakery")
	newPage(t, ann, "Soon", "soon", "<p>Coming up</p>")
	newPage(t, bob, "Later", "later", "<p>Bread</p>")
	e.scheduleOverdue("site_1", "soon")
	e.scheduleOverdue("site_2", "later")
	e.pool.Exec(ctx, `UPDATE sites SET status = 'suspended' WHERE name = 'Beta Bakery'`)

	// nothing is due until something is: a quiet platform serves nobody
	if n := e.p.SweepOnce(ctx); n != 1 {
		t.Fatalf("one website had something due, the sweep served %d", n)
	}
	if got := e.scalar(`SELECT status FROM site_1.pages WHERE slug = 'soon'`); got != "published" {
		t.Errorf("the scheduled page should be live, it is %q", got)
	}
	if got := e.scalar(`SELECT status FROM site_2.pages WHERE slug = 'later'`); got != "draft" {
		t.Errorf("a suspended website is left alone, its page is %q", got)
	}
	if got := e.scalar(`SELECT count(*) FROM audit_log WHERE action = 'scheduled content went live' AND site_name = 'Acme Dental'`); got != "1" {
		t.Errorf("the audit log should say so: %s", got)
	}
	if n := e.p.SweepOnce(ctx); n != 0 {
		t.Errorf("nothing is due now, the sweep served %d", n)
	}

	// the suspended website goes back to work when it is switched back on
	e.pool.Exec(ctx, `UPDATE sites SET status = 'active' WHERE name = 'Beta Bakery'`)
	if n := e.p.SweepOnce(ctx); n != 1 || e.scalar(`SELECT status FROM site_2.pages WHERE slug = 'later'`) != "published" {
		t.Errorf("a website that is active again catches up (%d)", n)
	}

	// queued newsletters are sent, to each person, from the website that queued them
	e.pool.Exec(ctx, `INSERT INTO site_1.subscribers (email, confirmed_at, token_hash) VALUES ('a@x.example', now(), 'h1'), ('b@x.example', now(), 'h2')`)
	e.pool.Exec(ctx, `INSERT INTO site_1.campaigns (subject, body, status, total, base_url, site_title) VALUES ('Spring news', 'Hello from Acme', 'sending', 2, 'https://builder.example.test/f/tok', 'Acme Dental')`)
	e.pool.Exec(ctx, `INSERT INTO site_1.deliveries (campaign_id, subscriber_id) SELECT c.id, s.id FROM site_1.campaigns c, site_1.subscribers s`)
	if n := e.p.SweepOnce(ctx); n != 1 {
		t.Fatalf("a newsletter was waiting: %d", n)
	}
	var msgs []string
	for _, m := range mail.all() { // the customers' welcome emails are in there too
		if strings.Contains(m, "Spring news") {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) != 2 || !strings.Contains(msgs[0]+msgs[1], "a@x.example") || !strings.Contains(msgs[0]+msgs[1], "b@x.example") {
		t.Fatalf("both subscribers should have the newsletter: %d messages", len(msgs))
	}
	if !strings.Contains(msgs[0], "Spring news") || !strings.Contains(msgs[0], "https://builder.example.test/f/tok/unsubscribe/") {
		t.Errorf("the newsletter and the person's own unsubscribe link: %s", msgs[0])
	}
	if got := e.scalar(`SELECT count(*) FROM site_1.deliveries WHERE status = 'sent'`); got != "2" {
		t.Errorf("sent: %s", got)
	}
	if n := e.p.SweepOnce(ctx); n != 0 {
		t.Errorf("the newsletter is done: %d", n)
	}

	// a website that asked for it publishes itself when scheduled content goes live
	host := fakehost.StartSFTP(t)
	form := hostingForm(host, "sftp", "http://127.0.0.1:9/")
	if r := ann.post("/admin/hosting", form); r.Status != 303 {
		t.Fatalf("hosting: %d", r.Status)
	}
	newPage(t, ann, "Again", "again", "<p>More</p>")
	e.scheduleOverdue("site_1", "again")
	e.p.SweepOnce(ctx)
	if got := e.scalar(`SELECT count(*) FROM publish_jobs WHERE started_by LIKE 'system%'`); got != "0" {
		t.Errorf("automatic publishing is opt-in: %s jobs", got)
	}
	form.Set("auto_publish", "on")
	if r := ann.post("/admin/hosting", form); r.Status != 303 {
		t.Fatalf("hosting: %d", r.Status)
	}
	if !strings.Contains(ann.get("/admin/hosting").Body, `name="auto_publish" checked`) {
		t.Error("the setting should show as on")
	}
	newPage(t, ann, "Third", "third", "<p>Even more</p>")
	e.scheduleOverdue("site_1", "third")
	e.p.SweepOnce(ctx)
	var status string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		status = e.scalar(`SELECT COALESCE(max(status), '') FROM publish_jobs WHERE started_by = 'system (scheduled content)'`)
		if status == "done" || status == "failed" {
			break
		}
	}
	if status != "done" || !host.Exists("third.html") {
		t.Errorf("the scheduled page should have been published to the hosting: %q", status)
	}

	// the hourly chores: every active website gets its scheduled backup
	e.p.HousekeepOnce(ctx)
	for _, id := range []string{"1", "2"} {
		files, _ := filepath.Glob(filepath.Join(e.cfg.DataDir, "backups", id, "*"))
		if len(files) == 0 {
			t.Errorf("website %s has no backup", id)
		}
	}
	// and not for a suspended one
	e.pool.Exec(ctx, `UPDATE sites SET status = 'suspended' WHERE name = 'Beta Bakery'`)
	os.RemoveAll(filepath.Join(e.cfg.DataDir, "backups", "2"))
	e.p.HousekeepOnce(ctx)
	if files, _ := filepath.Glob(filepath.Join(e.cfg.DataDir, "backups", "2", "*")); len(files) != 0 {
		t.Error("a suspended website is not backed up by the sweep")
	}
	_ = url.Values{}
}
