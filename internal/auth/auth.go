package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/thornity/steam-release-tool/internal/localstore"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("Steam username must contain 1-64 letters, numbers, or underscores")
	}
	return nil
}

func ResolveUsername(provided string) (string, error) {
	username := strings.TrimSpace(provided)
	if username == "" {
		username = strings.TrimSpace(os.Getenv("STEAM_USERNAME"))
	}
	if username == "" {
		username, _ = LoadUsername()
	}
	if username == "" {
		return "", errors.New("no Steam username was supplied or cached")
	}
	if err := ValidateUsername(username); err != nil {
		return "", err
	}
	return username, nil
}

const usernameKey = "steam_username"

func LoadUsername() (string, error) {
	db, err := localstore.Open()
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := localstore.Migrate(db, "username", func(tx *sql.Tx) error {
		directory, err := localstore.Directory()
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(filepath.Join(directory, "username"))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		username := strings.TrimSpace(string(contents))
		if err := ValidateUsername(username); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING", usernameKey, username)
		return err
	}); err != nil {
		return "", err
	}
	var username string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", usernameKey).Scan(&username); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", os.ErrNotExist
		}
		return "", err
	}
	if err := ValidateUsername(username); err != nil {
		return "", err
	}
	return username, nil
}

func CacheUsername(username string) error {
	if err := ValidateUsername(username); err != nil {
		return err
	}
	return updateUsername(username)
}

func ClearUsername() error {
	if err := updateUsername(""); err != nil {
		return err
	}
	directory, err := localstore.Directory()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(directory, "username"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func updateUsername(username string) error {
	db, err := localstore.Open()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if username == "" {
		_, err = tx.Exec("DELETE FROM settings WHERE key = ?", usernameKey)
	} else {
		_, err = tx.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, usernameKey, username)
	}
	if err != nil {
		return err
	}
	// Explicit account changes supersede the legacy cache, including logout.
	if _, err := tx.Exec("INSERT INTO migrations(name) VALUES ('username') ON CONFLICT(name) DO NOTHING"); err != nil {
		return err
	}
	return tx.Commit()
}
