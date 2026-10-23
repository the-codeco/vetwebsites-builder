// Package app wires every part of the site together. main() runs it; the
// integration tests build exactly the same thing.
package app

import (
	"context"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/admin"
	"webbuilder/internal/analytics"
	"webbuilder/internal/auth"
	"webbuilder/internal/backup"
	"webbuilder/internal/blocks"
	"webbuilder/internal/campaign"
	"webbuilder/internal/config"
	"webbuilder/internal/contact"
	"webbuilder/internal/content"
	"webbuilder/internal/domain"
	"webbuilder/internal/mailer"
	"webbuilder/internal/media"
	"webbuilder/internal/newsletter"
	"webbuilder/internal/search"
	"webbuilder/internal/secure"
	"webbuilder/internal/site"
	"webbuilder/internal/themes"
)

type App struct {
	// Handler serves the public site and the admin.
	Handler http.Handler
	// Internal serves endpoints meant for the reverse proxy only (Caddy's TLS check).
	// It must be listened on a port that is never published.
	Internal http.Handler

	auth       *auth.Store
	analytics  *analytics.Service
	newsletter *newsletter.Service
	campaigns  *campaign.Service
	backups    *backup.Service
	content    *content.Store
	site       *site.Service

	backupKeep    int
	backupPersonal bool
	closers        []io.Closer
}

// Close releases files held open by the app (not the database pool, which the caller owns).
func (a *App) Close() error {
	var first error
	for _, c := range a.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Options changes how a website is built when a platform runs it (see internal/platform).
type Options struct {
	// MultiTenant: the platform does the logging in, sets the identity on each request and applies the outer
	// middleware, so the website serves no login screens and loads no sessions of its own.
	MultiTenant bool
	// PreviewBase is where the platform mounts the website's live preview ("/preview/live").
	PreviewBase string
	// Engine is the theme set shared by every website (themes are parsed once, not once per site).
	Engine *themes.Engine
	// MediaDir and BackupDir are this website's own folders.
	MediaDir, BackupDir string
	// Publish adds the publish bar and screens (the platform publishes this website).
	Publish admin.PublishService
}

// New builds the application on an already-migrated database.
func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) (*App, error) {
	return NewWith(ctx, cfg, pool, Options{})
}

// NewWith is New with options, used by the platform to build one website's runtime.
func NewWith(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, opt Options) (*App, error) {
	if opt.MediaDir != "" {
		cfg.MediaDir = opt.MediaDir
	}
	if opt.BackupDir != "" {
		cfg.BackupDir = opt.BackupDir
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	authStore := auth.NewStore(pool)
	authn := auth.New(authStore, cfg.SecureCookies, cfg.TrustProxy)
	contentStore := content.NewStore(pool)

	mediaSvc, err := media.New(pool, cfg.MediaDir)
	if err != nil {
		return nil, err
	}
	engine := opt.Engine
	if engine == nil {
		var err error
		if engine, err = themes.Load(); err != nil {
			return nil, err
		}
	}
	domainSvc := domain.New(contentStore)
	stats, err := analytics.New(ctx, pool, authn.ClientIP, func(ctx context.Context) string {
		d, _ := domainSvc.Current(ctx)
		return d
	})
	if err != nil {
		return nil, err
	}

	backupDir := cfg.BackupDir
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(filepath.Clean(cfg.MediaDir)), "backups")
	}
	backupSvc, err := backup.New(pool, cfg.MediaDir, backupDir, engine)
	if err != nil {
		return nil, err
	}

	mail := mailer.Mailer{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.SMTPFrom,
	}
	contactSvc := contact.New(pool, mail, authn.ClientIP)

	newsletterSvc := newsletter.New(pool, mail, authn.ClientIP)
	campaignSvc := campaign.New(pool, mail)

	drafts := blocks.NewDrafts() // unsaved block edits: the editor writes them, the live preview shows them
	adminH, err := admin.New(admin.Deps{
		Drafts: drafts,
		Auth: authn, Content: contentStore, Media: mediaSvc, Engine: engine,
		Domain: domainSvc, Checker: domain.NewChecker(), SetupToken: cfg.SetupToken,
		Analytics: stats, Contact: contactSvc, Newsletter: newsletterSvc, Campaigns: campaignSvc,
		Backup: backupSvc, BackupKeep: cfg.BackupKeep, BackupPersonal: cfg.BackupPersonal, Mail: mail,
		MultiTenant: opt.MultiTenant, PreviewBase: opt.PreviewBase, Publish: opt.Publish,
	})
	if err != nil {
		return nil, err
	}
	adminH.Register(mux)

	siteSvc, err := site.New(pool, engine, cfg.MediaDir)
	if err != nil {
		return nil, err
	}
	if err := siteSvc.Seed(ctx); err != nil {
		return nil, err
	}
	// the search index of pages is built from their blocks: bring it up to date (covers starter content too)
	if err := contentStore.ReindexAllPages(ctx); err != nil {
		return nil, err
	}
	siteSvc.Drafts = drafts
	siteSvc.Contact = contactSvc
	siteSvc.Search = search.New(pool, authn.ClientIP)
	siteSvc.Newsletter = newsletterSvc
	siteSvc.Register(mux)

	var handler http.Handler
	if opt.MultiTenant {
		// the platform supplies the outer middleware and the identity; what is left is cross-origin protection and
		// page-view counting (which ignores logged-in people, so previews are not counted)
		handler = http.NewCrossOriginProtection().Handler(stats.Track(mux))
	} else {
		domain.RegisterPublic(mux)
		// Outermost first: headers/limits/panic recovery, then the canonical-domain
		// redirect, then cross-origin protection (Origin / Sec-Fetch-Site checks on
		// state-changing requests, on top of per-form CSRF tokens), then sessions.
		handler = secure.Middleware(cfg.TrustProxy)(
			domainSvc.Canonical(
				http.NewCrossOriginProtection().Handler(
					authn.LoadSession(stats.Track(mux)))))
	}

	internal := http.NewServeMux()
	internal.HandleFunc("GET /tls-check", domainSvc.TLSCheck)

	return &App{Handler: handler, Internal: internal, site: siteSvc, content: contentStore, auth: authStore, analytics: stats, newsletter: newsletterSvc, campaigns: campaignSvc,
		backups: backupSvc, backupKeep: cfg.BackupKeep, backupPersonal: cfg.BackupPersonal,
		closers: []io.Closer{stats, mediaSvc, siteSvc, backupSvc}}, nil
}

