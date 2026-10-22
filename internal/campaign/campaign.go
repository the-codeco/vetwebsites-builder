// Package campaign sends newsletters to the people on the subscriber list.
//
// A newsletter is written as a draft. Sending it takes a snapshot of the confirmed subscribers into a queue
// (one row per person), and a worker sends from the queue one message at a time, at a gentle rate, retrying
// temporary failures. The queue lives in the database, so a restart carries on where it stopped, and someone who
// unsubscribes in the middle simply drops out of it. Every message carries that person's own unsubscribe link.
package campaign

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"webbuilder/internal/mailer"
	"webbuilder/internal/newsletter"
)

const (
	MaxSubject = 150
	MaxBody    = 20000

	// MaxAttempts is how many times one message is tried before it is given up on.
	MaxAttempts = 3

	statusDraft    = "draft"
	statusSending  = "sending"
	statusSent     = "sent"
	statusCanceled = "canceled"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrNotDraft   = errors.New("this newsletter has already been sent")
	ErrSending    = errors.New("this newsletter is being sent; cancel it first")
	ErrNoMail     = errors.New("email is not set up")
	ErrNoReceiver = errors.New("there is nobody to send it to")
)

// ErrListChanged is returned when the list has a different number of people than the one the owner confirmed.
type ErrListChanged struct{ Now int }

func (e ErrListChanged) Error() string {
	return fmt.Sprintf("the list changed while you were reading: it now has %d subscribers", e.Now)
}

type Campaign struct {
	ID                   int64
	Subject, Body        string
	Status               string
	CreatedAt, UpdatedAt time.Time
	StartedAt            *time.Time
	FinishedAt           *time.Time
	Total                int
	BaseURL, SiteTitle   string
	Progress
}

func (c Campaign) Draft() bool    { return c.Status == statusDraft }
func (c Campaign) Sending() bool  { return c.Status == statusSending }
func (c Campaign) Finished() bool { return c.Status == statusSent || c.Status == statusCanceled }

// Progress counts a newsletter's messages.
type Progress struct{ Sent, Failed, Pending int }

// Done is how many messages have been dealt with, successfully or not.
func (p Progress) Done() int { return p.Sent + p.Failed }

type Failure struct{ Email, Error string }

type Service struct {
	pool *pgxpool.Pool
	Mail mailer.Mailer
	// Interval is the pause after each message, which keeps the sending rate gentle.
	Interval time.Duration
	// Lease is how long a message being sent is reserved before another attempt may pick it up (a crash mid-send).
	Lease time.Duration
	// RetryStep is how long to wait before the second attempt; the third waits twice as long.
	RetryStep time.Duration
}

func New(pool *pgxpool.Pool, m mailer.Mailer) *Service {
	return &Service{pool: pool, Mail: m, Interval: 500 * time.Millisecond, Lease: 5 * time.Minute, RetryStep: 10 * time.Minute}
}

// Clean checks a subject and body before they are saved. It returns the cleaned values, or a message saying what to fix.
func Clean(subject, body string) (string, string, string) {
	subject = mailer.CleanLine(subject)
	body = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n"))
	body = strings.TrimSpace(body)
	switch {
	case subject == "":
		return "", "", "Give the newsletter a subject."
	case utf8.RuneCountInString(subject) > MaxSubject:
		return "", "", fmt.Sprintf("The subject can be at most %d characters.", MaxSubject)
	case body == "":
		return "", "", "Write something to send."
	case utf8.RuneCountInString(body) > MaxBody:
		return "", "", fmt.Sprintf("The newsletter can be at most %d characters.", MaxBody)
	}
	return subject, body, ""
}

// Footer is added under every newsletter: why the person is getting it, and how to stop.
func Footer(siteTitle, unsubscribeURL string) string {
	return "-- \nYou are receiving this because you subscribed to " + mailer.CleanLine(siteTitle) + ".\nTo unsubscribe: " + unsubscribeURL + "\n"
}

