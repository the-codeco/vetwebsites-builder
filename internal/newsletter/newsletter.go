// Package newsletter keeps the list of people who signed up for updates: it collects addresses (confirmed by
// email when possible), handles confirming and unsubscribing, and lets the owner export the list. Sending a
// newsletter to it is the campaign package's job.
package newsletter

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/formguard"
	"webbuilder/internal/mailer"
)

const (
	perIPPerHour = 5
	allPerHour   = 200
	// PendingLifetime is how long an unconfirmed signup is kept before it is deleted.
	PendingLifetime = 14 * 24 * time.Hour
	listLimit       = 5000
)

// TokenRe matches the tokens this package makes (32 random bytes, URL-safe base64).
var TokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Subscriber struct {
	ID          int64
	Email       string
	CreatedAt   time.Time
	ConfirmedAt *time.Time
}

func (s Subscriber) Confirmed() bool { return s.ConfirmedAt != nil }

type Service struct {
	pool *pgxpool.Pool
	// Guard issues the signup form's token and enforces the hourly limits.
	*formguard.Guard
	Mail     mailer.Mailer
	ClientIP func(*http.Request) string
}

func New(pool *pgxpool.Pool, m mailer.Mailer, clientIP func(*http.Request) string) *Service {
	return &Service{pool: pool, Guard: formguard.New("newsletter-form", perIPPerHour, allPerHour), Mail: m, ClientIP: clientIP}
}

// Normalize trims and lower-cases an address and says whether it is acceptable.
func Normalize(email string) (string, bool) {
	e := strings.ToLower(mailer.CleanLine(email))
	return e, mailer.ValidEmail(e)
}

func newToken() (token, hash string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

// NewToken makes a random token for a link (an unsubscribe link in a newsletter).
func NewToken() string {
	token, _ := newToken()
	return token
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Subscribe records a signup and returns the token for the confirmation link, or "" when
// no email should be sent. With needConfirm the address stays pending until its owner
// confirms; without it (no email set up) it is confirmed at once. Signing up an address that
// is already confirmed changes nothing and sends nothing, so the answer never reveals
// whether an address was already on the list.
func (s *Service) Subscribe(ctx context.Context, email string, needConfirm bool) (string, error) {
	token, hash := newToken()
	if !needConfirm {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO subscribers (email, token_hash, confirmed_at) VALUES ($1, $2, now())
			ON CONFLICT (email) DO UPDATE SET confirmed_at = COALESCE(subscribers.confirmed_at, now())`, email, hash)
		return "", err
	}
	// a pending address gets a fresh link; a confirmed one is left alone
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO subscribers (email, token_hash) VALUES ($1, $2)
		ON CONFLICT (email) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = now()
		WHERE subscribers.confirmed_at IS NULL
		RETURNING id`, email, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return token, nil
}

// Confirm marks the owner of the token's address as confirmed.
func (s *Service) Confirm(ctx context.Context, token string) (bool, error) {
	if !TokenRe.MatchString(token) {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE subscribers SET confirmed_at = COALESCE(confirmed_at, now()) WHERE token_hash = $1`, hashToken(token))
	return tag.RowsAffected() == 1, err
}

// Exists reports whether a token belongs to a subscriber (confirmed or pending).
func (s *Service) Exists(ctx context.Context, token string) (bool, error) {
	if !TokenRe.MatchString(token) {
		return false, nil
	}
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM subscribers WHERE token_hash = $1 OR unsub_token = $2)`, hashToken(token), token).Scan(&ok)
	return ok, err
}

// Unsubscribe removes the token's address from the list entirely. The token is either the one from the
// confirmation email or the one in a newsletter's unsubscribe link.
func (s *Service) Unsubscribe(ctx context.Context, token string) (bool, error) {
	if !TokenRe.MatchString(token) {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM subscribers WHERE token_hash = $1 OR unsub_token = $2`, hashToken(token), token)
	return tag.RowsAffected() == 1, err
}

// --- admin ---

func (s *Service) List(ctx context.Context) ([]Subscriber, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, email, created_at, confirmed_at FROM subscribers ORDER BY created_at DESC, id DESC LIMIT $1`, listLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscriber
	for rows.Next() {
		var x Subscriber
		if err := rows.Scan(&x.ID, &x.Email, &x.CreatedAt, &x.ConfirmedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Counts returns how many addresses are confirmed and how many are waiting to confirm.
func (s *Service) Counts(ctx context.Context) (confirmed, pending int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE confirmed_at IS NOT NULL), count(*) FILTER (WHERE confirmed_at IS NULL) FROM subscribers`).Scan(&confirmed, &pending)
	return
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM subscribers WHERE id = $1`, id)
	return err
}

// PurgePending deletes signups that were never confirmed.
func (s *Service) PurgePending(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM subscribers WHERE confirmed_at IS NULL AND created_at < $1`, time.Now().Add(-PendingLifetime))
	return err
}

// --- email ---

func ConfirmSubject(siteTitle string) string { return "Confirm your subscription to " + siteTitle }

func ConfirmBody(siteTitle, confirmURL, unsubscribeURL string) string {
	return "Someone (hopefully you) asked to receive updates from " + siteTitle + " at this address.\n\n" +
		"To confirm, open this link and press the button:\n" + confirmURL + "\n\n" +
		"If you didn't ask for this, ignore this email and you won't hear from us. The request expires in two weeks.\n\n" +
		"You can leave the list at any time with this link:\n" + unsubscribeURL + "\n"
}
