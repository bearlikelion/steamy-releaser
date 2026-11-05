package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestDoctorUsesResolvedCustomCommandAndProfile(t *testing.T) {
	directory := t.TempDir()
	steamCMD := filepath.Join(directory, "steamcmd")
	t.Setenv("STEAMCMD_BIN", steamCMD)
	for _, path := range []string{steamCMD, filepath.Join(directory, "builder")} {
		if err := os.WriteFile(path, []byte("fake"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(directory, "steam-release.yaml")
	source := `app_id: 100
targets:
  linux:
    depot_id: 101
    output: builds/linux/Game
    build:
      command: ["{project}/builder", "{output}"]
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(document, "", nil)
	if err != nil || !report.OK() || report.Profile != "release" {
		t.Fatalf("unexpected diagnostics: %#v, %v", report, err)
	}
}
