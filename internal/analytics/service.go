package analytics

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/auth"
)

const (
	flushEvery = 5 * time.Second
	flushAt    = 200  // hits waiting that trigger an early write
	maxPending = 5000 // beyond this, hits are dropped rather than slowing the site or filling memory

	// Retention is how long page views are kept. Older rows are deleted by Purge.
	Retention = 90 * 24 * time.Hour
)

// Service records page views and reports on them.
type Service struct {
	pool     *pgxpool.Pool
	clientIP func(*http.Request) string
	domain   func(context.Context) string // the saved custom domain, "" if none

	enabled atomic.Bool
	secret  atomic.Value // string

	mu      sync.Mutex
	pending []hit

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

type hit struct {
	day, path, referrer, visitor, device, browser string
}

// New loads the settings and starts the background writer. clientIP says which
// address a request came from (it must honour the trusted-proxy setting); domain
// returns the saved custom domain so links from the site itself aren't counted
// as referrals.
func New(ctx context.Context, pool *pgxpool.Pool, clientIP func(*http.Request) string, domain func(context.Context) string) (*Service, error) {
	s := &Service{
		pool: pool, clientIP: clientIP, domain: domain,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	if err := s.reload(ctx); err != nil {
		return nil, err
	}
	go s.run()
	return s, nil
}

func (s *Service) reload(ctx context.Context) error {
	var on bool
	var secret string
	if err := s.pool.QueryRow(ctx, `SELECT analytics_enabled, analytics_secret FROM site_settings WHERE id = 1`).Scan(&on, &secret); err != nil {
		return err
	}
	s.enabled.Store(on)
	s.secret.Store(secret)
	return nil
}

// Enabled reports whether page views are being recorded.
func (s *Service) Enabled() bool { return s.enabled.Load() }

// SetEnabled turns recording on or off. Turning it off keeps the data already collected.
func (s *Service) SetEnabled(ctx context.Context, on bool) error {
	if _, err := s.pool.Exec(ctx, `UPDATE site_settings SET analytics_enabled = $1 WHERE id = 1`, on); err != nil {
		return err
	}
	s.enabled.Store(on)
	return nil
}

// Close stops the writer after saving what is waiting.
func (s *Service) Close() error {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
	return nil
}

func (s *Service) run() {
	defer close(s.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.Flush(ctx)
			cancel()
			return
		case <-s.wake:
			s.flushLogged()
		case <-t.C:
			s.flushLogged()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.reload(ctx) // picks up a setting changed by another instance
			cancel()
		}
	}
}

func (s *Service) flushLogged() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Flush(ctx); err != nil {
		log.Printf("analytics: %v", err)
	}
}

// Flush writes the waiting page views now. It is called by the background
// writer, on shutdown, and by tests.
func (s *Service) Flush(ctx context.Context) error {
	s.mu.Lock()
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	rows := make([][]any, len(batch))
	for i, h := range batch {
		rows[i] = []any{h.day, h.path, h.referrer, h.visitor, h.device, h.browser}
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"hits"},
		[]string{"day", "path", "referrer", "visitor", "device", "browser"}, pgx.CopyFromRows(rows))
	return err // a failed batch is dropped: losing a few counts beats retrying forever
}

// --- recording ---

// statusRecorder notes what the page handler answered, without changing it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Track wraps the whole site. After a request has been answered it counts it,
// but only if a person's browser loaded a public HTML page successfully: not
// the admin, not drafts or previews (they are served no-store), not logged-in
// users, not bots, and not anyone who sent Do Not Track or Global Privacy Control.
// It needs the session middleware to have run first, so it can tell who is logged in.
func (s *Service) Track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusOK && s.shouldCount(r, w.Header()) {
			s.record(r)
		}
	})
}

func (s *Service) shouldCount(r *http.Request, h http.Header) bool {
	switch {
	case !s.enabled.Load(), r.Method != http.MethodGet:
		return false
	case !strings.HasPrefix(h.Get("Content-Type"), "text/html"), strings.Contains(h.Get("Cache-Control"), "no-store"):
		return false
	case !Countable(r.URL.Path), r.URL.Query().Get("theme") != "":
		return false
	case r.Header.Get("DNT") == "1", r.Header.Get("Sec-GPC") == "1":
		return false
	case IsBot(r.UserAgent()), auth.CurrentIdentity(r) != nil:
		return false
	}
	return true
}

func (s *Service) record(r *http.Request) {
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	secret, _ := s.secret.Load().(string)
	ua := r.UserAgent()
	own := []string{r.Host}
	if s.domain != nil {
		own = append(own, s.domain(r.Context()))
	}
	h := hit{
		day: day, path: r.URL.Path,
		referrer: ReferrerHost(r.Referer(), own...),
		visitor:  VisitorHash(secret, day, s.clientIP(r), ua),
		device:   Device(ua), browser: Browser(ua),
	}
	s.mu.Lock()
	if len(s.pending) < maxPending {
		s.pending = append(s.pending, h)
	}
	n := len(s.pending)
	s.mu.Unlock()
	if n >= flushAt {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// Purge deletes page views older than the retention period.
func (s *Service) Purge(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM hits WHERE day < $1`, time.Now().UTC().Add(-Retention).Format("2006-01-02"))
	return err
}

// DeleteAll removes every recorded page view.
func (s *Service) DeleteAll(ctx context.Context) error {
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
	_, err := s.pool.Exec(ctx, `TRUNCATE hits`)
	return err
}
