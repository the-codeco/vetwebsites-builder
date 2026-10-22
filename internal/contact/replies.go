package contact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"webbuilder/internal/mailer"
)

const (
	MaxReply = 5000
	// repliesPerUserPerHour keeps a stolen login (or a slip of the mouse) from turning the contact inbox into a mail cannon.
	repliesPerUserPerHour = 30
)

var (
	ErrNoMail  = errors.New("email is not set up")
	ErrTooMany = errors.New("too many replies in the last hour")
)

type Reply struct {
	ID        int64
	Body      string
	By        string // the email of the person who wrote it ("" if their account has been deleted)
	CreatedAt time.Time
}

// CleanReply checks what the owner typed and returns it cleaned, or a message saying what to fix.
func CleanReply(body string) (string, string) {
	body = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"))
	body = strings.TrimSpace(body)
	switch {
	case body == "":
		return "", "Write your reply first."
	case utf8.RuneCountInString(body) > MaxReply:
		return "", fmt.Sprintf("A reply can be at most %d characters.", MaxReply)
	}
	return body, ""
}

// ReplySubject is "Re: " and the message's subject, without piling up "Re: Re:".
func ReplySubject(subject string) string {
	subject = mailer.CleanLine(subject)
	if subject == "" {
		return "Re: your message"
	}
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

// quote puts "> " in front of every line, the way mail programs show what is being replied to.
func quote(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// ReplyBody is the reply followed by the visitor's own message, quoted.
func ReplyBody(reply string, m Message) string {
	return reply + "\n\n-- \nOn " + m.CreatedAt.UTC().Format("2 January 2006, 15:04 MST") + ", " + mailer.CleanLine(m.Name) + " wrote:\n" + quote(m.Body) + "\n"
}

// Reply emails an answer to the person who sent a message and keeps a copy with the message. The visitor's
// replies come back to replyTo (the person answering), not to the site's no-reply address. Nothing is stored if
// the email can't be sent.
func (s *Service) Reply(ctx context.Context, m Message, userID int64, replyTo, body string) error {
	if !s.Mail.Configured() {
		return ErrNoMail
	}
	var recent int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM message_replies WHERE user_id = $1 AND created_at > now() - interval '1 hour'`, userID).Scan(&recent); err != nil {
		return err
	}
	if recent >= repliesPerUserPerHour {
		return ErrTooMany
	}
	if err := s.Mail.Send(m.Email, replyTo, ReplySubject(m.Subject), ReplyBody(body, m)); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO message_replies (message_id, user_id, body) VALUES ($1, NULLIF($2, 0), $3)`, m.ID, userID, body)
	return err
}

// Replies lists the answers sent to a message, oldest first.
func (s *Service) Replies(ctx context.Context, messageID int64) ([]Reply, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.body, COALESCE(u.email, ''), r.created_at FROM message_replies r
		LEFT JOIN users u ON u.id = r.user_id WHERE r.message_id = $1 ORDER BY r.id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reply
	for rows.Next() {
		var r Reply
		if err := rows.Scan(&r.ID, &r.Body, &r.By, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplyCounts says how many answers each message has had, by message id.
func (s *Service) ReplyCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT message_id, count(*) FROM message_replies GROUP BY message_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
