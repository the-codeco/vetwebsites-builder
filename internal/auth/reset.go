package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ResetTTL is how long a password reset link works.
const ResetTTL = time.Hour

// UserEmail returns a user's email address.
func (s *Store) UserEmail(ctx context.Context, id int64) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return email, err
}

// CreateReset makes a one-time link token for a user and returns it (only its hash is stored). A user has one
// link at a time: asking again replaces the earlier one, which stops working.
func (s *Store) CreateReset(ctx context.Context, userID int64) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO password_resets (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, expires_at = EXCLUDED.expires_at, created_at = now()`,
		hash, userID, time.Now().Add(ResetTTL))
	return token, err
}

// ResetEmail says whose password a link would change, or ErrInvalidToken if it is unknown, used or expired.
func (s *Store) ResetEmail(ctx context.Context, token string) (string, error) {
	var email string
	err := s.pool.QueryRow(ctx, `
		SELECT u.email FROM password_resets r JOIN users u ON u.id = r.user_id
		WHERE r.token_hash = $1 AND r.expires_at > now()`, hashToken(token)).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidToken
	}
	return email, err
}

// CompleteReset uses the link: it sets the new password, ends every session the user has (whoever was signed in
// with the old password is signed out) and returns the user. A link works once.
func (s *Store) CompleteReset(ctx context.Context, token, passwordHash string) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	var userID int64
	err = tx.QueryRow(ctx, `DELETE FROM password_resets WHERE token_hash = $1 AND expires_at > now() RETURNING user_id`, hashToken(token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidToken
	}
	if err != nil {
		return User{}, err
	}
	var u User
	if err := tx.QueryRow(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1 RETURNING id, email, role, created_at`, userID, passwordHash).
		Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt); err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return User{}, err
	}
	return u, tx.Commit(ctx)
}

// CreateResetFor is CreateReset with its own lifetime, for the link given to a new customer to choose a first password.
func (s *Store) CreateResetFor(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO password_resets (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, expires_at = EXCLUDED.expires_at, created_at = now()`,
		hash, userID, time.Now().Add(ttl))
	return token, err
}
