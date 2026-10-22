// Package mailer sends plain-text email through an SMTP server and checks addresses.
package mailer

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
	"unicode"
)

const MaxEmail = 254

// ErrBadRecipient is returned for an address that can't be mailed at all.
var ErrBadRecipient = errors.New("not a valid recipient address")

// Permanent reports whether a failed send will fail again if repeated: the server refused the message or the
// address (a 5xx answer), or the address is not valid. Anything else (a network problem, a busy server, a 4xx
// answer) may work later.
func Permanent(err error) bool {
	var te *textproto.Error
	return errors.Is(err, ErrBadRecipient) || (errors.As(err, &te) && te.Code >= 500)
}

// Mailer sends plain-text email through an SMTP server (STARTTLS is used whenever the
// server offers it). Leave Host empty to turn email off.
type Mailer struct {
	Host, Port, User, Password string
	From                       string // the address mail is sent from, e.g. "Site <noreply@example.com>"
}

// Configured reports whether email can be sent.
func (m Mailer) Configured() bool { return m.Host != "" && m.From != "" }

const smtpTimeout = 15 * time.Second

// ValidEmail accepts a plain address like name@example.com (no display name, no list).
func ValidEmail(s string) bool {
	if s == "" || len(s) > MaxEmail || strings.ContainsAny(s, " <>,;\"\r\n") {
		return false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	return at > 0 && strings.Contains(s[at+1:], ".")
}

// CleanLine trims a single-line value and replaces control characters, so nothing
// typed into a form can inject extra lines into an email header.
func CleanLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Send delivers one message. replyTo may be empty.
func (m Mailer) Send(to, replyTo, subject, body string) error {
	return m.SendWith(to, replyTo, subject, body, nil)
}

// ExtraHeaders are the only extra headers a message may carry: the ones that let a mail program offer
// an "unsubscribe" button. Anything else is dropped.
var ExtraHeaders = map[string]bool{"List-Unsubscribe": true, "List-Unsubscribe-Post": true}

// SendWith is Send with extra headers (see ExtraHeaders).
func (m Mailer) SendWith(to, replyTo, subject, body string, extra map[string]string) error {
	if !m.Configured() {
		return errors.New("email is not configured")
	}
	if !ValidEmail(to) {
		return ErrBadRecipient
	}
	from, err := bareAddress(m.From)
	if err != nil {
		return fmt.Errorf("SMTP_FROM: %w", err)
	}
	port := m.Port
	if port == "" {
		port = "587"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(m.Host, port), smtpTimeout)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(2 * smtpTimeout))
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if m.User != "" {
		// PlainAuth refuses to send the password unless the connection is encrypted (or local)
		if err := c.Auth(smtp.PlainAuth("", m.User, m.Password, m.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(m.composeWith(to, replyTo, subject, body, extra)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func bareAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '<'); i >= 0 && strings.HasSuffix(s, ">") {
		s = s[i+1 : len(s)-1]
	}
	if !ValidEmail(s) {
		return "", errors.New("not a valid email address")
	}
	return s, nil
}

// compose builds the message. Every header value is a single cleaned line, and the
// subject is encoded, so visitor-supplied text can't add headers.
func (m Mailer) compose(to, replyTo, subject, body string) []byte {
	return m.composeWith(to, replyTo, subject, body, nil)
}

func (m Mailer) composeWith(to, replyTo, subject, body string, extra map[string]string) []byte {
	id := make([]byte, 12)
	rand.Read(id)
	host := "localhost"
	if from, err := bareAddress(m.From); err == nil {
		host = from[strings.LastIndexByte(from, '@')+1:]
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + CleanLine(v) + "\r\n") }
	h("From", m.From)
	h("To", to)
	if replyTo != "" {
		h("Reply-To", replyTo)
	}
	h("Subject", mime.QEncoding.Encode("utf-8", CleanLine(subject)))
	h("Date", time.Now().Format(time.RFC1123Z))
	for _, k := range []string{"List-Unsubscribe", "List-Unsubscribe-Post"} {
		if v := extra[k]; v != "" && ExtraHeaders[k] {
			h(k, v)
		}
	}
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+host+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	return []byte(b.String())
}