// Compose builds what is sent to one person: the subject, the text with its footer, and the headers that let a
// mail program show an unsubscribe button.
func Compose(c Campaign, token string) (subject, body string, headers map[string]string) {
	link := strings.TrimRight(c.BaseURL, "/") + "/unsubscribe/" + token
	return c.Subject, c.Body + "\n\n" + Footer(c.SiteTitle, link), map[string]string{
		"List-Unsubscribe":      "<" + link + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

const campaignCols = `c.id, c.subject, c.body, c.status, c.created_at, c.updated_at, c.started_at, c.finished_at, c.total, c.base_url, c.site_title,
	(SELECT count(*) FROM deliveries d WHERE d.campaign_id = c.id AND d.status = 'sent'),
	(SELECT count(*) FROM deliveries d WHERE d.campaign_id = c.id AND d.status = 'failed'),
	(SELECT count(*) FROM deliveries d WHERE d.campaign_id = c.id AND d.status = 'pending')`

func scan(row pgx.Row) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.Subject, &c.Body, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.StartedAt, &c.FinishedAt, &c.Total,
		&c.BaseURL, &c.SiteTitle, &c.Sent, &c.Failed, &c.Pending)
	return c, err
}

func (s *Service) Create(ctx context.Context, userID int64, subject, body string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO campaigns (subject, body, created_by) VALUES ($1, $2, NULLIF($3, 0)) RETURNING id`,
		subject, body, userID).Scan(&id)
	return id, err
}

// Update changes a draft. A newsletter that has started sending can't be changed.
func (s *Service) Update(ctx context.Context, id int64, subject, body string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE campaigns SET subject = $2, body = $3, updated_at = now() WHERE id = $1 AND status = 'draft'`, id, subject, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.whyNot(ctx, id)
	}
	return nil
}

// whyNot explains an update that changed nothing: the newsletter is missing, or no longer a draft.
func (s *Service) whyNot(ctx context.Context, id int64) error {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == statusSending {
		return ErrSending
	}
	return ErrNotDraft
}

