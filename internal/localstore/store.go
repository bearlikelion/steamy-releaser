// Package localstore owns the SQLite database for machine-local application data.
// Project configuration remains in YAML.
package localstore

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const Filename = "steam-releaser.db"
const schemaVersion = 2

var createMu sync.Mutex

func Directory() (string, error) {
	if directory := strings.TrimSpace(os.Getenv("STEAM_RELEASER_CONFIG_DIR")); directory != "" {
		return directory, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "steam-releaser"), nil
}

// Open returns a database owned by the caller, who must close it. Transactions
// take the write lock immediately so concurrent CLI and desktop updates wait
// instead of losing changes during a read/modify/write cycle.
func Open() (*sql.DB, error) {
	directory, err := Directory()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(directory, Filename))
	if err != nil {
		return nil, err
	}
	if err := prepareFile(path); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath}).String() + "?_pragma=busy_timeout(30000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("open local database: %w", err)
	}
	return db, nil
}

func prepareFile(path string) error {
	createMu.Lock()
	defer createMu.Unlock()
	// Closing an extra descriptor for an existing database can release SQLite's
	// POSIX locks held by other connections in this process. Only open a file
	// descriptor when creating the database for the first time.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		if err := file.Close(); err != nil {
			return err
		}
	} else if !os.IsExist(err) {
		return err
	}
	return os.Chmod(path, 0o600)
}

func initialize(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == schemaVersion {
		return nil
	}
	if version > schemaVersion {
		return fmt.Errorf("local database schema %d is newer than this application supports", version)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A concurrent opener may have migrated while this connection waited for
	// the write lock. Check again before applying non-idempotent schema changes.
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("local database schema %d is newer than this application supports", version)
	}
	if version == 0 {
		_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS release_history (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			config_path TEXT NOT NULL,
			project_root TEXT NOT NULL,
			payload TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS release_history_project
			ON release_history(config_path, project_root, sequence DESC);
	`)
		if err != nil {
			return err
		}
		version = 1
	}
	if version == 1 {
		if _, err := tx.Exec("ALTER TABLE release_history ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("PRAGMA user_version = 2"); err != nil {
		return err
	}
	return tx.Commit()
}

// Migrate imports a legacy store once, recording completion in the same
// transaction. A failed import rolls back and leaves the original file intact.
func Migrate(db *sql.DB, name string, importData func(*sql.Tx) error) error {
	// Most calls use an already-migrated store. Avoid taking a write lock on
	// every read or save; a first migration is checked again under the lock.
	var done bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM migrations WHERE name = ?)", name).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM migrations WHERE name = ?)", name).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	if err := importData(tx); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO migrations(name) VALUES (?)", name); err != nil {
		return err
	}
	return tx.Commit()
}
