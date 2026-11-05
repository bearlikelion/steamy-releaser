package steam

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
)

type recordingRunner struct {
	commands    []release.CommandPlan
	interactive []bool
	output      string
	stdout      string
	stderr      string
	truncated   bool
	err         error
	input       string
}

func (r *recordingRunner) RunWithInput(_ context.Context, command release.CommandPlan, input io.Reader) (release.RunResult, error) {
	r.commands = append(r.commands, command)
	contents, err := io.ReadAll(input)
	r.input = string(contents)
	if err != nil {
		return release.RunResult{}, err
	}
	return release.RunResult{Output: r.output, Stdout: r.stdout, Stderr: r.stderr, OutputTruncated: r.truncated}, r.err
}

func (r *recordingRunner) Run(_ context.Context, command release.CommandPlan, interactive bool) (release.RunResult, error) {
	r.commands = append(r.commands, command)
	r.interactive = append(r.interactive, interactive)
	return release.RunResult{Output: r.output, Stdout: r.stdout, Stderr: r.stderr, OutputTruncated: r.truncated}, r.err
}

func TestPublisherChecksEachSteamOutputStream(t *testing.T) {
	for _, action := range []string{"upload", "login", "logout"} {
		t.Run(action, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			runner.output = "ERRORprogress\n! Permission denied\n"
			runner.stdout = "ERROR! Permission denied\n"
			runner.stderr = "progress\n"
			var err error
			switch action {
			case "upload":
				_, err = publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder"})
			case "login":
				err = publisher.Login(context.Background(), "builder")
			case "logout":
				err = publisher.Logout(context.Background(), "builder", false)
			}
			if err == nil {
				t.Fatalf("accepted a SteamCMD error split by stderr: %s", runner.output)
			}
		})
	}
}

func TestSteamOutputChecksDoNotJoinUnrelatedStreams(t *testing.T) {
	result := release.RunResult{Output: "ERROR! harmless diagnostic\n", Stdout: "ERROR", Stderr: "! harmless diagnostic\n"}
	if anySteamOutput(result, steamOutputFailed) {
		t.Fatal("unrelated stream fragments produced a false error")
	}
}

func TestSteamBuildCompletionUsesSeparateStreams(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  release.RunResult
		want    steamCompletion
		wantErr bool
	}{
		{"combined fallback", release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 123).\n"}, steamCompletion{AppID: 100, BuildID: "123"}, false},
		{"interleaved output", release.RunResult{Output: "Successfully progress\nfinished AppID 100 build (BuildID 123).\n", Stdout: "Successfully finished AppID 100 build (BuildID 123).\n", Stderr: "progress\n"}, steamCompletion{AppID: 100, BuildID: "123"}, false},
		{"preview", release.RunResult{Output: "[2026-09-28 20:24:30]: Successfully finished AppID 100 build preview.\r\n"}, steamCompletion{AppID: 100, Preview: true}, false},
		{"conflicting IDs", release.RunResult{Stdout: "Successfully finished AppID 100 build (BuildID 123).\n", Stderr: "Successfully finished AppID 100 build (BuildID 456).\n"}, steamCompletion{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := steamBuildCompletion(tc.result)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("completion = %#v, %v; want %#v, error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func publisherFixture(t *testing.T) (Publisher, *recordingRunner) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("STEAM_USERNAME", "")
	t.Setenv("STEAMCMD_BIN", "")
	steamCMD := filepath.Join(root, "steamcmd")
	if err := os.WriteFile(steamCMD, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Tools: config.Tools{SteamCMD: steamCMD},
		Profiles: map[string]config.Profile{"release": {
			AppID: 100, Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build/linux"}},
		}},
	}}
	runner := &recordingRunner{output: "Successfully finished AppID 100 build (BuildID 12345)."}
	return Publisher{Document: document, Runner: runner}, runner
}

func TestPublisherDoesNotConfirmTruncatedOutput(t *testing.T) {
	for _, action := range []string{"upload", "login"} {
		t.Run(action, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			runner.truncated = true
			var events []release.Event
			publisher.Reporter = release.ReporterFunc(func(event release.Event) { events = append(events, event) })
			var err error
			if action == "login" {
				err = publisher.Login(context.Background(), "builder")
			} else {
				_, err = publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder"})
			}
			if err == nil || !strings.Contains(err.Error(), "status is unconfirmed") {
				t.Fatalf("truncated output was accepted: %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("reported success despite truncation: %#v", events)
			}
			if _, err := auth.LoadUsername(); !os.IsNotExist(err) {
				t.Fatalf("cached an unconfirmed login: %v", err)
			}
		})
	}
}

func TestPublisherRequiresMatchingBuildCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		output  string
		preview bool
	}{
		{"empty output", "", false},
		{"login only", "Logging in user 'builder' to Steam Public...OK\n", false},
		{"ID without completion", "BuildID 12345\n", false},
		{"embedded text", "not a completion: Successfully finished AppID 100 build (BuildID 12345).\n", false},
		{"other app", "Successfully finished AppID 101 build (BuildID 12345).\n", false},
		{"preview instead of upload", "Successfully finished AppID 100 build preview.\n", false},
		{"upload instead of preview", "Successfully finished AppID 100 build (BuildID 12345).\n", true},
		{"zero build ID", "Successfully finished AppID 100 build (BuildID 0).\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			runner.output = tc.output
			var success bool
			publisher.Reporter = release.ReporterFunc(func(event release.Event) {
				if event.Level == release.LevelSuccess {
					success = true
				}
			})
			_, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder", Preview: tc.preview})
			if err == nil || success {
				t.Fatalf("reported success without the requested build completion: %v", err)
			}
			if _, err := auth.LoadUsername(); !os.IsNotExist(err) {
				t.Fatalf("cached a username after an unconfirmed upload: %v", err)
			}
		})
	}
}

func TestPublisherReportsMatchingCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, output, wantID string
		preview              bool
	}{
		{"upload", "[2026-09-26 17:29:02]: Successfully finished AppID 100 build (BuildID 12345).\n", "12345", false},
		{"preview", "[2026-09-28 20:24:30]: Successfully finished AppID 100 build preview.\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			runner.output = tc.output
			var success string
			publisher.Reporter = release.ReporterFunc(func(event release.Event) {
				if event.Level == release.LevelSuccess {
					success = event.Message
				}
			})
			result, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder", Preview: tc.preview})
			if err != nil || result.BuildID != tc.wantID || success == "" {
				t.Fatalf("completion = %#v, success=%q, error=%v", result, success, err)
			}
			if tc.preview && !strings.Contains(success, "no upload") {
				t.Fatalf("preview reported as an upload: %q", success)
			}
		})
	}
}

func TestPublisherAsksToVerifyBranchPromotion(t *testing.T) {
	publisher, _ := publisherFixture(t)
	var events []release.Event
	publisher.Reporter = release.ReporterFunc(func(event release.Event) { events = append(events, event) })
	if _, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder", SetLive: "beta"}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Level != release.LevelWarning || !strings.Contains(events[0].Message, "branch promotion") || events[1].Level != release.LevelSuccess {
		t.Fatalf("missing branch verification warning: %#v", events)
	}
}

func TestPublisherUploadArgumentsAndCache(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		name := "noninteractive"
		if interactive {
			name = "interactive"
		}
		t.Run(name, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			var events []release.Event
			publisher.Reporter = release.ReporterFunc(func(event release.Event) { events = append(events, event) })
			result, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder", Interactive: interactive})
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := []string{"+login", "builder", "+run_app_build", result.BuildScript, "+quit"}
			if !interactive {
				wantArgs = append([]string{"+@NoPromptForPassword", "1"}, wantArgs...)
			}
			if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, wantArgs) || runner.interactive[0] != interactive {
				t.Fatalf("unexpected SteamCMD calls: %#v, interactive=%v", runner.commands, runner.interactive)
			}
			if runner.commands[0].Program != publisher.Document.Config.Tools.SteamCMD || runner.commands[0].WorkingDir != publisher.Document.ProjectRoot() {
				t.Fatalf("incorrect SteamCMD executable or working directory: %#v", runner.commands[0])
			}
			if result.Username != "builder" || result.Output != runner.output || result.BuildScript != result.Scripts.AppPath {
				t.Fatalf("unexpected result: %#v", result)
			}
			if got, err := auth.LoadUsername(); err != nil || got != "builder" {
				t.Fatalf("upload did not cache username: %q, %v", got, err)
			}
			if len(events) != 1 || events[0].Level != release.LevelSuccess {
				t.Fatalf("unexpected success events: %#v", events)
			}
		})
	}
}

