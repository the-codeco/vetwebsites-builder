package db

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"webbuilder/migrations"
	platformmigrations "webbuilder/migrations/platform"
)

// Every website lives in its own PostgreSQL schema named site_<number>. The name is built by the program, never from
// what a person types, and it is checked again here before it is ever put into a statement.
var schemaRe = regexp.MustCompile(`^site_[0-9]{1,9}$`)

// ValidSchema says whether name is a site schema name.
func ValidSchema(name string) bool { return schemaRe.MatchString(name) }

// SchemaName is the schema for a site id.
func SchemaName(siteID int64) string { return fmt.Sprintf("site_%d", siteID) }

// gooseMu serialises migrations: goose keeps its file system and table name in package-level variables.
var gooseMu sync.Mutex

// ConnectSchema opens a small pool whose every connection sees only the given schema, so the whole program, which
// uses plain table names, reads and writes one website's data and cannot reach another's.
func ConnectSchema(ctx context.Context, url, schema string, maxConns int32) (*pgxpool.Pool, error) {
	if !ValidSchema(schema) {
		return nil, fmt.Errorf("not a site schema: %q", schema)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = max(maxConns, 1)
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s: %w", schema, err)
	}
	return pool, nil
}

// CreateSchema makes an empty schema for a new website.
func CreateSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if !ValidSchema(schema) {
		return fmt.Errorf("not a site schema: %q", schema)
	}
	_, err := pool.Exec(ctx, `CREATE SCHEMA `+schema)
	return err
}

// DropSchema deletes a website's schema and everything in it.
func DropSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if !ValidSchema(schema) {
		return fmt.Errorf("not a site schema: %q", schema)
	}
	_, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	return err
}

// SchemaExists says whether a site's schema is there.
func SchemaExists(ctx context.Context, pool *pgxpool.Pool, schema string) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`, schema).Scan(&ok)
	return ok, err
}

// MigratePlatform applies the platform's own migrations (accounts, sites, audit log) in the default schema.
func MigratePlatform(pool *pgxpool.Pool) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	goose.SetBaseFS(platformmigrations.FS)
	goose.SetTableName("goose_platform_version")
	defer goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(sqlDB, ".")
}

// MigrateSite applies the website migrations to a pool made by ConnectSchema, so the tables are made in that schema.
func MigrateSite(pool *pgxpool.Pool) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(sqlDB, ".")
}
