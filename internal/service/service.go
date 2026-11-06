package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/thornity/steam-release-tool/internal/artifact"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/doctor"
	"github.com/thornity/steam-release-tool/internal/engine"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

type RunOptions struct {
	Targets []string `json:"targets"`
	// ReuseTargets contains completed targets from an explicitly requested retry.
	ReuseTargets        []string                    `json:"-"`
	TargetFingerprints  map[string]string           `json:"-"`
	UploadFingerprint   string                      `json:"-"`
	UnverifiedTargets   []string                    `json:"-"`
	CaptureFingerprints bool                        `json:"-"`
	VerifyRetry         func() error                `json:"-"`
	OnLocked            func(context.Context) error `json:"-"`
}

type Service struct {
	Document *config.Document
	Reporter release.Reporter
	Runner   release.ProcessRunner
}

func Load(path string, reporter release.Reporter) (*Service, error) {
	return LoadWithProjectRoot(path, "", reporter)
}

func LoadWithProjectRoot(path, projectRoot string, reporter release.Reporter) (*Service, error) {
	document, err := config.LoadWithProjectRoot(path, projectRoot)
	if err != nil {
		return nil, err
	}
	locked := release.LockReporter(reporter)
	return &Service{Document: document, Reporter: locked, Runner: release.CommandRunner{Reporter: locked}}, nil
}

// Standalone runs SteamCMD account commands before any configuration exists.
func Standalone(reporter release.Reporter) (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	locked := release.LockReporter(reporter)
	document := &config.Document{Path: filepath.Join(home, "steam-release.yaml")}
	return &Service{Document: document, Reporter: locked, Runner: release.CommandRunner{Reporter: locked}}, nil
}

func (s *Service) Doctor(profileName string, options RunOptions) (doctor.Report, error) {
	return doctor.Run(s.Document, profileName, options.Targets)
}

func (s *Service) Plan(profileName string, options RunOptions, upload steam.UploadOptions) (*release.Plan, error) {
	if err := upload.Validate(); err != nil {
		return nil, err
	}
	if err := steam.ValidateScriptSelection(s.Document, profileName, options.Targets, steam.ScriptOptions{Preview: upload.Preview, SetLive: upload.SetLive}); err != nil {
		return nil, err
	}
	plan, err := engine.Plan(s.Document, profileName, options.Targets)
	if err != nil {
		return nil, err
	}
	plan.Upload.Preview = upload.Preview
	plan.Upload.SetLive = upload.SetLive
	return plan, nil
}

func (s *Service) Build(ctx context.Context, profileName string, options RunOptions) error {
	prepared, err := s.prepareBuild(ctx, profileName, options)
	if err != nil {
		return err
	}
	releaseLock, err := acquireProjectLock(ctx, s.Document.ProjectRoot())
	if err != nil {
		return err
	}
	defer releaseLock()
	if options.VerifyRetry != nil {
		if err := options.VerifyRetry(); err != nil {
			return err
		}
	}
	if options.OnLocked != nil {
		if err := options.OnLocked(ctx); err != nil {
			return err
		}
	}
	return s.buildLocked(ctx, profileName, options, prepared)
}

type preparedBuild struct {
	plan      *release.Plan
	buildPlan release.Plan
	reused    []release.TargetPlan
}