func TestPublisherUploadAcceptsDesktopInputWithoutCachedCredentials(t *testing.T) {
	publisher, runner := publisherFixture(t)
	result, err := publisher.Upload(context.Background(), "", nil, UploadOptions{
		Username: "builder", Input: strings.NewReader("secret\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"+login", "builder", "+run_app_build", result.BuildScript, "+quit"}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, wantArgs) || runner.input != "secret\n" || len(runner.interactive) != 0 {
		t.Fatalf("desktop upload did not use private SteamCMD input: %#v", runner)
	}
}

func TestPublisherDesktopAuthFailureKeepsUsernameForRetry(t *testing.T) {
	publisher, runner := publisherFixture(t)
	if err := auth.CacheUsername("builder"); err != nil {
		t.Fatal(err)
	}
	runner.output = "FAILED (No cached credentials)"
	_, err := publisher.Upload(context.Background(), "", nil, UploadOptions{
		Username: "builder", Input: strings.NewReader(""),
	})
	if err == nil || !strings.Contains(err.Error(), "check the process log") {
		t.Fatalf("desktop auth failure was not actionable: %v", err)
	}
	if username, err := auth.LoadUsername(); err != nil || username != "builder" {
		t.Fatalf("desktop auth failure erased the account name: %q, %v", username, err)
	}
}

func TestPublisherUploadDetectsFailures(t *testing.T) {
	runErr := errors.New("process failed")
	for _, tc := range []struct {
		name, output string
		runErr       error
		clearsCache  bool
	}{
		{"invalid password", "Invalid Password", nil, true},
		{"login failure", "LOGIN FAILURE", runErr, true},
		{"no credentials", "No cached credentials", nil, true},
		{"password required", "Password is required", nil, true},
		{"not logged on", "Not logged on", nil, true},
		{"invalid guard code", "FAILED (Invalid Login Auth Code)", nil, true},
		{"guard approval denied", "Account Logon Denied", nil, true},
		{"steam error", "ERROR! Failed to upload", nil, false},
		{"failed result", "Failed (Access Denied)", nil, false},
		{"build failure", "App build failed", nil, false},
		{"branch commit failure", "Failed to commit build", nil, false},
		{"process failure", "connection closed", runErr, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			if err := auth.CacheUsername("previous_builder"); err != nil {
				t.Fatal(err)
			}
			runner.output, runner.err = tc.output, tc.runErr
			publisher.Reporter = release.ReporterFunc(func(event release.Event) {
				if event.Level == release.LevelSuccess {
					t.Error("reported success for a failed upload")
				}
			})
			_, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "new_builder"})
			if err == nil {
				t.Fatal("upload accepted failed SteamCMD output")
			}
			if tc.clearsCache && !strings.Contains(err.Error(), "authentication failed") {
				t.Fatalf("expected authentication error, got %v", err)
			}
			if tc.runErr != nil && !tc.clearsCache && !errors.Is(err, tc.runErr) {
				t.Fatalf("lost process error: %v", err)
			}
			username, cacheErr := auth.LoadUsername()
			if tc.clearsCache {
				if !errors.Is(cacheErr, os.ErrNotExist) {
					t.Fatalf("authentication failure retained cache: %q, %v", username, cacheErr)
				}
			} else if cacheErr != nil || username != "previous_builder" {
				t.Fatalf("upload failure changed cache: %q, %v", username, cacheErr)
			}
		})
	}
}

func TestPublisherLogoutDoesNotConfirmTruncatedOutput(t *testing.T) {
	publisher, runner := publisherFixture(t)
	runner.truncated = true
	if err := auth.CacheUsername("builder"); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Logout(context.Background(), "builder", false); err == nil || !strings.Contains(err.Error(), "unconfirmed") {
		t.Fatalf("truncated logout was accepted: %v", err)
	}
	if _, err := auth.LoadUsername(); !os.IsNotExist(err) {
		t.Fatalf("logout did not clear local username: %v", err)
	}
}

func TestPublisherUploadRejectsInvalidRequestsBeforeRunning(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options UploadOptions
		targets []string
	}{
		{"missing username", UploadOptions{}, nil},
		{"invalid username", UploadOptions{Username: "+quit"}, nil},
		{"invalid options", UploadOptions{Username: "builder", Preview: true, SetLive: "beta"}, nil},
		{"unknown target", UploadOptions{Username: "builder"}, []string{"missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			if _, err := publisher.Upload(context.Background(), "", tc.targets, tc.options); err == nil {
				t.Fatal("accepted invalid upload request")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("invalid request started SteamCMD: %#v", runner.commands)
			}
			if _, err := os.Stat(publisher.Document.ResolveProject(".steam-releaser")); !os.IsNotExist(err) {
				t.Fatalf("invalid request generated scripts: %v", err)
			}
		})
	}
}

