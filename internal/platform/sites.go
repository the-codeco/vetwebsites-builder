package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"webbuilder/internal/admin"
	"webbuilder/internal/auth"
	"webbuilder/internal/db"
)

// Site is a customer's website.
type Site struct {
	ID        int64
	OwnerID   int64
	Owner     string
	Name      string
	Schema    string
	Active    bool
	Token     string
	CreatedAt time.Time

	PublicURL    string     // where the website is published
	BuildVersion string     // the latest static copy ("" when none)
	BuildChanges int64      // the change counter when it was made (-1 when none)
	BuiltAt      *time.Time // and when

	PublishedVersion string     // the copy that is on the hosting ("" when never published)
	PublishedChanges int64      // the change counter when it was published (-1 when never)
	PublishedAt      *time.Time // and when
}

// onboardingLink is how long a new customer has to choose a first password.
const onboardingLink = 7 * 24 * time.Hour

const siteCols = `s.id, COALESCE(s.owner_id, 0), COALESCE(u.email, ''), s.name, s.schema_name, s.status = 'active', s.public_token, s.created_at,
	s.public_url, s.build_version, s.build_changes, s.built_at, s.published_version, s.published_changes, s.published_at`

func scanSite(row pgx.Row) (Site, error) {
	var s Site
	err := row.Scan(&s.ID, &s.OwnerID, &s.Owner, &s.Name, &s.Schema, &s.Active, &s.Token, &s.CreatedAt, &s.PublicURL, &s.BuildVersion, &s.BuildChanges, &s.BuiltAt, &s.PublishedVersion, &s.PublishedChanges, &s.PublishedAt)
	return s, err
}

func (p *Platform) siteByID(ctx context.Context, id int64) (Site, error) {
	return scanSite(p.pool.QueryRow(ctx, `SELECT `+siteCols+` FROM sites s LEFT JOIN users u ON u.id = s.owner_id WHERE s.id = $1`, id))
}

func (p *Platform) siteByOwner(ctx context.Context, userID int64) (Site, error) {
	return scanSite(p.pool.QueryRow(ctx, `SELECT `+siteCols+` FROM sites s LEFT JOIN users u ON u.id = s.owner_id WHERE s.owner_id = $1`, userID))
}

func (p *Platform) listSites(ctx context.Context) ([]Site, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+siteCols+` FROM sites s LEFT JOIN users u ON u.id = s.owner_id ORDER BY lower(s.name), s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		s, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// audit records an action. A failure to write the log is reported but never blocks the action itself.
func (p *Platform) audit(ctx context.Context, actor string, s *Site, action, detail string) {
	var id any
	name := ""
	if s != nil {
		id, name = s.ID, s.Name
	}
	if _, err := p.pool.Exec(ctx, `INSERT INTO audit_log (actor, site_id, site_name, action, detail) VALUES ($1, $2, $3, $4, $5)`,
		actor, id, name, action, detail); err != nil {
		log.Printf("audit log: %v", err)
	}
}

func actorOf(a *auth.Identity) string {
	if a == nil {
		return "system"
	}
	return a.Email
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// --- admin.PlatformService ---

func (p *Platform) Sites(ctx context.Context) ([]admin.SiteRow, error) {
	sites, err := p.listSites(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admin.SiteRow, len(sites))
	for i, s := range sites {
		out[i] = admin.SiteRow{ID: s.ID, Name: s.Name, Owner: s.Owner, Active: s.Active, CreatedAt: s.CreatedAt}
	}
	return out, nil
}

// cleanName keeps a website name on one line and within its limit.
func cleanName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return s
}

// CreateCustomer makes an account and its website. The customer chooses their own password through a one-time link,
// which is returned (as a path) for the admin to pass on.
func (p *Platform) CreateCustomer(ctx context.Context, actor *auth.Identity, email, siteName string) (string, error) {
	email = auth.NormalizeEmail(email)
	name := cleanName(siteName)
	switch {
	case email == "":
		return "", admin.UserError("Enter a valid email address.")
	case name == "":
		return "", admin.UserError("Give the website a name.")
	}
	// the account starts with a password nobody knows; the customer sets their own through the link
	hash, err := auth.HashPassword(randomHex(24))
	if err != nil {
		return "", err
	}
	user, err := p.store.CreateUser(ctx, email, hash, auth.RoleCustomer)
	if errors.Is(err, auth.ErrEmailTaken) {
		return "", admin.UserError("That email address already has an account.")
	}
	if err != nil {
		return "", err
	}

	site, err := p.createSite(ctx, user.ID, name)
	if err != nil {
		p.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
		return "", err
	}
	token, err := p.store.CreateResetFor(ctx, user.ID, onboardingLink)
	if err != nil {
		return "", err
	}
	p.audit(ctx, actorOf(actor), &site, "created the website and account", email)
	return "/reset/" + token, nil
}

// createSite makes a site's row, schema and starting content.
func (p *Platform) createSite(ctx context.Context, ownerID int64, name string) (Site, error) {
	var id int64
	if err := p.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('sites', 'id'))`).Scan(&id); err != nil {
		return Site{}, err
	}
	schema := db.SchemaName(id)
	if _, err := p.pool.Exec(ctx, `INSERT INTO sites (id, owner_id, name, schema_name, public_token) VALUES ($1, $2, $3, $4, $5)`,
		id, ownerID, name, schema, randomHex(24)); err != nil {
		return Site{}, err
	}
	undo := func() {
		db.DropSchema(ctx, p.pool, schema)
		p.pool.Exec(ctx, `DELETE FROM sites WHERE id = $1`, id)
		os.RemoveAll(p.mgr.MediaDir(id))
		os.RemoveAll(p.mgr.BackupDir(id))
	}
	if err := db.CreateSchema(ctx, p.pool, schema); err != nil {
		undo()
		return Site{}, err
	}
	sp, err := db.ConnectSchema(ctx, p.cfg.DatabaseURL, schema, 1)
	if err != nil {
		undo()
		return Site{}, err
	}
	err = db.MigrateSite(sp)
	sp.Close()
	if err != nil {
		undo()
		return Site{}, err
	}
	site, err := p.siteByID(ctx, id)
	if err != nil {
		undo()
		return Site{}, err
	}
	// loading the website for the first time writes its starting pages; then its title becomes the customer's name for it
	rt, release, err := p.mgr.Acquire(ctx, site.ID, site.Schema)
	if err != nil {
		undo()
		return Site{}, err
	}
	defer release()
	if err := rt.App.SetSiteTitle(ctx, name); err != nil {
		undo()
		return Site{}, err
	}
	return site, nil
}

