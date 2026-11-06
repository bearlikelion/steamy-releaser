package steam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/tools"
)

type UploadOptions struct {
	Username    string    `json:"username"`
	Preview     bool      `json:"preview"`
	SetLive     string    `json:"set_live"`
	Interactive bool      `json:"-"`
	Input       io.Reader `json:"-"`
}

func (o UploadOptions) Validate() error {
	return ValidateScriptOptions(ScriptOptions{Preview: o.Preview, SetLive: o.SetLive})
}

type UploadResult struct {
	BuildID     string  `json:"build_id,omitempty" yaml:"build_id,omitempty"`
	Username    string  `json:"username" yaml:"username"`
	BuildScript string  `json:"build_script" yaml:"build_script"`
	Output      string  `json:"-" yaml:"-"`
	Scripts     Scripts `json:"scripts" yaml:"scripts"`
}

type Publisher struct {
	Document *config.Document
	Runner   release.ProcessRunner
	Reporter release.Reporter
}

// CheckUploadPrerequisites checks local upload requirements without running
// SteamCMD, generating scripts, or requiring artifacts that have yet to be built.
func (p Publisher) CheckUploadPrerequisites(options UploadOptions) error {
	_, _, err := p.uploadPrerequisites(options)
	return err
}

func (p Publisher) uploadPrerequisites(options UploadOptions) (string, tools.Resolution, error) {
	if err := options.Validate(); err != nil {
		return "", tools.Resolution{}, err
	}
	username, err := auth.ResolveUsername(options.Username)
	if err != nil {
		return "", tools.Resolution{}, fmt.Errorf("%w; sign in to Steam or set STEAM_USERNAME", err)
	}
	steamCMD := tools.SteamCMD(p.Document)
	if !steamCMD.Found {
		return "", steamCMD, fmt.Errorf("SteamCMD was not found; configure tools.steamcmd or STEAMCMD_BIN")
	}
	return username, steamCMD, nil
}

