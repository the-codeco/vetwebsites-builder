// Package formguard keeps bots out of public forms without CAPTCHAs: a signed token that
// proves the form was loaded a few seconds ago, and hourly limits per visitor and overall.
package formguard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"webbuilder/internal/auth"
)

const (
	// A real person needs a few seconds to fill a form in; most bots post it instantly.
	MinFillTime = 3 * time.Second
	MaxFormAge  = 24 * time.Hour
)

// Guard protects one kind of form. Tokens from one guard are useless on another.
type Guard struct {
	purpose string
	secret  []byte
	perIP   *auth.Limiter
	all     *auth.Limiter
}

// New makes a guard allowing perIPPerHour accepted submissions per address and
// allPerHour in total. The secret is random per process: a form loaded before a
// restart is rejected once and the visitor simply reloads it.
func New(purpose string, perIPPerHour, allPerHour int) *Guard {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return NewWithSecret(purpose, secret, perIPPerHour, allPerHour)
}

// NewWithSecret is New with a chosen secret (for tests).
func NewWithSecret(purpose string, secret []byte, perIPPerHour, allPerHour int) *Guard {
	return &Guard{
		purpose: purpose, secret: secret,
		perIP: auth.NewLimiter(perIPPerHour, time.Hour), all: auth.NewLimiter(allPerHour, time.Hour),
	}
}

func (g *Guard) sign(ts string) string {
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(g.purpose + ":" + ts))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Token goes into the form when the page is shown. It proves the form was loaded,
// and when, without any cookie or stored state.
func (g *Guard) Token(now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + "." + g.sign(ts)
}

// CheckToken accepts a token that is genuine, at least a few seconds old, and not stale.
func (g *Guard) CheckToken(tok string, now time.Time) bool {
	ts, sig, ok := strings.Cut(tok, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(g.sign(ts))) {
		return false
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	age := now.Sub(time.Unix(n, 0))
	return age >= MinFillTime && age <= MaxFormAge
}

// Allow records an attempt and reports whether this address (and the site as a
// whole) is still under the hourly limits. Call it once per accepted submission.
func (g *Guard) Allow(ip string) bool {
	if g.perIP.Blocked(ip) || g.all.Blocked("*") {
		return false
	}
	g.perIP.Fail(ip)
	g.all.Fail("*")
	return true
}
