// Package persistence provides the signer-owned durable journal: a private
// SQLite database in WAL mode with strict file permissions (plan §9.2).
package persistence

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DB is the signer journal handle.
type DB struct {
	*sql.DB
	path string
}

var connectPragmas = []string{
	"busy_timeout(5000)",
	"journal_mode(WAL)",
	"foreign_keys(1)",
	"synchronous(FULL)",
}

// Open creates or opens the journal at path, enforcing a 0700 parent directory
// and 0600 database file, applies durability pragmas, and runs migrations.
func Open(path string) (*DB, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("journal path must be absolute: %s", path)
	}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}

	dsn := "file:" + path + "?" + pragmaQuery()
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open signer journal: %w", err)
	}
	// A signer journal is low-throughput; a single connection removes all
	// SQLITE_BUSY contention and keeps write ordering deterministic.
	handle.SetMaxOpenConns(1)

	if err := handle.Ping(); err != nil {
		handle.Close()
		return nil, fmt.Errorf("initialize signer journal: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		handle.Close()
		return nil, fmt.Errorf("restrict signer journal file: %w", err)
	}

	db := &DB{DB: handle, path: path}
	if err := Migrate(db.DB); err != nil {
		handle.Close()
		return nil, err
	}
	return db, nil
}

// Path returns the journal file path.
func (d *DB) Path() string { return d.path }

func pragmaQuery() string {
	parts := make([]string, len(connectPragmas))
	for i, p := range connectPragmas {
		parts[i] = "_pragma=" + p
	}
	return strings.Join(parts, "&")
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create signer journal directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict signer journal directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("inspect signer journal directory: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("signer journal directory permissions must be 0700, got %04o", info.Mode().Perm())
	}
	return nil
}