// Prepare validates the selection without creating project files. The caller
// checks reused outputs again after obtaining the project lock.
func (s *Service) prepareBuild(ctx context.Context, profileName string, options RunOptions) (*preparedBuild, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := engine.Plan(s.Document, profileName, options.Targets)
	if err != nil {
		return nil, err
	}
	if plan.Engine == "prebuilt" && len(options.UnverifiedTargets) > 0 {
		return nil, fmt.Errorf("prebuilt targets have no artifact checkpoint or verified source checkpoint; start a new release")
	}
	reused := make(map[string]bool, len(options.ReuseTargets))
	for _, name := range options.ReuseTargets {
		found := false
		for _, target := range plan.Targets {
			if target.Name == name {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("cannot reuse unselected target %q", name)
		}
		reused[name] = true
	}
	prepared := &preparedBuild{plan: plan, buildPlan: *plan}
	prepared.buildPlan.Targets = nil
	for _, target := range plan.Targets {
		if reused[target.Name] {
			if options.TargetFingerprints[target.Name] == "" {
				return nil, fmt.Errorf("cannot reuse %s without an artifact checkpoint", target.Name)
			}
			prepared.reused = append(prepared.reused, target)
		} else {
			prepared.buildPlan.Targets = append(prepared.buildPlan.Targets, target)
		}
	}
	if plan.Engine == "unity" && len(prepared.buildPlan.Targets) > 0 {
		if err := engine.ValidateUnityProjectRoot(s.Document.ProjectRoot()); err != nil {
			return nil, err
		}
	}
	return prepared, nil
}

// buildLocked requires the caller to hold the project lock.
func (s *Service) buildLocked(ctx context.Context, profileName string, options RunOptions, prepared *preparedBuild) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := engine.CheckBuildPaths(prepared.plan); err != nil {
		return err
	}
	prepared.buildPlan.Parallelism = min(prepared.buildPlan.Parallelism, prepared.plan.Parallelism)
	verified := make(map[string]bool, len(prepared.reused))
	for _, target := range prepared.reused {
		s.Reporter.Report(release.Event{Level: release.LevelInfo, Target: target.Name, Message: "Checking saved build contents"})
		fingerprint, err := artifact.Fingerprint(ctx, s.Document, profileName, []string{target.Name})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || fingerprint != options.TargetFingerprints[target.Name] {
			if target.Command == nil {
				return fmt.Errorf("prebuilt target %s changed or is unavailable since the previous attempt; start a new release", target.Name)
			}
			s.Reporter.Report(release.Event{Level: release.LevelWarning, Target: target.Name, Message: "Saved build contents changed or are unavailable; rebuilding this target"})
			continue
		}
		verified[target.Name] = true
		s.Reporter.Report(release.Event{Level: release.LevelSuccess, Target: target.Name, Kind: "build-reused", Message: "Reusing completed build", Fingerprint: fingerprint})
	}
	prepared.buildPlan.Targets = nil
	for _, target := range prepared.plan.Targets {
		if !verified[target.Name] {
			prepared.buildPlan.Targets = append(prepared.buildPlan.Targets, target)
		}
	}
	if prepared.plan.Engine == "unity" && len(prepared.buildPlan.Targets) > 0 {
		if err := engine.ValidateUnityProjectRoot(s.Document.ProjectRoot()); err != nil {
			return err
		}
	}
	if err := artifact.Prepare(s.Document, prepared.plan); err != nil {
		return err
	}
	var protected []artifact.ProtectedCopyOutput
	for _, target := range prepared.plan.Targets {
		if target.Command != nil || verified[target.Name] {
			protected = append(protected,
				artifact.ProtectedCopyOutput{Name: target.Name, Path: target.Output, Reused: verified[target.Name], FreshnessEvidence: true},
				artifact.ProtectedCopyOutput{Name: target.Name, Path: target.Content, Reused: verified[target.Name]})
		}
	}
	copyGuard, err := artifact.SnapshotCopySources(ctx, s.Document, profileName, options.Targets, protected)
	if err != nil {
		return err
	}
	for _, command := range prepared.plan.Prepare {
		if len(prepared.buildPlan.Targets) == 0 {
			break
		}
		if _, err := s.Runner.Run(ctx, command, false); err != nil {
			return err
		}
	}
	pending, err := s.runTargetBuilds(ctx, profileName, &prepared.buildPlan, options.CaptureFingerprints)
	if err != nil {
		return err
	}
	if copyGuard != nil {
		var names []string
		for name := range pending {
			names = append(names, name)
		}
		copyGuard.SetPending(names)
	}
	if err := artifact.ApplyCopyRulesWithGuard(s.Document, profileName, options.Targets, s.Reporter, copyGuard); err != nil {
		return err
	}
	for _, target := range prepared.buildPlan.Targets {
		before, ok := pending[target.Name]
		if !ok {
			continue
		}
		after, err := observeOutput(ctx, target.Output)
		if err != nil {
			err = fmt.Errorf("inspect output after post-build copies for %s: %w", target.Name, err)
		} else if after.info == nil {
			err = fmt.Errorf("target %s is missing build output %s", target.Name, target.Output)
		} else if !copyGuard.Fresh(target.Name) {
			if before.info == nil {
				err = fmt.Errorf("target %s is missing build output produced by this build: %s", target.Name, target.Output)
			} else {
				err = fmt.Errorf("build for %s succeeded but no change was detected in its existing output: %s; check the build command and rebuild, or review the artifact and upload it separately", target.Name, target.Output)
			}
		}
		if err != nil {
			s.Reporter.Report(release.Event{Level: release.LevelError, Target: target.Name, Kind: "build-failed", Message: err.Error()})
			return err
		}
		s.Reporter.Report(release.Event{Level: release.LevelWarning, Target: target.Name, Message: "The build command did not change its output; a post-build source was created or changed during the build. Artifact freshness remains unverified."})
		fingerprint := s.targetFingerprint(ctx, profileName, target.Name, options.CaptureFingerprints)
		s.Reporter.Report(release.Event{Level: release.LevelSuccess, Target: target.Name, Kind: "build-complete", Message: "Build completed", Fingerprint: fingerprint})
	}
	if err := artifact.Validate(s.Document, profileName, options.Targets); err != nil {
		return err
	}
	for _, target := range prepared.reused {
		if !verified[target.Name] {
			continue
		}
		fingerprint, err := artifact.Fingerprint(ctx, s.Document, profileName, []string{target.Name})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || fingerprint != options.TargetFingerprints[target.Name] {
			return fmt.Errorf("reused build contents for %s changed during this run; retry to rebuild it or start a new release", target.Name)
		}
	}
	s.Reporter.Report(release.Event{Level: release.LevelSuccess, Kind: "build-ready", Message: "All selected builds completed and passed artifact validation"})
	return nil
}

