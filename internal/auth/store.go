package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	RoleAdmin    = "admin"
	RoleEditor   = "editor"
	RoleCustomer = "customer" // a platform account that owns one website

	SessionTTL = 14 * 24 * time.Hour
	InviteTTL  = 7 * 24 * time.Hour
)

var (
	ErrSetupDone    = errors.New("setup already completed")
	ErrEmailTaken   = errors.New("email already registered")
	ErrNotFound     = errors.New("not found")
	ErrInvalidToken = errors.New("invalid or expired token")
)

type User struct {
	ID        int64
	Email     string
	Role      string
	CreatedAt time.Time
}

// Identity is the authenticated user plus their session's CSRF token.
type Identity struct {
	UserID int64
	Email  string
	Role   string
	CSRF   string

	// ActingSiteID is the website a platform admin is working on (0 when none). ImpersonatedBy and SiteName are filled in
	// by the platform for the website's own admin screens: who is really at the keyboard, and whose site it is.
	ActingSiteID   int64
	ImpersonatedBy string
	SiteName       string
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func newToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM users LIMIT 1) t`).Scan(&n)
	return n > 0, err
}

// CreateFirstAdmin creates the initial admin, serialised so only one caller can win.
func (s *Store) CreateFirstAdmin(ctx context.Context, email, passwordHash string) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7001)`); err != nil {
		return User{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrSetupDone
	}
	u := User{Email: email, Role: RoleAdmin}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'admin') RETURNING id, created_at`,
		email, passwordHash).Scan(&u.ID, &u.CreatedAt); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, string, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, role, created_at, password_hash FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return u, hash, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, email, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateSession(ctx context.Context, userID int64) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	csrf, _, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, userID, csrf, time.Now().Add(SessionTTL))
	return token, err
}

func (s *Store) LookupSession(ctx context.Context, token string) (*Identity, error) {
	var id Identity
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.role, s.csrf_token
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, hashToken(token)).
		Scan(&id.UserID, &id.Email, &id.Role, &id.CSRF)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &id, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

func (s *Store) PurgeExpired(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM invites WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM password_resets WHERE expires_at < now()`)
	return err
}

// CreateInvite returns the raw token; only its hash is stored.
func (s *Store) CreateInvite(ctx context.Context, email, role string, invitedBy int64) (string, error) {
	var taken bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, email).Scan(&taken); err != nil {
		return "", err
	}
	if taken {
		return "", ErrEmailTaken
	}
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO invites (token_hash, email, role, invited_by, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		hash, email, role, invitedBy, time.Now().Add(InviteTTL))
	return token, err
}

func (s *Store) InviteEmail(ctx context.Context, token string) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx,
		`SELECT email FROM invites WHERE token_hash = $1 AND expires_at > now()`, hashToken(token)).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidToken
	}
	return email, err
}

// AcceptInvite consumes the invite and creates the user atomically.
func (s *Store) AcceptInvite(ctx context.Context, token, passwordHash string) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	u := User{}
	err = tx.QueryRow(ctx,
		`DELETE FROM invites WHERE token_hash = $1 AND expires_at > now() RETURNING email, role`,
		hashToken(token)).Scan(&u.Email, &u.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidToken
	}
	if err != nil {
		return User{}, err
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id, created_at`,
		u.Email, passwordHash, u.Role).Scan(&u.ID, &u.CreatedAt)
	if isUnique(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

var ErrLastAdmin = errors.New("cannot remove the last admin")

// PasswordHash returns the stored hash for a user.
func (s *Store) PasswordHash(ctx context.Context, userID int64) (string, error) {
	var h string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, err
}

// SetPassword changes a user's password and ends every session except the one
// identified by keepToken (the caller's own), so a stolen session doesn't survive a
// password change.
func (s *Store) SetPassword(ctx context.Context, userID int64, passwordHash, keepToken string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, passwordHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`,
		userID, hashToken(keepToken)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteUser removes a user (their sessions go with them). The last remaining
// admin can't be removed: the site would be left with nobody able to manage it.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// serialise with other removals so two admins can't remove each other at once
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7002)`); err != nil {
		return err
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if role == RoleAdmin {
		var admins int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
