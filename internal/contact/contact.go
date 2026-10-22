// Package contact handles the contact form: validating what a visitor sent, keeping
// spam out, storing messages for the admin inbox, and emailing a copy if mail is set up.
package contact

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/formguard"
	"webbuilder/internal/mailer"
)

const (
	MaxName    = 100
	MaxSubject = 150
	MaxBody    = 5000

	perIPPerHour = 5
	allPerHour   = 100
)

var ErrNotFound = errors.New("message not found")

type Message struct {
	ID                         int64
	Name, Email, Subject, Body string
	Read                       bool
	CreatedAt                  time.Time
}

// Input is what a visitor typed.
type Input struct{ Name, Email, Subject, Body string }

// Validate cleans the input and returns a message for the visitor if it is not acceptable.
func Validate(in Input) (Input, string) {
	in.Name = mailer.CleanLine(in.Name)
	in.Email = mailer.CleanLine(in.Email)
	in.Subject = mailer.CleanLine(in.Subject)
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	// keep newlines and tabs in the body, drop other control characters
	in.Body = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, in.Body)

	switch {
	case in.Name == "":
		return in, "Please enter your name."
	case utf8.RuneCountInString(in.Name) > MaxName:
		return in, fmt.Sprintf("Your name is too long (at most %d characters).", MaxName)
	case !mailer.ValidEmail(in.Email):
		return in, "Please enter a valid email address so we can reply."
	case utf8.RuneCountInString(in.Subject) > MaxSubject:
		return in, fmt.Sprintf("The subject is too long (at most %d characters).", MaxSubject)
	case in.Body == "":
		return in, "Please write a message."
	case utf8.RuneCountInString(in.Body) > MaxBody:
		return in, fmt.Sprintf("The message is too long (at most %d characters).", MaxBody)
	}
	return in, ""
}

type Service struct {
	pool *pgxpool.Pool
	// Guard issues the form's token and enforces the hourly limits (Token, CheckToken, Allow).
	*formguard.Guard
	Mail mailer.Mailer
	// ClientIP says which address a request came from (it honours the trusted-proxy setting).
	ClientIP func(*http.Request) string
}

func New(pool *pgxpool.Pool, m mailer.Mailer, clientIP func(*http.Request) string) *Service {
	return &Service{pool: pool, Guard: formguard.New("contact-form", perIPPerHour, allPerHour), Mail: m, ClientIP: clientIP}
}

// --- storage ---

func (s *Service) Save(ctx context.Context, in Input) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO messages (name, email, subject, body) VALUES ($1, $2, $3, $4) RETURNING id`,
		in.Name, in.Email, in.Subject, in.Body).Scan(&id)
	return id, err
}

func (s *Service) List(ctx context.Context) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, email, subject, body, read, created_at FROM messages ORDER BY created_at DESC, id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.Subject, &m.Body, &m.Read, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id int64) (Message, error) {
	var m Message
	err := s.pool.QueryRow(ctx, `SELECT id, name, email, subject, body, read, created_at FROM messages WHERE id = $1`, id).
		Scan(&m.ID, &m.Name, &m.Email, &m.Subject, &m.Body, &m.Read, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (s *Service) SetRead(ctx context.Context, id int64, read bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE messages SET read = $2 WHERE id = $1`, id, read)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM messages WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Service) Unread(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE NOT read`).Scan(&n)
	return n, err
}

// NotifyEmail is where copies of new messages are sent ("" = nowhere).
func (s *Service) NotifyEmail(ctx context.Context) (string, error) {
	var e string
	err := s.pool.QueryRow(ctx, `SELECT notify_email FROM site_settings WHERE id = 1`).Scan(&e)
	return e, err
}

func (s *Service) SetNotifyEmail(ctx context.Context, e string) error {
	_, err := s.pool.Exec(ctx, `UPDATE site_settings SET notify_email = $1 WHERE id = 1`, e)
	return err
}
