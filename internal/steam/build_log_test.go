package steam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/release"
)

type buildLogRunner struct {
	project, console, log string
}

func (r buildLogRunner) Run(_ context.Context, _ release.CommandPlan, _ bool) (release.RunResult, error) {
	directory := filepath.Join(r.project, ".steam-releaser", "output")
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		return release.RunResult{}, fmt.Errorf("find generated output: %v, %d entries", err, len(entries))
	}
	if err := os.WriteFile(filepath.Join(directory, entries[0].Name(), "app_build_100.log"), []byte(r.log), 0o600); err != nil {
		return release.RunResult{}, err
	}
	return release.RunResult{Output: r.console}, nil
}

func TestPublisherChecksFreshBuildLog(t *testing.T) {
	for _, tc := range []struct {
		name, console, log, wantID, wantError string
	}{
		{
			name: "completion only in fresh log", console: "SteamCMD finished\n",
			log: "[2026-09-26 17:29:02]: Successfully finished AppID 100 build (BuildID 12345).\n", wantID: "12345",
		},
		{
			name: "commit failure after completion", console: "Successfully finished AppID 100 build (BuildID 12345).\n",
			log: "Successfully finished AppID 100 build (BuildID 12345).\nFailed to commit build\n", wantError: "build log reported a failure",
		},
		{
			name: "conflicting completions", console: "Successfully finished AppID 100 build (BuildID 12345).\n",
			log: "Successfully finished AppID 100 build (BuildID 54321).\n", wantError: "different completions",
		},
		{
			name: "conflicting console despite valid log", console: "Successfully finished AppID 100 build (BuildID 12345).\nSuccessfully finished AppID 100 build (BuildID 54321).\n",
			log: "Successfully finished AppID 100 build (BuildID 12345).\n", wantError: "conflicting completion lines",
		},
		{
			name: "incomplete log", console: "Successfully finished AppID 100 build (BuildID 12345).\n",
			log: "Starting AppID 100 build\n", wantError: "unconfirmed in the build log",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, _ := publisherFixture(t)
			publisher.Runner = buildLogRunner{project: publisher.Document.ProjectRoot(), console: tc.console, log: tc.log}
			result, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder"})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("upload error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil || result.BuildID != tc.wantID {
				t.Fatalf("upload = %#v, %v", result, err)
			}
		})
	}
}

func TestPublisherDoesNotTrustSharedBuildLog(t *testing.T) {
	publisher, runner := publisherFixture(t)
	profile := publisher.Document.Config.Profiles["release"]
	profile.BuildOutput = "shared-output"
	publisher.Document.Config.Profiles["release"] = profile
	directory := publisher.Document.ResolveProject("shared-output")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(directory, "app_build_100.log")
	if err := os.WriteFile(log, []byte("Successfully finished AppID 100 build (BuildID 12345).\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.output = "SteamCMD exited without a completion line\n"
	if _, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder"}); err == nil || !strings.Contains(err.Error(), "unconfirmed") {
		t.Fatalf("stale shared build log was trusted: %v", err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("shared build log was changed: %v", err)
	}
}

func TestPublisherDoesNotTrustCustomVDFBuildLog(t *testing.T) {
	publisher, runner := publisherFixture(t)
	profile := publisher.Document.Config.Profiles["release"]
	profile.ExistingVDF = "custom.vdf"
	publisher.Document.Config.Profiles["release"] = profile
	if err := os.WriteFile(publisher.Document.ResolveProject("custom.vdf"), []byte("custom script"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory := publisher.Document.ResolveProject(filepath.Join(".steam-releaser", "output"))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(directory, "app_build_100.log")
	if err := os.WriteFile(log, []byte("Successfully finished AppID 100 build (BuildID 12345).\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.output = "SteamCMD exited without a completion line\n"
	if _, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder"}); err == nil || !strings.Contains(err.Error(), "unconfirmed") {
		t.Fatalf("custom VDF accepted a stale log: %v", err)
	}
}

func TestGeneratedBuildLogRejectsUnsafeFiles(t *testing.T) {
	project := t.TempDir()
	output := filepath.Join(project, ".steam-releaser", "output", "run-0123456789abcdef0123456789abcdef")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := openGeneratedBuildLog(project, output)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, present, err := log.Read(100); err != nil || present {
		t.Fatalf("missing log = present %v, %v", present, err)
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(output, "app_build_100.log")
	if err := os.Symlink(outside, link); err == nil {
		if _, _, err := log.Read(100); err == nil {
			t.Fatal("accepted a linked build log")
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Logf("symlinks unavailable: %v", err)
	}
	file, err := os.Create(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxSteamBuildLog + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Read(100); err == nil || !strings.Contains(err.Error(), "capture limit") {
		t.Fatalf("accepted oversized build log: %v", err)
	}
}

func TestGeneratedBuildLogDetectsReplacedDirectory(t *testing.T) {
	project := t.TempDir()
	output := filepath.Join(project, ".steam-releaser", "output", "run-0123456789abcdef0123456789abcdef")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	log, err := openGeneratedBuildLog(project, output)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if err := os.Rename(output, output+"-moved"); err != nil {
		t.Skipf("cannot rename an open directory on this filesystem: %v", err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := log.Read(100); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("accepted a replaced output directory: %v", err)
	}
}

func TestGeneratedBuildLogRequiresAnEmptyDirectory(t *testing.T) {
	project := t.TempDir()
	output := filepath.Join(project, ".steam-releaser", "output", "run-0123456789abcdef0123456789abcdef")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(output, "app_build_100.log")
	if err := os.WriteFile(stale, []byte("old log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if log, err := openGeneratedBuildLog(project, output); err == nil {
		log.Close()
		t.Fatal("accepted a nonempty output directory")
	}
	if contents, err := os.ReadFile(stale); err != nil || string(contents) != "old log" {
		t.Fatalf("changed the existing log: %q, %v", contents, err)
	}
}
