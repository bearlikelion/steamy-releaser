package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateUnityProjectRootRequiresProjectMarkers(t *testing.T) {
	for _, test := range []struct {
		name       string
		assets     bool
		version    bool
		versionDir bool
		wantError  bool
	}{
		{name: "valid", assets: true, version: true},
		{name: "missing Assets", version: true, wantError: true},
		{name: "missing version", assets: true, wantError: true},
		{name: "version is a directory", assets: true, versionDir: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.assets {
				if err := os.Mkdir(filepath.Join(root, "Assets"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			version := filepath.Join(root, "ProjectSettings", "ProjectVersion.txt")
			if err := os.MkdirAll(filepath.Dir(version), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.version {
				if err := os.WriteFile(version, []byte("m_EditorVersion: 6000.3.21f1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if test.versionDir {
				if err := os.Mkdir(version, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateUnityProjectRoot(root)
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateUnityProjectRoot = %v, want error: %v", err, test.wantError)
			}
		})
	}
}

func TestValidateUnityProjectRootDoesNotSuggestAmbiguousChildren(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Game", "OtherGame"} {
		for _, folder := range []string{"Assets", "ProjectSettings"} {
			if err := os.MkdirAll(filepath.Join(root, name, folder), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, name, "ProjectSettings", "ProjectVersion.txt"), []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := ValidateUnityProjectRoot(root)
	if err == nil || strings.Contains(err.Error(), "Unity project found at") {
		t.Fatalf("ambiguous root must fail without choosing a child: %v", err)
	}
}
