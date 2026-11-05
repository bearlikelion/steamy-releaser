package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/localstore"
)

func TestExistingCredentialFileSurvivesRestartWithoutModification(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(directory, "config"))
	path := filepath.Join(directory, "credentials.json")
	want := FileCredentials{Username: "build_account", Password: "private password $ ` \\"}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o400); err != nil {
		t.Fatal(err)
	}
	config := CredentialFileConfig{Enabled: true, Path: path}
	if err := SaveCredentialFileConfig(config); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadCredentialFileConfig()
	if err != nil || restored != config {
		t.Fatalf("credential settings were not restored: %v", err)
	}
	got, err := ReadCredentialFile(restored.Path)
	if err != nil || got != want {
		t.Fatal("credentials were not restored")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o400 {
		t.Fatalf("credential file permissions = %o", info.Mode().Perm())
	}
	db, err := localstore.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stored string
	if err := db.QueryRow("SELECT value FROM settings WHERE key = ?", credentialFileKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal([]byte(stored), &metadata) != nil || len(metadata) != 2 || metadata["password"] != nil {
		t.Fatal("password was stored in SQLite")
	}
	config.Enabled = false
	if err := SaveCredentialFileConfig(config); err != nil {
		t.Fatal(err)
	}
	got, err = ReadCredentialFile(path)
	if err != nil || got != want {
		t.Fatal("disabling removed the credential file")
	}
	storedFile, err := os.ReadFile(path)
	if err != nil || string(storedFile) != string(data) {
		t.Fatal("existing credential file was modified")
	}
}

func TestCredentialFileRejectsUnrelatedAndInvalidFilesWithoutExposingContents(t *testing.T) {
	for _, data := range []string{
		`{"username":"","password":""}`, `{"username":"builder","password":""}`,
		`{"project":"keep me"}`, `{"username":"builder","password":"secret","other":1}`,
		`{"username":"builder","password":"secret`, `{"username":"bad name","password":"secret"}`,
		`{"username":null,"password":null}`, `{"username":"builder","password":"line\nsecret"}`,
		strings.Repeat("x", (16<<10)+1),
	} {
		path := filepath.Join(t.TempDir(), "existing.json")
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ReadCredentialFile(path)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid credential file was accepted or its contents exposed")
		}
		got, _ := os.ReadFile(path)
		if string(got) != data {
			t.Fatal("unrelated file was changed")
		}
	}
	if _, err := ReadCredentialFile(t.TempDir()); err == nil {
		t.Fatal("accepted a directory")
	}
}

func TestCredentialFileRejectsMissingFilesAndSymlinks(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "credentials.json")
	if _, err := ReadCredentialFile(path); !os.IsNotExist(err) {
		t.Fatal("accepted a missing credential file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created a missing credential file")
	}
	if err := os.WriteFile(path, []byte(`{"username":"builder","password":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlinks unavailable on this platform")
	}
	if _, err := ReadCredentialFile(link); err == nil {
		t.Fatal("accepted a symlink")
	}
}