func (s *Service) Upload(ctx context.Context, profileName string, run RunOptions, options steam.UploadOptions) (steam.UploadResult, error) {
	if err := ctx.Err(); err != nil {
		return steam.UploadResult{}, err
	}
	if err := options.Validate(); err != nil {
		return steam.UploadResult{}, err
	}
	if err := steam.ValidateScriptSelection(s.Document, profileName, run.Targets, steam.ScriptOptions{Preview: options.Preview, SetLive: options.SetLive}); err != nil {
		return steam.UploadResult{}, err
	}
	releaseLock, err := acquireProjectLock(ctx, s.Document.ProjectRoot())
	if err != nil {
		return steam.UploadResult{}, err
	}
	defer releaseLock()
	if run.VerifyRetry != nil {
		if err := run.VerifyRetry(); err != nil {
			return steam.UploadResult{}, err
		}
	}
	if run.OnLocked != nil {
		if err := run.OnLocked(ctx); err != nil {
			return steam.UploadResult{}, err
		}
	}
	return s.uploadLocked(ctx, profileName, run, options)
}

// uploadLocked requires the caller to hold the project lock.
func (s *Service) uploadLocked(ctx context.Context, profileName string, run RunOptions, options steam.UploadOptions) (steam.UploadResult, error) {
	if err := ctx.Err(); err != nil {
		return steam.UploadResult{}, err
	}
	if err := artifact.Validate(s.Document, profileName, run.Targets); err != nil {
		return steam.UploadResult{}, err
	}
	if run.CaptureFingerprints || run.UploadFingerprint != "" {
		selection, err := s.Document.Select(profileName, run.Targets)
		if err != nil {
			return steam.UploadResult{}, err
		}
		if selection.Profile.ExistingVDF != "" {
			if run.UploadFingerprint != "" {
				return steam.UploadResult{}, fmt.Errorf("cannot verify upload contents referenced by existing_vdf; start a new upload after reviewing the script")
			}
			s.Reporter.Report(release.Event{Level: release.LevelWarning, Message: "Cannot record an upload checkpoint for existing_vdf; a failed upload must be started again after reviewing the script"})
		} else {
			s.Reporter.Report(release.Event{Level: release.LevelInfo, Message: "Checking artifact contents for retry safety"})
			fingerprint, err := artifact.Fingerprint(ctx, s.Document, profileName, run.Targets)
			if ctx.Err() != nil {
				return steam.UploadResult{}, ctx.Err()
			}
			if run.UploadFingerprint != "" && (err != nil || fingerprint != run.UploadFingerprint) {
				return steam.UploadResult{}, fmt.Errorf("artifact contents changed or are unavailable since the previous attempt; start a new build or upload")
			}
			if err != nil {
				s.Reporter.Report(release.Event{Level: release.LevelWarning, Message: "Could not record an artifact checkpoint; a failed upload cannot be retried automatically: " + err.Error()})
			} else if run.CaptureFingerprints {
				s.Reporter.Report(release.Event{Kind: "upload-checkpoint", Fingerprint: fingerprint})
			}
		}
	}
	publisher := steam.Publisher{Document: s.Document, Runner: s.Runner, Reporter: s.Reporter}
	return publisher.Upload(ctx, profileName, run.Targets, options)
}