func (p Publisher) Upload(ctx context.Context, profileName string, targets []string, options UploadOptions) (UploadResult, error) {
	username, steamCMD, err := p.uploadPrerequisites(options)
	if err != nil {
		return UploadResult{}, err
	}
	selection, err := p.Document.Select(profileName, targets)
	if err != nil {
		return UploadResult{}, err
	}
	scripts, err := Generate(p.Document, profileName, targets, ScriptOptions{Preview: options.Preview, SetLive: options.SetLive})
	if err != nil {
		return UploadResult{}, err
	}
	var buildLog *generatedBuildLog
	if selection.Profile.ExistingVDF == "" && strings.TrimSpace(selection.Profile.BuildOutput) == "" {
		buildLog, err = openGeneratedBuildLog(p.Document.ProjectRoot(), scripts.BuildOutput)
		if err != nil {
			return UploadResult{}, fmt.Errorf("prepare Steam build log: %w", err)
		}
		defer buildLog.Close()
	}
	args := []string{}
	if !options.Interactive && options.Input == nil {
		args = append(args, "+@NoPromptForPassword", "1")
	}
	args = append(args, "+login", username, "+run_app_build", scripts.AppPath, "+quit")
	plan := release.CommandPlan{
		Label:      "SteamCMD upload",
		Program:    steamCMD.Path,
		Args:       args,
		WorkingDir: p.Document.ProjectRoot(),
	}
	var result release.RunResult
	var runErr error
	if options.Input != nil {
		runner, ok := p.Runner.(release.InputProcessRunner)
		if !ok {
			return UploadResult{}, fmt.Errorf("process runner does not support desktop SteamCMD input")
		}
		result, runErr = runner.RunWithInput(ctx, plan, options.Input)
	} else {
		result, runErr = p.Runner.Run(ctx, plan, options.Interactive)
	}
	if ctx.Err() != nil {
		return UploadResult{}, fmt.Errorf("SteamCMD upload cancelled; check Steamworks before retrying: %w", ctx.Err())
	}
	if runErr != nil || anySteamOutput(result, steamOutputFailed) {
		if anySteamOutput(result, steamAuthFailed) {
			if options.Input != nil {
				return UploadResult{}, fmt.Errorf("SteamCMD authentication failed; check the process log and retry")
			}
			_ = auth.ClearUsername()
			if !options.Interactive {
				return UploadResult{}, fmt.Errorf("SteamCMD authentication failed; refresh its saved login token with auth login outside CI and preserve SteamCMD's config/config.vdf between runs; cached username was cleared")
			}
			return UploadResult{}, fmt.Errorf("SteamCMD authentication failed; cached username was cleared")
		}
		if runErr != nil {
			return UploadResult{}, fmt.Errorf("SteamCMD upload failed; verify Steamworks before retrying: %w", runErr)
		}
		return UploadResult{}, fmt.Errorf("SteamCMD reported that the app build failed; verify Steamworks before retrying")
	}
	if result.OutputTruncated {
		return UploadResult{}, fmt.Errorf("SteamCMD output exceeded the capture limit; upload status is unconfirmed, verify the build in Steamworks before retrying")
	}
	completion, completionErr := steamBuildCompletion(result)
	if completionErr != nil && !errors.Is(completionErr, errNoSteamCompletion) {
		return UploadResult{}, fmt.Errorf("SteamCMD completion is unconfirmed: %w; check Steamworks before retrying", completionErr)
	}
	if buildLog != nil {
		contents, present, err := buildLog.Read(selection.Profile.AppID)
		if err != nil {
			return UploadResult{}, fmt.Errorf("SteamCMD completion is unconfirmed: %w; check Steamworks before retrying", err)
		}
		if present {
			if steamOutputFailed(contents) {
				return UploadResult{}, fmt.Errorf("SteamCMD build log reported a failure; verify Steamworks before retrying")
			}
			logged, err := steamBuildCompletion(release.RunResult{Output: contents})
			if err != nil {
				return UploadResult{}, fmt.Errorf("SteamCMD completion is unconfirmed in the build log: %w; check Steamworks before retrying", err)
			}
			if completionErr == nil && completion != logged {
				return UploadResult{}, fmt.Errorf("SteamCMD console and build log report different completions; check Steamworks before retrying")
			}
			completion, completionErr = logged, nil
		}
	}
	if completionErr != nil {
		return UploadResult{}, fmt.Errorf("SteamCMD completion is unconfirmed: %w; check the build log and Steamworks before retrying", completionErr)
	}
	if completion.AppID != selection.Profile.AppID {
		return UploadResult{}, fmt.Errorf("SteamCMD reported completion for app %d instead of app %d; check Steamworks before retrying", completion.AppID, selection.Profile.AppID)
	}
	if completion.Preview != options.Preview {
		if completion.Preview {
			return UploadResult{}, fmt.Errorf("SteamCMD completed a preview instead of an upload; check the build script and Steamworks before retrying")
		}
		return UploadResult{}, fmt.Errorf("SteamCMD reported build %s during a requested preview; check Steamworks before retrying", completion.BuildID)
	}
	if err := auth.CacheUsername(username); err != nil {
		return UploadResult{}, fmt.Errorf("SteamCMD completed but username cache failed: %w", err)
	}
	if p.Reporter != nil {
		if options.SetLive != "" {
			p.Reporter.Report(release.Event{Level: release.LevelWarning, Message: "Confirm the requested branch promotion in Steamworks"})
		}
		message := "Steam upload completed successfully"
		if options.Preview {
			message = "Steam preview completed successfully; no upload was performed"
		}
		p.Reporter.Report(release.Event{Level: release.LevelSuccess, Message: message})
	}
	return UploadResult{BuildID: completion.BuildID, Username: username, BuildScript: scripts.AppPath, Output: result.Output, Scripts: scripts}, nil
}

var steamCompletionPattern = regexp.MustCompile(`(?im)^[ \t]*(?:\[[^\]\r\n]{1,64}\]:[ \t]*)?successfully finished appid[ \t]+([0-9]+)[ \t]+build[ \t]+(?:\(buildid[ \t]+([0-9]+)\)|(preview))\.?[ \t]*\r?$`)
var errNoSteamCompletion = errors.New("no recognized build completion line")

func anySteamOutput(result release.RunResult, matches func(string) bool) bool {
	for _, output := range result.OutputStreams() {
		if matches(output) {
			return true
		}
	}
	return false
}

type steamCompletion struct {
	AppID   uint64
	BuildID string
	Preview bool
}

// Accept only SteamCMD's completion line, not an unrelated BuildID mention.
// Conflicting completion lines are ambiguous even when the process exits zero.
func steamBuildCompletion(result release.RunResult) (steamCompletion, error) {
	var completion steamCompletion
	found := false
	for _, output := range result.OutputStreams() {
		for _, match := range steamCompletionPattern.FindAllStringSubmatch(output, -1) {
			appID, err := strconv.ParseUint(match[1], 10, 64)
			if err != nil || appID == 0 {
				return steamCompletion{}, fmt.Errorf("invalid app ID in completion output")
			}
			candidate := steamCompletion{AppID: appID, Preview: match[3] != ""}
			if !candidate.Preview {
				buildID, err := strconv.ParseUint(match[2], 10, 64)
				if err != nil || buildID == 0 {
					return steamCompletion{}, fmt.Errorf("invalid build ID in completion output")
				}
				candidate.BuildID = strconv.FormatUint(buildID, 10)
			}
			if found && completion != candidate {
				return steamCompletion{}, fmt.Errorf("conflicting completion lines")
			}
			completion, found = candidate, true
		}
	}
	if !found {
		return steamCompletion{}, errNoSteamCompletion
	}
	return completion, nil
}

