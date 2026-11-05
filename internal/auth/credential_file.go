package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thornity/steam-release-tool/internal/localstore"
)

// Only the opt-in and path go into SQLite. The chosen file is read-only.
type CredentialFileConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type FileCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

const credentialFileKey = "steam_credential_file"

func LoadCredentialFileConfig() (CredentialFileConfig, error) {
	db, err := localstore.Open()
	if err != nil {
		return CredentialFileConfig{}, err
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", credentialFileKey).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CredentialFileConfig{}, nil
		}
		return CredentialFileConfig{}, err
	}
	var config CredentialFileConfig
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return config, fmt.Errorf("saved credential file settings are invalid")
	}
	return config, nil
}

func SaveCredentialFileConfig(config CredentialFileConfig) error {
	db, err := localstore.Open()
	if err != nil {
		return err
	}
	defer db.Close()
	value, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", credentialFileKey, string(value))
	return err
}

func validateFileCredentials(credentials FileCredentials) error {
	if ValidateUsername(credentials.Username) != nil || credentials.Password == "" ||
		len(credentials.Password) > 1024 || strings.ContainsAny(credentials.Password, "\r\n\x00") {
		return fmt.Errorf("credential file must contain a valid Steam username and password")
	}
	return nil
}

// ReadCredentialFile rejects links, non-files, oversized data, and unrelated JSON.
// Decode errors never include file contents, which may contain a password.
func ReadCredentialFile(path string) (FileCredentials, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return FileCredentials{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return FileCredentials{}, fmt.Errorf("choose a regular credential file smaller than 16 KB")
	}
	file, err := os.Open(path)
	if err != nil {
		return FileCredentials{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil {
		return FileCredentials{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fields map[string]json.RawMessage
	if len(data) > 16<<10 || json.Unmarshal(data, &fields) != nil || fields["username"] == nil || fields["password"] == nil ||
		bytes.Equal(bytes.TrimSpace(fields["username"]), []byte("null")) || bytes.Equal(bytes.TrimSpace(fields["password"]), []byte("null")) {
		return FileCredentials{}, fmt.Errorf("credential file must be JSON with username and password fields")
	}
	var credentials FileCredentials
	if err := decoder.Decode(&credentials); err != nil {
		return FileCredentials{}, fmt.Errorf("credential file must be JSON with username and password fields")
	}
	return credentials, validateFileCredentials(credentials)
}
