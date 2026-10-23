package platform

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/admin"
	"webbuilder/internal/auth"
	"webbuilder/internal/config"
	"webbuilder/internal/db"
	"webbuilder/internal/mailer"
	"webbuilder/internal/publish"
	"webbuilder/internal/secure"
	"webbuilder/internal/themes"
)

// Platform is the whole multi-website service.
type Platform struct {
	// Handler serves everything: the platform's screens, each website's editor and preview.
	Handler http.Handler

	cfg   config.Config
	pool  *pgxpool.Pool
	store *auth.Store
	authn *auth.Auth
	mgr   *Manager
	adm   *admin.Handler
	mail  mailer.Mailer

	cipher    *publish.Cipher // nil when the server has no SECRET_KEY: hosting passwords cannot be stored
	pub       *publisher
	tlsConfig *tls.Config // nil in production (certificates are verified against the system's authorities); tests set one
}

// New builds the platform on a database: it migrates the platform's own tables, makes sure every website's schema is
// up to date, and wires the screens.
func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) (*Platform, error) {
	if err := db.MigratePlatform(pool); err != nil {
		return nil, err
	}
	engine, err := themes.Load()
	if err != nil {
		return nil, err
	}
	store := auth.NewStore(pool)
	authn := auth.New(store, cfg.SecureCookies, cfg.TrustProxy)
	mail := mailer.Mailer{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.SMTPFrom}
	p := &Platform{cfg: cfg, pool: pool, store: store, authn: authn, mgr: NewManager(cfg, engine), mail: mail}
	if cfg.SecretKey != "" {
		if p.cipher, err = publish.NewCipher(cfg.SecretKey); err != nil {
			return nil, err
		}
	}
	p.mgr.publishFor = p.publisherFor

	p.adm, err = admin.New(admin.Deps{Auth: authn, Mail: mail, SetupToken: cfg.SetupToken, BaseURL: cfg.BaseURL, Platform: p})
	if err != nil {
		return nil, err
	}
	if err := p.migrateSites(ctx); err != nil {
		return nil, err
	}
	p.startPublisher()
	// the published copies' forms are sent from another origin by design; remoteForms checks the origin itself
	cop := http.NewCrossOriginProtection()
	cop.AddInsecureBypassPattern(FormsBase)
	p.Handler = secure.Middleware(cfg.TrustProxy)(cop.Handler(p.loadSession(p.routes())))
	return p, nil
}

// migrateSites brings every website's schema up to the current version (a no-op when it already is), so an update of the
// program that adds a migration reaches every website before anyone is served.
func (p *Platform) migrateSites(ctx context.Context) error {
	sites, err := p.listSites(ctx)
	if err != nil {
		return err
	}
	for _, s := range sites {
		pool, err := db.ConnectSchema(ctx, p.cfg.DatabaseURL, s.Schema, 1)
		if err != nil {
			return err
		}
		err = db.MigrateSite(pool)
		pool.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Run does the platform's housekeeping until ctx ends.
func (p *Platform) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.store.PurgeExpired(ctx); err != nil {
				log.Printf("purge expired: %v", err)
			}
		}
	}
}

// Close stops publishing and shuts every loaded website down.
func (p *Platform) Close() {
	p.stopPublisher()
	p.mgr.Close()
}

// Loaded is how many websites are in memory (for tests and the health check).
func (p *Platform) Loaded() int { return p.mgr.Loaded() }
