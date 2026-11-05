package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/localstore"
)

func TestHistoryPersistsBoundsAndFiltersAttempts(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	for i := range Limit + 3 {
		record := Record{ID: fmt.Sprint(i), Root: "game", Request: Request{ConfigPath: "release.yaml"}, StartedAt: time.Now(), Status: "failed"}
		if i == Limit+2 {
			record.Root = "other checkout"
		}
		if err := Save(record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := List("release.yaml", "game")
	if err != nil || len(records) != Limit-1 || records[0].ID != fmt.Sprint(Limit+1) {
		t.Fatalf("unexpected history: %d records, %v", len(records), err)
	}
	if _, err := Find("0"); err == nil {
		t.Fatal("oldest attempt was not removed")
	}
	records[0].Status = "complete"
	if err := Save(records[0]); err != nil {
		t.Fatal(err)
	}
	record, err := Find(records[0].ID)
	if err != nil || record.Status != "complete" {
		t.Fatalf("record not updated: %#v, %v", record, err)
	}
	path := filepath.Join(directory, localstore.Filename)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("history permissions = %v", info.Mode())
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(contents, []byte("SQLite format 3\x00")) {
		t.Fatalf("history is not a SQLite database: %v", err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(record); err == nil {
		t.Fatal("corrupt history was silently overwritten")
	}
	contents, err = os.ReadFile(path)
	if err != nil || string(contents) != "broken" {
		t.Fatalf("corrupt database was modified: %v", err)
	}
}

func TestHideHistoryPersistsAndRetainsRecords(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	cleared := Record{ID: "cleared", Root: "game", Request: Request{ConfigPath: "release.yaml"}, Status: "failed", Logs: "saved logs", Error: "build failed"}
	for _, record := range []Record{
		cleared,
		{ID: "other-root", Root: "other", Request: Request{ConfigPath: "release.yaml"}},
		{ID: "other-config", Root: "game", Request: Request{ConfigPath: "other.yaml"}},
	} {
		if err := Save(record); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := Hide("release.yaml", "game"); err != nil {
			t.Fatal(err)
		}
		if records, err := ListVisible("release.yaml", "game"); err != nil || len(records) != 0 {
			t.Fatalf("cleared history reappeared: %#v, %v", records, err)
		}
		if record, err := Find(cleared.ID); err != nil || !reflect.DeepEqual(record, cleared) {
			t.Fatalf("clear changed stored record: %#v, %v", record, err)
		}
	}
	if records, err := List("release.yaml", "game"); err != nil || len(records) != 1 {
		t.Fatalf("clear removed stored history: %#v, %v", records, err)
	}
	for _, selection := range [][2]string{{"release.yaml", "other"}, {"other.yaml", "game"}} {
		if records, err := ListVisible(selection[0], selection[1]); err != nil || len(records) != 1 {
			t.Fatalf("clear affected another project: %#v, %v", records, err)
		}
	}
	cleared.Status = "cancelled"
	if err := Save(cleared); err != nil {
		t.Fatal(err)
	}
	if records, err := ListVisible("release.yaml", "game"); err != nil || len(records) != 0 {
		t.Fatalf("updating a hidden record made it visible: %#v, %v", records, err)
	}
	for i := range Limit + 3 {
		if err := Save(Record{ID: fmt.Sprintf("new-%d", i), Root: "game", Request: Request{ConfigPath: "release.yaml"}}); err != nil {
			t.Fatal(err)
		}
	}
	if records, err := ListVisible("release.yaml", "game"); err != nil || len(records) != Limit || records[0].ID != fmt.Sprint("new-", Limit+2) {
		t.Fatalf("new releases were not visible or bounded: %#v, %v", records, err)
	}
	if record, err := Find(cleared.ID); err != nil || !reflect.DeepEqual(record, cleared) {
		t.Fatalf("later releases pruned the cleared record: %#v, %v", record, err)
	}
}

func TestHideRecordsOnlyClearsSelectedAttempts(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	selected := Record{ID: "selected", Root: "game", Request: Request{ConfigPath: "release.yaml"}, Status: "complete", Logs: "saved logs"}
	running := Record{ID: "running", Root: "game", Request: Request{ConfigPath: "release.yaml"}, Status: "running"}
	other := Record{ID: "other", Root: "other", Request: Request{ConfigPath: "release.yaml"}, Status: "complete"}
	for _, record := range []Record{selected, running, other} {
		if err := Save(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := HideRecords("release.yaml", "game", nil); err != nil {
		t.Fatal(err)
	}
	if records, err := ListVisible("release.yaml", "game"); err != nil || len(records) != 2 {
		t.Fatalf("empty selection changed history: %#v, %v", records, err)
	}
	if err := HideRecords("release.yaml", "game", []string{selected.ID, other.ID, "missing"}); err != nil {
		t.Fatal(err)
	}
	running.Status = "complete"
	if err := Save(running); err != nil {
		t.Fatal(err)
	}
	if records, err := ListVisible("release.yaml", "game"); err != nil || !reflect.DeepEqual(records, []Record{running}) {
		t.Fatalf("clear affected an unselected release: %#v, %v", records, err)
	}
	if records, err := ListVisible("release.yaml", "other"); err != nil || !reflect.DeepEqual(records, []Record{other}) {
		t.Fatalf("clear affected another project: %#v, %v", records, err)
	}
	if record, err := Find(selected.ID); err != nil || !reflect.DeepEqual(record, selected) {
		t.Fatalf("clear changed the saved record: %#v, %v", record, err)
	}
}

func TestHistoryMigratesLegacyRecordsOnce(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	finished := time.Now().Round(0).UTC()
	newest := Record{
		ID: "newest", Root: "game", Project: "Game", AppID: 123,
		StartedAt: finished.Add(-time.Minute), FinishedAt: &finished, DurationMS: 60000,
		Status: "failed", Error: "upload failed", ConfigHash: "config", Commit: "commit",
		SourceHash: "source", Version: "v1.0", Dirty: true, BuildID: "456",
		CompletedTargets: []string{"linux"}, BuildReady: true, Logs: "saved logs",
		Request: Request{Action: "ship", ConfigPath: "release.yaml", ProjectRoot: "game",
			Profile: "demo", Targets: []string{"linux"}, Username: "builder", Preview: true,
			SetLive: "qa", Notifications: true, RetryID: "older"},
	}
	older := Record{ID: "older", Root: "other", Request: Request{ConfigPath: "release.yaml"}}
	legacy := []Record{newest, older}
	contents, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "history.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	records, err := List("release.yaml", "game")
	if err != nil || !reflect.DeepEqual(records, []Record{newest}) {
		t.Fatalf("imported history = %#v, %v", records, err)
	}
	if record, err := Find("older"); err != nil || !reflect.DeepEqual(record, older) {
		t.Fatalf("other checkout lost: %#v, %v", record, err)
	}
	newest.Status = "complete"
	if err := Save(newest); err != nil {
		t.Fatal(err)
	}
	// The legacy backup cannot replace an updated record on the next open.
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := Find("newest")
	if err != nil || !reflect.DeepEqual(record, newest) {
		t.Fatalf("migration ran again: %#v, %v", record, err)
	}
}

func TestHistoryMigrationFailurePreservesLegacyData(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	path := filepath.Join(directory, "history.json")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(Record{ID: "new"}); err == nil {
		t.Fatal("corrupt legacy history was ignored")
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "broken" {
		t.Fatalf("legacy history was modified: %v", err)
	}
	if err := os.WriteFile(path, []byte(`[{"id":"recovered","root":"game","request":{"config_path":"release.yaml"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Find("recovered"); err != nil {
		t.Fatalf("repaired legacy file was not imported: %v", err)
	}
	if _, err := Find("new"); err == nil {
		t.Fatal("failed save survived migration rollback")
	}
}

func TestHistoryConcurrentProcesses(t *testing.T) {
	if worker := os.Getenv("STEAM_RELEASER_HISTORY_TEST_WORKER"); worker != "" {
		for i := range 5 {
			if err := Save(Record{ID: fmt.Sprintf("%s-%d", worker, i), Root: "game", Request: Request{ConfigPath: "release.yaml"}}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	errors := make(chan error, 4)
	for worker := range 4 {
		go func() {
			command := exec.Command(os.Args[0], "-test.run=^TestHistoryConcurrentProcesses$")
			command.Env = append(os.Environ(), fmt.Sprintf("STEAM_RELEASER_HISTORY_TEST_WORKER=%d", worker))
			output, err := command.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("worker %d: %w: %s", worker, err, output)
			}
			errors <- err
		}()
	}
	for range 4 {
		if err := <-errors; err != nil {
			t.Error(err)
		}
	}
	records, err := List("release.yaml", "game")
	if err != nil || len(records) != 20 {
		t.Fatalf("concurrent saves lost records: %d, %v", len(records), err)
	}
}

func TestHistoryConcurrentConnections(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	errors := make(chan error, 20)
	for i := range 20 {
		go func() {
			errors <- Save(Record{ID: fmt.Sprint(i), Root: "game", Request: Request{ConfigPath: "release.yaml"}})
		}()
	}
	for range 20 {
		if err := <-errors; err != nil {
			t.Error(err)
		}
	}
	records, err := List("release.yaml", "game")
	if err != nil || len(records) != 20 {
		t.Fatalf("concurrent connections lost records: %d, %v", len(records), err)
	}
}

func TestRetryUsesOriginalDestinationAndRejectsChangedConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, _ := ConfigHash(path)
	fingerprint := strings.Repeat("a", 64)
	record := Record{ID: "failed", Root: root, Status: "failed", ConfigHash: hash, BuildReady: true, CompletedTargets: []string{"linux"}, TargetFingerprints: map[string]string{"linux": fingerprint}, UploadFingerprint: fingerprint, Request: Request{Action: "ship", ConfigPath: path, Profile: "demo", Targets: []string{"linux"}, SetLive: "qa", Username: "builder"}}
	request, err := RetryRequest(record, true)
	if err != nil || request.Action != "upload" || request.SetLive != "qa" || request.Profile != "demo" || request.RetryID != "failed" || request.UploadFingerprint != fingerprint || !request.Notifications {
		t.Fatalf("retry = %#v, %v", request, err)
	}
	record.BuildReady = false
	request, err = RetryRequest(record, false)
	if err != nil || request.Action != "ship" || len(request.ReuseTargets) != 0 || len(request.UnverifiedTargets) != 1 || request.UnverifiedTargets[0] != "linux" {
		t.Fatalf("partial build retry = %#v, %v", request, err)
	}
	record.Status = "complete"
	if _, err := RetryRequest(record, false); err == nil {
		t.Fatal("completed release was retried")
	}
	record.Status = "failed"
	if err := os.WriteFile(path, []byte("app_id: 200\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RetryRequest(record, false); err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatalf("changed config retry: %v", err)
	}
}

func TestRetryDoesNotTrustMissingOrMalformedArtifactCheckpoints(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := ConfigHash(path)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{Status: "failed", Root: root, ConfigHash: hash, BuildReady: true,
		CompletedTargets: []string{"linux"}, TargetFingerprints: map[string]string{"linux": "invalid"},
		Request: Request{Action: "ship", ConfigPath: path, Targets: []string{"linux"}}}
	if _, err := RetryRequest(record, false); err == nil || !strings.Contains(err.Error(), "no artifact checkpoint") {
		t.Fatalf("unverified completed release was retried: %v", err)
	}
	record.BuildReady = false
	request, err := RetryRequest(record, false)
	if err != nil || request.Action != "ship" || len(request.ReuseTargets) != 0 || len(request.UnverifiedTargets) != 1 {
		t.Fatalf("unverified partial build should be rebuilt: %#v, %v", request, err)
	}
	record.Request.Action = "upload"
	if _, err := RetryRequest(record, false); err == nil || !strings.Contains(err.Error(), "no artifact checkpoint") {
		t.Fatalf("unverified upload was retried: %v", err)
	}
	record.CustomUploadScript = true
	for _, action := range []string{"ship", "upload"} {
		record.Request.Action = action
		if _, err := RetryRequest(record, false); err == nil || !strings.Contains(err.Error(), "cannot verify upload contents referenced by existing_vdf") {
			t.Errorf("custom script %s was retried: %v", action, err)
		}
	}
	record.Request.Action = "build"
	if _, err := RetryRequest(record, false); err != nil {
		t.Fatalf("custom script should not prevent a build-only retry: %v", err)
	}
}

func TestSanitizeRedactsEchoedInputsBeforeBoundingLogs(t *testing.T) {
	log := strings.Repeat("build output\n", LogLimit/10) + "Password: private\n" + "response secret-123\naccess_token=token-value\n"
	sanitized := Sanitize(log, []string{"secret-123", "private"})
	for _, value := range []string{"secret-123", "private", "token-value"} {
		if strings.Contains(sanitized, value) {
			t.Fatalf("secret %q persisted", value)
		}
	}
	if len(sanitized) > LogLimit+100 || !strings.Contains(sanitized, "[redacted]") {
		t.Fatal("log was not bounded and redacted")
	}
}

func TestRetryRejectsTrackedSourceChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s, %v", output, err)
		}
	}
	git("init", "--quiet")
	source := filepath.Join(root, "game.txt")
	if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "initial")
	path := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, _ := ConfigHash(path)
	commit, _, _ := GitMetadata(root)
	sourceHash, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("a", 64)
	record := Record{Status: "failed", Root: root, ConfigHash: hash, Commit: commit, SourceHash: sourceHash,
		CompletedTargets: []string{"linux"}, TargetFingerprints: map[string]string{"linux": fingerprint},
		Request: Request{Action: "build", ConfigPath: path}}
	request, err := RetryRequest(record, false)
	if err != nil || request.RetrySnapshot == nil || len(request.ReuseTargets) != 1 || request.TargetFingerprints["linux"] != fingerprint {
		t.Fatalf("verified target was not reused: %#v, %v", request, err)
	}
	legacy := record
	legacy.SourceHash = ""
	unverified, err := RetryRequest(legacy, false)
	if err != nil || len(unverified.ReuseTargets) != 0 || len(unverified.UnverifiedTargets) != 1 || unverified.UnverifiedTargets[0] != "linux" {
		t.Fatalf("target without a source checkpoint was reused: %#v, %v", unverified, err)
	}
	untracked := filepath.Join(root, "new-asset.bin")
	if err := os.WriteFile(untracked, []byte("new asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRetrySnapshot(*request.RetrySnapshot); err == nil || !strings.Contains(err.Error(), "source files changed") {
		t.Fatalf("retry accepted a new untracked source file: %v", err)
	}
	if err := os.Remove(untracked); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRetrySnapshot(*request.RetrySnapshot); err != nil {
		t.Fatalf("removing the new source file did not restore the snapshot: %v", err)
	}
	if err := os.WriteFile(source, []byte("changed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRetrySnapshot(*request.RetrySnapshot); err == nil || !strings.Contains(err.Error(), "source files changed") {
		t.Fatalf("changed source was not detected during retry verification: %v", err)
	}
	if _, err := RetryRequest(record, false); err == nil || !strings.Contains(err.Error(), "source files changed") {
		t.Fatalf("source changed retry: %v", err)
	}
	prepared, err := PrepareRetryRequest(record, false)
	if err != nil || prepared.RetrySnapshot == nil {
		t.Fatalf("could not prepare a retry for locked verification: %#v, %v", prepared, err)
	}
	if err := VerifyRetrySnapshot(*prepared.RetrySnapshot); err == nil || !strings.Contains(err.Error(), "source files changed") {
		t.Fatalf("locked retry verification accepted changed source: %v", err)
	}
}
