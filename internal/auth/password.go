package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16

	MinPasswordLen = 10
	MaxPasswordLen = 128
)

var b64 = base64.RawStdEncoding

// Each hash uses 64 MB. Without a cap, a flood of login attempts from many
// addresses (each under its own rate limit) could allocate gigabytes at once.
// Excess attempts simply wait their turn.
var hashSlots = make(chan struct{}, 4)

func deriveKey(pw string, salt []byte, t, m uint32, p uint8, keyLen uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(pw), salt, t, m, p, keyLen)
}

func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := deriveKey(pw, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := deriveKey(pw, salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// BurnPasswordCheck spends the same time as a real verification so unknown
// emails can't be told apart from wrong passwords by response time.
func BurnPasswordCheck(pw string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("dummy-password-for-timing") })
	VerifyPassword(pw, dummyHash)
}

func ValidatePassword(pw string) string {
	switch {
	case len(pw) < MinPasswordLen:
		return fmt.Sprintf("Password must be at least %d characters.", MinPasswordLen)
	case len(pw) > MaxPasswordLen:
		return fmt.Sprintf("Password must be at most %d characters.", MaxPasswordLen)
	}
	return ""
}