func (p Publisher) Login(ctx context.Context, username string) error {
	return p.LoginWithOptions(ctx, username, true)
}

func (p Publisher) LoginWithOptions(ctx context.Context, username string, interactive bool) error {
	err := p.login(ctx, username, func(plan release.CommandPlan) (release.RunResult, error) {
		if !interactive {
			plan.Args = append([]string{"+@NoPromptForPassword", "1"}, plan.Args...)
		}
		return p.Runner.Run(ctx, plan, interactive)
	})
	if err != nil && !interactive && ctx.Err() == nil {
		return fmt.Errorf("SteamCMD saved login token verification failed; run auth login outside CI and preserve SteamCMD's config/config.vdf between runs: %w", err)
	}
	return err
}

func (p Publisher) LoginWithInput(ctx context.Context, username string, input io.Reader) error {
	runner, ok := p.Runner.(release.InputProcessRunner)
	if !ok {
		return fmt.Errorf("process runner does not support desktop login input")
	}
	return p.login(ctx, username, func(plan release.CommandPlan) (release.RunResult, error) {
		return runner.RunWithInput(ctx, plan, input)
	})
}

func (p Publisher) login(ctx context.Context, username string, run func(release.CommandPlan) (release.RunResult, error)) error {
	if err := auth.ValidateUsername(username); err != nil {
		return err
	}
	steamCMD := tools.SteamCMD(p.Document)
	if !steamCMD.Found {
		return fmt.Errorf("SteamCMD was not found; install it, or set STEAMCMD_BIN or tools.steamcmd in the YAML")
	}
	result, err := run(release.CommandPlan{
		Label:      "SteamCMD login",
		Program:    steamCMD.Path,
		Args:       []string{"+login", username, "+quit"},
		WorkingDir: p.Document.ProjectRoot(),
	})
	if err != nil {
		return fmt.Errorf("SteamCMD login failed; complete the password and Steam Guard prompts: %w", err)
	}
	if anySteamOutput(result, steamOutputFailed) {
		return fmt.Errorf("SteamCMD login failed; complete the password and Steam Guard prompts")
	}
	if result.OutputTruncated {
		return fmt.Errorf("SteamCMD output exceeded the capture limit; login status is unconfirmed")
	}
	return auth.CacheUsername(username)
}

func (p Publisher) Logout(ctx context.Context, provided string, interactive bool) error {
	username, err := auth.ResolveUsername(provided)
	if err != nil {
		if strings.Contains(err.Error(), "no Steam username") {
			return auth.ClearUsername()
		}
		return err
	}
	if err := auth.ClearUsername(); err != nil {
		return err
	}
	steamCMD := tools.SteamCMD(p.Document)
	if !steamCMD.Found {
		return fmt.Errorf("cached username was cleared, but SteamCMD was not found to clear its login token")
	}
	args := []string{}
	if !interactive {
		args = append(args, "+@NoPromptForPassword", "1")
	}
	args = append(args, "+login", username, "+logout", "+quit")
	result, err := p.Runner.Run(ctx, release.CommandPlan{
		Label:      "SteamCMD logout",
		Program:    steamCMD.Path,
		Args:       args,
		WorkingDir: p.Document.ProjectRoot(),
	}, interactive)
	if err != nil {
		return err
	}
	if anySteamOutput(result, steamOutputFailed) {
		return fmt.Errorf("SteamCMD logout failed; cached username was cleared; check the process log")
	}
	if result.OutputTruncated {
		return fmt.Errorf("cached username was cleared, but SteamCMD logout status is unconfirmed because output exceeded the capture limit")
	}
	return nil
}

func steamAuthFailed(output string) bool {
	lower := strings.ToLower(output)
	for _, pattern := range []string{"invalid password", "login failure", "no cached credentials", "password is required", "not logged on", "invalid login auth code", "account logon denied", "account login denied", "two-factor code mismatch"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func steamOutputFailed(output string) bool {
	if steamAuthFailed(output) {
		return true
	}
	lower := strings.ToLower(output)
	for _, pattern := range []string{"error!", "failed (", "app build failed", "build failed", "failed to commit build", "failed to set build live", "failed to upload"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
