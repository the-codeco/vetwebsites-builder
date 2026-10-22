package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// The platform keeps its accounts and sessions in the same tables as a single website, plus one more column on a
// session: the website a platform admin is acting on. These are the calls that use it.

// LookupPlatformSession is LookupSession that also reads the website the session is acting on.
func (s *Store) LookupPlatformSession(ctx context.Context, token string) (*Identity, error) {
	var id Identity
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.role, s.csrf_token, COALESCE(s.acting_site_id, 0)
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, hashToken(token)).
		Scan(&id.UserID, &id.Email, &id.Role, &id.CSRF, &id.ActingSiteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &id, err
}

// SetActingSite records that the session is working on a website (0 stops).
func (s *Store) SetActingSite(ctx context.Context, token string, siteID int64) error {
	var v any
	if siteID > 0 {
		v = siteID
	}
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET acting_site_id = $2 WHERE token_hash = $1`, hashToken(token), v)
	return err
}

// CreateUser adds an account with the given role and password hash.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (User, error) {
	u := User{Email: email, Role: role}
	err := s.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id, created_at`,
		email, passwordHash, role).Scan(&u.ID, &u.CreatedAt)
	if isUnique(err) {
		return User{}, ErrEmailTaken
	}
	return u, err
}

// SetPasswordOnly changes a password without touching sessions (the caller decides which to end).
func (s *Store) SetPasswordOnly(ctx context.Context, userID int64, passwordHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, passwordHash)
	return err
}

// EndSessionsOf signs a user out everywhere.
func (s *Store) EndSessionsOf(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}