func (s *Service) Ship(ctx context.Context, profileName string, run RunOptions, options steam.UploadOptions) (steam.UploadResult, error) {
	if err := ctx.Err(); err != nil {
		return steam.UploadResult{}, err
	}
	if err := options.Validate(); err != nil {
		return steam.UploadResult{}, err
	}
	if err := steam.ValidateScriptSelection(s.Document, profileName, run.Targets, steam.ScriptOptions{Preview: options.Preview, SetLive: options.SetLive}); err != nil {
		return steam.UploadResult{}, err
	}
	publisher := steam.Publisher{Document: s.Document, Runner: s.Runner, Reporter: s.Reporter}
	if err := publisher.CheckUploadPrerequisites(options); err != nil {
		return steam.UploadResult{}, err
	}
	prepared, err := s.prepareBuild(ctx, profileName, run)
	if err != nil {
		return steam.UploadResult{}, err
	}
	releaseLock, err := acquireProjectLock(ctx, s.Document.ProjectRoot())
	if err != nil {
		return steam.UploadResult{}, err
	}
	defer releaseLock()
	if run.VerifyRetry != nil {
		if err := run.VerifyRetry(); err != nil {
			return steam.UploadResult{}, err
		}
		selection, err := s.Document.Select(profileName, run.Targets)
		if err != nil {
			return steam.UploadResult{}, err
		}
		if selection.Profile.ExistingVDF != "" {
			return steam.UploadResult{}, fmt.Errorf("cannot verify upload contents referenced by existing_vdf; start a new operation after reviewing the script")
		}
	}
	if run.OnLocked != nil {
		if err := run.OnLocked(ctx); err != nil {
			return steam.UploadResult{}, err
		}
	}
	if err := s.buildLocked(ctx, profileName, run, prepared); err != nil {
		return steam.UploadResult{}, err
	}
	return s.uploadLocked(ctx, profileName, run, options)
}

func (s *Service) Login(ctx context.Context, username string) error {
	return s.LoginWithOptions(ctx, username, true)
}

func (s *Service) LoginWithOptions(ctx context.Context, username string, interactive bool) error {
	publisher := steam.Publisher{Document: s.Document, Runner: s.Runner, Reporter: s.Reporter}
	return publisher.LoginWithOptions(ctx, username, interactive)
}