// Act starts (siteID > 0) or stops (0) a platform admin working on a customer's website. Starting is refused for a
// website that does not exist or is suspended, and both ways are written to the audit log.
func (p *Platform) Act(ctx context.Context, sessionToken string, actor *auth.Identity, siteID int64) error {
	if siteID == 0 {
		if actor != nil && actor.ActingSiteID > 0 {
			if s, err := p.siteByID(ctx, actor.ActingSiteID); err == nil {
				p.audit(ctx, actorOf(actor), &s, "stopped working on the website", "")
			}
		}
		return p.store.SetActingSite(ctx, sessionToken, 0)
	}
	s, err := p.siteByID(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.UserError("That website does not exist.")
	}
	if err != nil {
		return err
	}
	if !s.Active {
		return admin.UserError("That website is suspended. Resume it first.")
	}
	if err := p.store.SetActingSite(ctx, sessionToken, s.ID); err != nil {
		return err
	}
	p.audit(ctx, actorOf(actor), &s, "started working on the website", "as the customer's admin")
	return nil
}

// SetActive suspends or resumes a website. A suspended website's owner is signed out and cannot log in; an admin
// working on it is sent back to the list.
func (p *Platform) SetActive(ctx context.Context, actor *auth.Identity, siteID int64, active bool) error {
	s, err := p.siteByID(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.UserError("That website does not exist.")
	}
	if err != nil {
		return err
	}
	status := "suspended"
	if active {
		status = "active"
	}
	if _, err := p.pool.Exec(ctx, `UPDATE sites SET status = $2 WHERE id = $1`, siteID, status); err != nil {
		return err
	}
	if !active {
		if s.OwnerID > 0 {
			if err := p.store.EndSessionsOf(ctx, s.OwnerID); err != nil {
				return err
			}
		}
		if _, err := p.pool.Exec(ctx, `UPDATE sessions SET acting_site_id = NULL WHERE acting_site_id = $1`, siteID); err != nil {
			return err
		}
		p.mgr.Evict(siteID)
	}
	p.audit(ctx, actorOf(actor), &s, map[bool]string{true: "resumed the website", false: "suspended the website"}[active], "")
	return nil
}

// DeleteSite removes a website, its owner's account, its database schema and its files. The person must type the
// website's name, so it cannot be done by a stray click.
func (p *Platform) DeleteSite(ctx context.Context, actor *auth.Identity, siteID int64, typed string) error {
	s, err := p.siteByID(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return admin.UserError("That website does not exist.")
	}
	if err != nil {
		return err
	}
	if typed != s.Name {
		return admin.UserError("The name you typed does not match the website's name, so nothing was deleted.")
	}
	p.mgr.Evict(siteID)
	if _, err := p.pool.Exec(ctx, `UPDATE sessions SET acting_site_id = NULL WHERE acting_site_id = $1`, siteID); err != nil {
		return err
	}
	if err := db.DropSchema(ctx, p.pool, s.Schema); err != nil {
		return err
	}
	if _, err := p.pool.Exec(ctx, `DELETE FROM sites WHERE id = $1`, siteID); err != nil {
		return err
	}
	if s.OwnerID > 0 {
		if _, err := p.pool.Exec(ctx, `DELETE FROM users WHERE id = $1 AND role = 'customer'`, s.OwnerID); err != nil {
			return err
		}
	}
	for _, dir := range []string{p.mgr.MediaDir(siteID), p.mgr.BackupDir(siteID), filepath.Join(p.cfg.DataDir, "builds", fmt.Sprint(siteID))} {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("delete %s: %v", dir, err)
		}
	}
	p.audit(ctx, actorOf(actor), &s, "deleted the website and account", s.Owner)
	return nil
}

// ResetLink makes a one-time link for a customer to choose a new password.
func (p *Platform) ResetLink(ctx context.Context, actor *auth.Identity, siteID int64) (string, string, error) {
	s, err := p.siteByID(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && s.OwnerID == 0 {
		return "", "", admin.UserError("That website has no owner to make a link for.")
	}
	if err != nil {
		return "", "", err
	}
	token, err := p.store.CreateResetFor(ctx, s.OwnerID, onboardingLink)
	if err != nil {
		return "", "", err
	}
	p.audit(ctx, actorOf(actor), &s, "made a password link for the owner", s.Owner)
	return s.Owner, "/reset/" + token, nil
}

// Audit lists the latest entries of the audit log.
func (p *Platform) Audit(ctx context.Context, limit int) ([]admin.AuditRow, error) {
	rows, err := p.pool.Query(ctx, `SELECT at, actor, site_name, action, detail FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []admin.AuditRow
	for rows.Next() {
		var r admin.AuditRow
		if err := rows.Scan(&r.At, &r.Actor, &r.Site, &r.Action, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
