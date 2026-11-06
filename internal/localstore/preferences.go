package localstore

import (
	"context"
	"fmt"
	"strings"
)

const SteamCMDAccountsKey = "steam-releaser:steamcmd-accounts"

func preferenceKey(key string) bool {
	switch key {
	case "steam-releaser:last-config", "steam-releaser:recent-configs", "steam-releaser:notifications", SteamCMDAccountsKey:
		return true
	}
	return strings.HasPrefix(key, "steam-releaser:project-root:") ||
		strings.HasPrefix(key, "steam-releaser:selections:")
}

// Preferences imports missing browser preferences without replacing values
// already saved in SQLite, and returns the machine-local desktop preferences.
func Preferences(legacy map[string]string) (map[string]string, error) {
	db, err := Open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for key, value := range legacy {
		if !preferenceKey(key) {
			continue
		}
		if _, err := tx.Exec("INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING", key, value); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query("SELECT key, value FROM settings WHERE key LIKE 'steam-releaser:%'")
	if err != nil {
		return nil, err
	}
	preferences := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return nil, err
		}
		if preferenceKey(key) {
			preferences[key] = value
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return preferences, nil
}

func SavePreferences(values map[string]string) error {
	for key := range values {
		if !preferenceKey(key) {
			return fmt.Errorf("unsupported desktop preference %q", key)
		}
	}
	db, err := Open()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