func (s *Service) LoginWithInput(ctx context.Context, username string, input io.Reader) error {
	publisher := steam.Publisher{Document: s.Document, Runner: s.Runner, Reporter: s.Reporter}
	return publisher.LoginWithInput(ctx, username, input)
}

func (s *Service) Logout(ctx context.Context, username string, interactive bool) error {
	publisher := steam.Publisher{Document: s.Document, Runner: s.Runner, Reporter: s.Reporter}
	return publisher.Logout(ctx, username, interactive)
}

func (s *Service) runTargetBuilds(ctx context.Context, profileName string, plan *release.Plan, captureFingerprints bool) (map[string]outputObservation, error) {
	workerCount := min(max(1, plan.Parallelism), len(plan.Targets))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan release.TargetPlan)
	errorsFound := make(chan error, len(plan.Targets))
	pending := make(map[string]outputObservation)
	var pendingMu sync.Mutex
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for target := range jobs {
				if ctx.Err() != nil {
					return
				}
				if target.Command == nil {
					fingerprint := s.targetFingerprint(ctx, profileName, target.Name, captureFingerprints)
					s.Reporter.Report(release.Event{Level: release.LevelSuccess, Target: target.Name, Kind: "build-prebuilt", Message: "Using prebuilt artifact", Fingerprint: fingerprint})
					continue
				}
				s.Reporter.Report(release.Event{Level: release.LevelInfo, Target: target.Name, Kind: "build-start", Message: "Building"})
				before, err := observeOutput(ctx, target.Output)
				if err != nil {
					err = fmt.Errorf("inspect output before building %s: %w", target.Name, err)
					s.Reporter.Report(release.Event{Level: release.LevelError, Target: target.Name, Kind: "build-failed", Message: err.Error()})
					errorsFound <- err
					cancel()
					return
				}
				if _, err := s.Runner.Run(ctx, *target.Command, false); err != nil {
					s.Reporter.Report(release.Event{Level: release.LevelError, Target: target.Name, Kind: "build-failed", Message: err.Error()})
					errorsFound <- err
					cancel()
					return
				}
				after, err := observeOutput(ctx, target.Output)
				if err != nil {
					err = fmt.Errorf("inspect output after building %s: %w", target.Name, err)
				} else if after.info == nil || after.info.IsDir() && !after.hasEntries || before.info != nil && before.unchanged(after) {
					pendingMu.Lock()
					pending[target.Name] = before
					pendingMu.Unlock()
					continue
				}
				if err != nil {
					s.Reporter.Report(release.Event{Level: release.LevelError, Target: target.Name, Kind: "build-failed", Message: err.Error()})
					errorsFound <- err
					cancel()
					return
				}
				fingerprint := s.targetFingerprint(ctx, profileName, target.Name, captureFingerprints)
				s.Reporter.Report(release.Event{Level: release.LevelSuccess, Target: target.Name, Kind: "build-complete", Message: "Build completed", Fingerprint: fingerprint})
			}
		}()
	}
sendTargets:
	for _, target := range plan.Targets {
		select {
		case jobs <- target:
		case <-ctx.Done():
			break sendTargets
		}
	}
	close(jobs)
	workers.Wait()
	close(errorsFound)
	var allErrors []error
	for err := range errorsFound {
		allErrors = append(allErrors, err)
	}
	if len(allErrors) > 0 {
		return nil, errors.Join(allErrors...)
	}
	return pending, ctx.Err()
}

func (s *Service) targetFingerprint(ctx context.Context, profileName, target string, capture bool) string {
	if !capture {
		return ""
	}
	s.Reporter.Report(release.Event{Level: release.LevelInfo, Target: target, Message: "Recording build contents for retry safety"})
	fingerprint, err := artifact.Fingerprint(ctx, s.Document, profileName, []string{target})
	if err != nil && ctx.Err() == nil {
		s.Reporter.Report(release.Event{Level: release.LevelWarning, Target: target, Message: "Could not record a retry checkpoint; this target will be rebuilt on retry: " + err.Error()})
	}
	return fingerprint
}
