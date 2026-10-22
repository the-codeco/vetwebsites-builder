// Package platform runs many websites on one server: it owns the accounts and the list of sites, does the logging in,
// and hands each request to the right website's own program (a "runtime"), which lives in its own database schema.
package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/admin"
	"webbuilder/internal/app"
	"webbuilder/internal/config"
	"webbuilder/internal/db"
	"webbuilder/internal/themes"
)

// Runtime is one website's program, built on a pool that only sees that website's schema.
type Runtime struct {
	SiteID int64
	App    *app.App

	pool   *pgxpool.Pool
	cancel context.CancelFunc
	refs   int
	last   time.Time
	doomed bool // to be closed as soon as nobody is using it

	usersMu sync.Mutex
	users   map[int64]bool // platform accounts already mirrored into this website users table
}

// Manager keeps recently used websites loaded and closes the least recently used when there are too many, so hundreds
// of websites fit in a bounded number of database connections and amount of memory.
type Manager struct {
	cfg    config.Config
	engine *themes.Engine
	max    int
	// poolConns is how many database connections one loaded website may hold.
	poolConns int32
	// publishFor makes the publish screens for one website (set by the platform).
	publishFor func(siteID int64) admin.PublishService

	mu  sync.Mutex
	rts map[int64]*Runtime
}

// NewManager makes a manager. engine is shared by every website.
func NewManager(cfg config.Config, engine *themes.Engine) *Manager {
	return &Manager{cfg: cfg, engine: engine, max: max(cfg.MaxRuntimes, 1), poolConns: 4, rts: map[int64]*Runtime{}}
}

// MediaDir and BackupDir are a website's own folders.
func (m *Manager) MediaDir(siteID int64) string {
	return filepath.Join(m.cfg.DataDir, "media", fmt.Sprint(siteID))
}
func (m *Manager) BackupDir(siteID int64) string {
	return filepath.Join(m.cfg.DataDir, "backups", fmt.Sprint(siteID))
}

// ErrClosed is returned when the manager has been shut down.
// ErrBusy is returned for a website whose program is being shut down while requests are still finishing.
var ErrBusy = errors.New("platform: website is being closed, try again")

// Acquire returns the website's runtime, building it if it is not loaded, and a release function the caller must call
// when the request is done. A runtime in use is never closed.
func (m *Manager) Acquire(ctx context.Context, siteID int64, schema string) (*Runtime, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.rts[siteID]
	if rt != nil && rt.doomed {
		if rt.refs > 0 {
			return nil, nil, ErrBusy // being closed; the caller retries in a moment
		}
		m.closeLocked(rt)
		rt = nil
	}
	if rt == nil {
		var err error
		if rt, err = m.buildLocked(ctx, siteID, schema); err != nil {
			return nil, nil, err
		}
	}
	rt.refs++
	rt.last = time.Now()
	released := false
	return rt, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		rt.refs--
		if rt.doomed && rt.refs == 0 {
			m.closeLocked(rt)
		}
	}, nil
}

func (m *Manager) buildLocked(ctx context.Context, siteID int64, schema string) (*Runtime, error) {
	m.evictLocked(m.max - 1) // make room first
	pool, err := db.ConnectSchema(ctx, m.cfg.DatabaseURL, schema, m.poolConns)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{m.MediaDir(siteID), m.BackupDir(siteID)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			pool.Close()
			return nil, err
		}
	}
	rctx, cancel := context.WithCancel(context.Background())
	a, err := app.NewWith(rctx, m.cfg, pool, app.Options{
		MultiTenant: true, PreviewBase: PreviewBase, Engine: m.engine,
		MediaDir: m.MediaDir(siteID), BackupDir: m.BackupDir(siteID), Publish: m.publish(siteID),
	})
	if err != nil {
		cancel()
		pool.Close()
		return nil, fmt.Errorf("site %d: %w", siteID, err)
	}
	rt := &Runtime{SiteID: siteID, App: a, pool: pool, cancel: cancel, last: time.Now(), users: map[int64]bool{}}
	m.rts[siteID] = rt
	return rt, nil
}

func (m *Manager) publish(siteID int64) admin.PublishService {
	if m.publishFor == nil {
		return nil
	}
	return m.publishFor(siteID)
}

// evictLocked closes least recently used runtimes that nobody is using until at most keep remain.
func (m *Manager) evictLocked(keep int) {
	for len(m.rts) > keep {
		var oldest *Runtime
		for _, rt := range m.rts {
			if rt.refs == 0 && (oldest == nil || rt.last.Before(oldest.last)) {
				oldest = rt
			}
		}
		if oldest == nil {
			return // everything is busy; go over the limit rather than cut off a request
		}
		m.closeLocked(oldest)
	}
}

func (m *Manager) closeLocked(rt *Runtime) {
	delete(m.rts, rt.SiteID)
	rt.cancel()
	rt.App.Close()
	rt.pool.Close()
}

// Evict closes a website's runtime (when it is deleted or suspended) as soon as nothing is using it.
func (m *Manager) Evict(siteID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt := m.rts[siteID]; rt != nil {
		rt.doomed = true
		if rt.refs == 0 {
			m.closeLocked(rt)
		}
	}
}

// Loaded is how many websites are in memory.
func (m *Manager) Loaded() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rts)
}

// Close shuts every runtime down.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rt := range m.rts {
		m.closeLocked(rt)
	}
}

// ensureUser makes sure the person at the keyboard has a row in the website's own users table. The website's tables
// record who wrote a post, uploaded a picture or sent a newsletter by user id, and the platform's account ids are not
// otherwise known to the website. The row cannot log in, because the website has no login of its own.
func (rt *Runtime) ensureUser(ctx context.Context, id int64, email string) error {
	rt.usersMu.Lock()
	known := rt.users[id]
	rt.usersMu.Unlock()
	if known {
		return nil
	}
	if _, err := rt.pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, role) VALUES ($1, $2, '!', 'admin') ON CONFLICT DO NOTHING`, id, email); err != nil {
		return err
	}
	rt.usersMu.Lock()
	rt.users[id] = true
	rt.usersMu.Unlock()
	return nil
}