func TestPublisherLoginAndLogout(t *testing.T) {
	publisher, runner := publisherFixture(t)
	if err := publisher.Login(context.Background(), "builder"); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, []string{"+login", "builder", "+quit"}) || !runner.interactive[0] {
		t.Fatalf("unexpected login call: %#v", runner)
	}
	if username, err := auth.LoadUsername(); err != nil || username != "builder" {
		t.Fatalf("login did not cache username: %q, %v", username, err)
	}
	if err := publisher.Logout(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"+@NoPromptForPassword", "1", "+login", "builder", "+logout", "+quit"}
	if len(runner.commands) != 2 || !reflect.DeepEqual(runner.commands[1].Args, wantArgs) || runner.interactive[1] {
		t.Fatalf("unexpected logout call: %#v", runner)
	}
	if _, err := auth.LoadUsername(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout did not clear cache: %v", err)
	}
	if err := publisher.Logout(context.Background(), "", false); err != nil || len(runner.commands) != 2 {
		t.Fatalf("logout without credentials started a process: %v, %#v", err, runner.commands)
	}
}

func TestPublisherLoginWithDesktopInput(t *testing.T) {
	publisher, runner := publisherFixture(t)
	if err := publisher.LoginWithInput(context.Background(), "builder", strings.NewReader("secret\n")); err != nil {
		t.Fatal(err)
	}
	if runner.input != "secret\n" || len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].Args, []string{"+login", "builder", "+quit"}) {
		t.Fatalf("desktop login did not use private input: %#v", runner)
	}
	if username, err := auth.LoadUsername(); err != nil || username != "builder" {
		t.Fatalf("desktop login did not cache username: %q, %v", username, err)
	}
}

func TestPublisherFailedLoginDoesNotCacheUsername(t *testing.T) {
	for _, output := range []string{"Invalid password", "Login Failure", "No cached credentials", "FAILED (Invalid Login Auth Code)", "ERROR! Service unavailable"} {
		t.Run(output, func(t *testing.T) {
			publisher, runner := publisherFixture(t)
			runner.output = output
			if err := publisher.Login(context.Background(), "builder"); err == nil {
				t.Fatal("accepted failed login")
			}
			if _, err := auth.LoadUsername(); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed login cached username: %v", err)
			}
		})
	}
}

func TestPublisherNonInteractiveLogin(t *testing.T) {
	publisher, runner := publisherFixture(t)
	if err := publisher.LoginWithOptions(context.Background(), "builder", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"+@NoPromptForPassword", "1", "+login", "builder", "+quit"}
	if !reflect.DeepEqual(runner.commands[0].Args, want) || runner.interactive[0] {
		t.Fatalf("login enabled prompts: %#v", runner)
	}
	runner.output = "FAILED (No cached credentials)"
	if err := publisher.LoginWithOptions(context.Background(), "builder", false); err == nil || !strings.Contains(err.Error(), "config/config.vdf") {
		t.Fatalf("missing token recovery instructions: %v", err)
	}
}

func TestPublisherLogoutDetectsZeroExitFailure(t *testing.T) {
	publisher, runner := publisherFixture(t)
	if err := auth.CacheUsername("builder"); err != nil {
		t.Fatal(err)
	}
	runner.output = "FAILED (No cached credentials)"
	if err := publisher.Logout(context.Background(), "builder", false); err == nil {
		t.Fatal("logout reported success after SteamCMD failed")
	}
	if _, err := auth.LoadUsername(); !os.IsNotExist(err) {
		t.Fatalf("failed logout retained local credentials: %v", err)
	}
}

func TestPublisherMissingSteamCMD(t *testing.T) {
	publisher, runner := publisherFixture(t)
	if err := os.Remove(publisher.Document.Config.Tools.SteamCMD); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Login(context.Background(), "builder"); err == nil {
		t.Fatal("login accepted missing SteamCMD")
	}
	if _, err := publisher.Upload(context.Background(), "", nil, UploadOptions{Username: "builder"}); err == nil {
		t.Fatal("upload accepted missing SteamCMD")
	}
	if err := auth.CacheUsername("builder"); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Logout(context.Background(), "", false); err == nil || !strings.Contains(err.Error(), "cached username was cleared") {
		t.Fatalf("expected logout to report missing SteamCMD: %v", err)
	}
	if _, err := auth.LoadUsername(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout retained cache without SteamCMD: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("started missing SteamCMD: %#v", runner.commands)
	}
}