func (s *Service) Get(ctx context.Context, id int64) (Campaign, error) {
	c, err := scan(s.pool.QueryRow(ctx, `SELECT `+campaignCols+` FROM campaigns c WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	return c, err
}

// List returns the newest newsletters first.
func (s *Service) List(ctx context.Context) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+campaignCols+` FROM campaigns c ORDER BY c.id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a newsletter and its record of who it went to. One being sent must be canceled first.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM campaigns WHERE id = $1 AND status <> 'sending'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return s.whyNot(ctx, id)
	}
	return nil
}

// Recipients is how many people a newsletter sent now would go to.
func (s *Service) Recipients(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM subscribers WHERE confirmed_at IS NOT NULL`).Scan(&n)
	return n, err
}

// Start queues a draft for everyone confirmed on the list and begins sending it. expect is the number of people
// the owner was shown: if the list has changed since, nothing is sent and ErrListChanged says by how much.
func (s *Service) Start(ctx context.Context, id int64, baseURL, siteTitle string, expect int) (int, error) {
	if !s.Mail.Configured() {
		return 0, ErrNoMail
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE campaigns SET status = 'sending', started_at = now(), updated_at = now(), base_url = $2, site_title = $3
		WHERE id = $1 AND status = 'draft'`, id, baseURL, siteTitle)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		tx.Rollback(ctx)
		return 0, s.whyNot(ctx, id)
	}
	tag, err = tx.Exec(ctx, `INSERT INTO deliveries (campaign_id, subscriber_id) SELECT $1, id FROM subscribers WHERE confirmed_at IS NOT NULL`, id)
	if err != nil {
		return 0, err
	}
	n := int(tag.RowsAffected())
	switch {
	case n == 0:
		return 0, ErrNoReceiver
	case n != expect:
		return 0, ErrListChanged{n}
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET total = $2 WHERE id = $1`, id, n); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// Cancel stops a newsletter that is being sent. Messages already sent stay sent; the rest are not sent.
func (s *Service) Cancel(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE campaigns SET status = 'canceled', finished_at = now(), updated_at = now() WHERE id = $1 AND status = 'sending'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM campaigns WHERE id = $1`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// Failures lists the people a newsletter could not be delivered to, and why.
func (s *Service) Failures(ctx context.Context, id int64) ([]Failure, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.email, d.last_error FROM deliveries d JOIN subscribers s ON s.id = d.subscriber_id
		WHERE d.campaign_id = $1 AND d.status = 'failed' ORDER BY s.email LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Failure
	for rows.Next() {
		var f Failure
		if err := rows.Scan(&f.Email, &f.Error); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SendTest sends a newsletter as it will look, to one address, with a note where the unsubscribe link goes.
func (s *Service) SendTest(to, subject, body, siteTitle string) error {
	if !s.Mail.Configured() {
		return ErrNoMail
	}
	text := body + "\n\n-- \nThis is a test. In the real newsletter, each person's own unsubscribe link goes here, under a line saying they subscribed to " +
		mailer.CleanLine(siteTitle) + ".\n"
	return s.Mail.Send(to, "", "[Test] "+subject, text)
}

// Tick sends at most one queued message. It reports whether it had one to send.
func (s *Service) Tick(ctx context.Context) (bool, error) {
	if !s.Mail.Configured() {
		return false, nil
	}
	if err := s.finishDone(ctx); err != nil {
		return false, err
	}
	var campaignID, subscriberID int64
	var attempts int
	err := s.pool.QueryRow(ctx, `
		WITH next AS (
			SELECT d.campaign_id, d.subscriber_id FROM deliveries d JOIN campaigns c ON c.id = d.campaign_id
			WHERE d.status = 'pending' AND d.next_attempt <= now() AND c.status = 'sending'
			ORDER BY d.next_attempt, d.campaign_id, d.subscriber_id LIMIT 1
			FOR UPDATE OF d SKIP LOCKED)
		UPDATE deliveries d SET attempts = d.attempts + 1, next_attempt = now() + make_interval(secs => $1)
		FROM next WHERE d.campaign_id = next.campaign_id AND d.subscriber_id = next.subscriber_id
		RETURNING d.campaign_id, d.subscriber_id, d.attempts`, s.Lease.Seconds()).Scan(&campaignID, &subscriberID, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var to, token string
	err = s.pool.QueryRow(ctx, `UPDATE subscribers SET unsub_token = COALESCE(unsub_token, $2) WHERE id = $1 RETURNING email, unsub_token`,
		subscriberID, newsletter.NewToken()).Scan(&to, &token)
	if errors.Is(err, pgx.ErrNoRows) { // they unsubscribed a moment ago, which also removed this row
		return true, nil
	}
	if err != nil {
		return true, err
	}
	c, err := s.Get(ctx, campaignID)
	if err != nil {
		return true, err
	}
	subject, body, headers := Compose(c, token)
	sendErr := s.Mail.SendWith(to, "", subject, body, headers)
	if err := s.record(ctx, campaignID, subscriberID, attempts, sendErr); err != nil {
		return true, err
	}
	return true, s.finishDone(ctx)
}

// record stores how a send went: sent, given up on, or to be tried again later.
func (s *Service) record(ctx context.Context, campaignID, subscriberID int64, attempts int, sendErr error) error {
	var err error
	switch {
	case sendErr == nil:
		_, err = s.pool.Exec(ctx, `UPDATE deliveries SET status = 'sent', sent_at = now(), last_error = '' WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, subscriberID)
	case mailer.Permanent(sendErr) || attempts >= MaxAttempts:
		_, err = s.pool.Exec(ctx, `UPDATE deliveries SET status = 'failed', last_error = $3 WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, subscriberID, reason(sendErr))
	default:
		_, err = s.pool.Exec(ctx, `UPDATE deliveries SET last_error = $3, next_attempt = now() + make_interval(secs => $4)
			WHERE campaign_id = $1 AND subscriber_id = $2`, campaignID, subscriberID, reason(sendErr), (s.RetryStep * time.Duration(attempts)).Seconds())
	}
	return err
}

// reason is what is kept of an error: one short line.
func reason(err error) string {
	s := mailer.CleanLine(err.Error())
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// finishDone marks newsletters that have nothing left to send as sent.
func (s *Service) finishDone(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE campaigns c SET status = 'sent', finished_at = now(), updated_at = now()
		WHERE c.status = 'sending' AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.campaign_id = c.id AND d.status = 'pending')`)
	return err
}

// Run sends queued newsletters until ctx ends: one message, a pause, the next.
func (s *Service) Run(ctx context.Context, logf func(string, ...any)) {
	for {
		worked, err := s.Tick(ctx)
		if err != nil && ctx.Err() == nil {
			logf("campaign: %v", err)
		}
		wait := s.Interval
		if !worked {
			wait = max(s.Interval, 3*time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
