package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr          string
	DatabaseURL   string
	MediaDir      string
	SecureCookies bool
	TrustProxy    bool // read client IP from the rightmost X-Forwarded-For entry (set by Caddy)
	// InternalAddr is a second listener for the reverse proxy only (Caddy's TLS "ask").
	// It must never be published to the internet. Empty disables it.
	InternalAddr string
	// SetupToken, if set, is required on the first-run setup page.
	SetupToken string
	// SMTP lets the contact form email you a copy of each message. All optional: without
	// SMTP_HOST and SMTP_FROM, messages are only kept in the admin inbox.
	SMTPHost, SMTPPort, SMTPUser, SMTPPassword, SMTPFrom string
	// BackupDir is where saved backups are kept. It must be a different folder from MediaDir (which is public).
	// Empty means a "backups" folder next to the media folder.
	BackupDir string
	// BackupKeep is how many scheduled (daily) backups to keep; 0 turns scheduled backups off.
	BackupKeep int
	// BackupPersonal puts the subscriber list and contact messages in scheduled backups.
	BackupPersonal bool

	// Platform turns on the multi-website platform (PLATFORM=true): customers each have one website, edit it here, and
	// publish it as static files to their own hosting.
	Platform bool
	// DataDir holds every website's media, backups and builds (platform only).
	DataDir string
	// BaseURL is this server's public address, such as https://builder.example.com (platform only). Emailed links and
	// the forms on published sites point here.
	BaseURL string
	// MaxRuntimes is how many websites are kept loaded in memory at once (platform only).
	MaxRuntimes int
	// AllowPrivateHosts lets websites be published to servers on private networks. Off in production, because it would let a
	// customer make the server connect to its own network; it is for development and tests (ALLOW_PRIVATE_HOSTS=true).
	AllowPrivateHosts bool
	// QuotaStorageMB, QuotaPages and QuotaPosts are what a website may hold unless its own limits say otherwise (platform only;
	// 0 means no limit). Storage is the pictures and fonts a website has uploaded.
	QuotaStorageMB, QuotaPages, QuotaPosts int
	// SecretKey encrypts the hosting passwords the platform stores (32 bytes as 64 hex characters).
	SecretKey string
}

func Load() (Config, error) {
	c := Config{
		Addr:              env("ADDR", ":8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		MediaDir:          env("MEDIA_DIR", "./media"),
		SecureCookies:     os.Getenv("SECURE_COOKIES") == "true",
		TrustProxy:        os.Getenv("TRUST_PROXY") == "true",
		InternalAddr:      os.Getenv("INTERNAL_ADDR"),
		SetupToken:        os.Getenv("SETUP_TOKEN"),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPPort:          env("SMTP_PORT", "587"),
		SMTPUser:          os.Getenv("SMTP_USER"),
		SMTPPassword:      os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:          os.Getenv("SMTP_FROM"),
		BackupDir:         env("BACKUP_DIR", "./backups"),
		BackupKeep:        envInt("BACKUP_KEEP", 7),
		BackupPersonal:    os.Getenv("BACKUP_PERSONAL") == "true",
		Platform:          os.Getenv("PLATFORM") == "true",
		DataDir:           env("DATA_DIR", "./data"),
		BaseURL:           strings.TrimRight(os.Getenv("BASE_URL"), "/"),
		MaxRuntimes:       max(envInt("MAX_RUNTIMES", 40), 1),
		SecretKey:         os.Getenv("SECRET_KEY"),
		QuotaStorageMB:    envInt("QUOTA_STORAGE_MB", 1000),
		QuotaPages:        envInt("QUOTA_PAGES", 200),
		QuotaPosts:        envInt("QUOTA_POSTS", 1000),
		AllowPrivateHosts: os.Getenv("ALLOW_PRIVATE_HOSTS") == "true",
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	return c, nil
}

// envInt reads a whole number of at least 0; anything else gives def.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
