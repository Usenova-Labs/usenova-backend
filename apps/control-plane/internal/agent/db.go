package agent

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// schemaDDL is the bootstrap schema for the agent registry. It is applied
// idempotently on every startup, so a fresh checkout or an existing database
// both converge on the same schema without a separate migration step.
//
//go:embed schema.sql
var schemaDDL string

const sqliteDriver = "sqlite"

// OpenSQLite opens (creating the parent directory and file if needed) the
// SQLite database used to persist the agent registry, applies the bootstrap
// schema and returns a ready-to-use handle.
func OpenSQLite(path string) (*sql.DB, error) {
	// Owner-only directory: the database stores plaintext session tokens and
	// must not be readable by other local users.
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	if err := ensurePrivateFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}

	// SQLite has a single writer; serialize access through one connection so
	// concurrent requests never hit SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite database %q: %w", path, err)
	}

	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// ensurePrivateFile creates the database file if it does not exist and forces
// owner-only permissions on it. SQLite creates new files with 0666 & ~umask
// (commonly 0644), which would expose the stored session tokens to any local
// user, so the file is pre-created with 0600 and re-tightened on every start
// to also cover files created earlier with looser permissions. In-memory
// databases are skipped.
func ensurePrivateFile(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close database file: %w", err)
	}

	// OpenFile only applies 0600 when creating the file; chmod also tightens
	// pre-existing files.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict database file permissions: %w", err)
	}
	return nil
}

// Migrate applies the embedded agent registry schema. It is idempotent
// (CREATE TABLE IF NOT EXISTS), so calling it on an already-bootstrapped
// database is a no-op.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("apply agent registry schema: %w", err)
	}
	return nil
}