// FormAPI serves the contact and newsletter forms of a published static copy of the site (see site.Service.FormAPI).
func (a *App) FormAPI() http.Handler { return a.site.FormAPI() }

// FlushAnalytics writes any page views still waiting in memory. They are normally saved every few
// seconds; this is for tests and for anything that needs the numbers right now.
func (a *App) FlushAnalytics(ctx context.Context) error { return a.analytics.Flush(ctx) }

// Housekeeping deletes expired sessions and invites and page views past their retention period.
func (a *App) Housekeeping(ctx context.Context) {
	if err := a.auth.PurgeExpired(ctx); err != nil {
		log.Printf("purge expired: %v", err)
	}
	if err := a.analytics.Purge(ctx); err != nil {
		log.Printf("purge analytics: %v", err)
	}
	if err := a.newsletter.PurgePending(ctx); err != nil {
		log.Printf("purge unconfirmed signups: %v", err)
	}
}

// Maintain runs housekeeping (expired sessions and invites, old page views) until ctx ends.
func (a *App) Maintain(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Housekeeping(ctx)
		}
	}
}

// SendMail sends queued newsletters, one message at a time, until ctx ends.
func (a *App) SendMail(ctx context.Context) { a.campaigns.Run(ctx, log.Printf) }

// DeliverQueuedMail sends everything that is due right now and returns how many messages it sent. The sender
// normally paces itself; this is for tests.
func (a *App) DeliverQueuedMail(ctx context.Context) (int, error) {
	n := 0
	for {
		worked, err := a.campaigns.Tick(ctx)
		if err != nil || !worked {
			return n, err
		}
		n++
	}
}

// Campaigns gives tests access to the newsletter sender's settings (its pauses and retry times).
func (a *App) Campaigns() *campaign.Service { return a.campaigns }

// RunBackups makes the scheduled backups and tidies the backup folder until ctx ends.
func (a *App) RunBackups(ctx context.Context) {
	a.backups.Run(ctx, a.backupKeep, a.backupPersonal, log.Printf)
}

// Backups gives tests access to the backup service.
func (a *App) Backups() *backup.Service { return a.backups }

// Publish publishes scheduled pages and posts when their time comes, until ctx ends. A site that was down when a
// time passed publishes them as soon as it is back.
func (a *App) Publish(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		if p, q, err := a.PublishDue(ctx); err != nil {
			log.Printf("publish scheduled: %v", err)
		} else if p+q > 0 {
			log.Printf("published %d scheduled page(s) and %d post(s)", p, q)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PublishDue publishes everything scheduled that is due now. The loop above calls it; tests call it directly.
func (a *App) PublishDue(ctx context.Context) (pages, posts int, err error) {
	return a.content.PublishDue(ctx)
}

// SnapshotPage saves a restore point for a page right now (the admin does this itself before edits). It is here for tests.
func (a *App) SnapshotPage(ctx context.Context, pageID int64, by string) error {
	return a.content.SnapshotPage(ctx, pageID, by)
}

// SetSiteTitle sets the website title (the platform names a new website after the customer gives it a name).
func (a *App) SetSiteTitle(ctx context.Context, title string) error {
	return a.content.SetTitle(ctx, title)
}
