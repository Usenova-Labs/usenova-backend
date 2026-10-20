package agent

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

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
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
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

// Migrate applies the embedded agent registry schema. It is idempotent
// (CREATE TABLE IF NOT EXISTS), so calling it on an already-bootstrapped
// database is a no-op.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("apply agent registry schema: %w", err)
	}
	return nil
}
