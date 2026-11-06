package steam

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const accountsFixture = `"InstallConfigStore"
{
	// comment with "quotes" {
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"ConnectCache"
				{
					"9fe08d9d1"		"secret-token-value"
				}
				"Accounts"
				{
					"builder_one"
					{
						"SteamID"		"76561190000000001"
					}
					"Builder2" [$WIN32]
					{
						"SteamID"		"76561190000000002"
					}
					"bad name!"
					{
					}
				}
				"Password"		"hunter2"
			}
		}
	}
}
`

func TestCachedAccountNamesReturnsOnlyAccountKeys(t *testing.T) {
	names, err := CachedAccountNames([]byte(accountsFixture))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"builder_one", "Builder2"}) {
		t.Fatalf("names = %q", names)
	}
	for _, name := range names {
		for _, secret := range []string{"secret-token-value", "hunter2", "7656119", "9fe08d9d1"} {
			if strings.Contains(name, secret) {
				t.Fatalf("leaked %q in %q", secret, name)
			}
		}
	}
}

func TestCachedAccountNamesIgnoresAccountsOutsideSteamPath(t *testing.T) {
	names, err := CachedAccountNames([]byte(`"Other" { "Accounts" { "nope" { } } } "Accounts" { "also_nope" { } }`))
	if err != nil || len(names) != 0 {
		t.Fatalf("names = %q, err = %v", names, err)
	}
}

func TestCachedAccountNamesSurvivesMalformedInput(t *testing.T) {
	truncated := `"InstallConfigStore" { "Software" { "Valve" { "Steam" { "Accounts" { "ok_name" { } "broken`
	for _, input := range []string{
		"",
		"}}}}{{{{",
		truncated,
		`"a" [$X`,
		"\x00\xff\"{",
		strings.Repeat("{", 10000),
		`"InstallConfigStore" { "Software" { "Valve" { "Steam" { "Accounts" { "{" { } } } } } }`,
	} {
		names, _ := CachedAccountNames([]byte(input))
		for _, name := range names {
			if !accountNamePattern.MatchString(name) {
				t.Fatalf("invalid name %q from %q", name, input)
			}
		}
	}
	names, err := CachedAccountNames([]byte(truncated))
	if err == nil || !slices.Equal(names, []string{"ok_name"}) {
		t.Fatalf("names = %q, err = %v", names, err)
	}
}

func TestSteamCMDCachedAccountsReadsConfigNextToExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "config.vdf"), []byte(accountsFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := SteamCMDCachedAccounts(filepath.Join(dir, "steamcmd.sh"))
	if err != nil || !slices.Equal(names, []string{"Builder2", "builder_one"}) {
		t.Fatalf("names = %q, err = %v", names, err)
	}
	empty, err := SteamCMDCachedAccounts(filepath.Join(t.TempDir(), "steamcmd.sh"))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v, err = %v", empty, err)
	}
}
