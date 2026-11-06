package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/doctor"
	"github.com/thornity/steam-release-tool/internal/history"
	"github.com/thornity/steam-release-tool/internal/localstore"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/service"
	"github.com/thornity/steam-release-tool/internal/steam"
	"github.com/thornity/steam-release-tool/internal/tools"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type DesktopApp struct {
	ctx                      context.Context
	mu                       sync.Mutex
	cancel                   context.CancelFunc
	operationDone            chan struct{}
	exiting                  bool
	prepareExitOnce          sync.Once
	quitOnce                 sync.Once
	window                   desktopWindow
	busy                     bool
	steamInput               chan string
	steamReader              *os.File
	steamWriter              *os.File
	secrets                  []string
	credentialFile           auth.CredentialFileConfig
	steamUsername            string
	triedCredentialPassword  bool
	notifications            bool
	notificationsInitialized bool
	emit                     func(string, ...any)
	loadService              func(string, string, release.Reporter) (*service.Service, error)
	sendNotification         func(string, string) error
	openConfigFile           func(string) error
}

type ConfigSummary struct {
	Path           string           `json:"path"`
	ConfigHash     string           `json:"config_hash"`
	Project        string           `json:"project"`
	Root           string           `json:"root"`
	Engine         string           `json:"engine"`
	DefaultProfile string           `json:"default_profile"`
	Profiles       []ProfileSummary `json:"profiles"`
	Username       string           `json:"username"`
}

type ProfileSummary struct {
	Name    string          `json:"name"`
	AppID   uint64          `json:"app_id"`
	Targets []TargetSummary `json:"targets"`
}

type TargetSummary struct {
	Name         string            `json:"name"`
	Platform     string            `json:"platform"`
	Architecture string            `json:"architecture"`
	DepotID      uint64            `json:"depot_id"`
	Output       string            `json:"output"`
	BuildSetting string            `json:"build_setting"`
	BuildArgs    []string          `json:"build_args"`
	BuildEnv     map[string]string `json:"build_env"`
}

type CreateConfigRequest struct {
	Path        string                `json:"path"`
	ProjectRoot string                `json:"project_root"`
	Name        string                `json:"name"`
	Engine      string                `json:"engine"`
	AppID       uint64                `json:"app_id"`
	Targets     []CreateTargetRequest `json:"targets"`
}

type CreateTargetRequest struct {
	Name         string               `json:"name"`
	Platform     string               `json:"platform"`
	DepotID      uint64               `json:"depot_id"`
	Path         string               `json:"path"`
	BuildSetting string               `json:"build_setting"`
	Mappings     []config.FileMapping `json:"mappings"`
	Exclusions   []string             `json:"exclusions"`
}

type OperationRequest = history.Request

func (a *DesktopApp) GetPreferences(legacy map[string]string) (map[string]string, error) {
	return localstore.Preferences(legacy)
}

func (a *DesktopApp) SavePreferences(values map[string]string) error {
	return localstore.SavePreferences(values)
}

type OperationResult struct {
	Action         string          `json:"action"`
	OK             bool            `json:"ok"`
	Error          string          `json:"error,omitempty"`
	Data           any             `json:"data,omitempty"`
	History        *history.Record `json:"history,omitempty"`
	HistoryWarning string          `json:"history_warning,omitempty"`
}

type CredentialFileSettings struct {
	Enabled        bool   `json:"enabled"`
	Path           string `json:"path"`
	Username       string `json:"username"`
	HasCredentials bool   `json:"has_credentials"`
	Problem        string `json:"problem,omitempty"`
}

func credentialFileSettings(config auth.CredentialFileConfig) CredentialFileSettings {
	settings := CredentialFileSettings{Enabled: config.Enabled, Path: config.Path}
	if config.Enabled {
		credentials, err := auth.ReadCredentialFile(config.Path)
		if err != nil {
			settings.Problem = err.Error()
		} else {
			settings.Username = credentials.Username
			settings.HasCredentials = credentials.Password != ""
		}
	}
	return settings
}

func (a *DesktopApp) GetCredentialFileSettings() (CredentialFileSettings, error) {
	config, err := auth.LoadCredentialFileConfig()
	if err != nil {
		return CredentialFileSettings{}, err
	}
	return credentialFileSettings(config), nil
}

func (a *DesktopApp) ChooseCredentialFile() (string, error) {
	options := runtime.OpenDialogOptions{
		Title:   "Choose Existing Steam Credential File",
		Filters: []runtime.FileFilter{{DisplayName: "Steam credentials JSON", Pattern: "*.json"}},
	}
	options.DefaultDirectory, _ = localstore.Directory()
	if config, err := auth.LoadCredentialFileConfig(); err == nil && config.Path != "" {
		options.DefaultDirectory, options.DefaultFilename = filepath.Dir(config.Path), filepath.Base(config.Path)
	}
	return runtime.OpenFileDialog(a.ctx, options)
}

func (a *DesktopApp) ConfigureCredentialFile(enabled bool, path string) (CredentialFileSettings, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy || a.exiting {
		return CredentialFileSettings{}, fmt.Errorf("wait for the current operation before changing the credential file")
	}
	config, err := auth.LoadCredentialFileConfig()
	if err != nil {
		return CredentialFileSettings{}, err
	}
	if enabled {
		path = strings.TrimSpace(path)
		if path == "" {
			return CredentialFileSettings{}, fmt.Errorf("choose a credential file first")
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return CredentialFileSettings{}, err
		}
		if _, err := auth.ReadCredentialFile(path); err != nil {
			return CredentialFileSettings{}, err
		}
		config.Path = path
	}
	config.Enabled = enabled
	if err := auth.SaveCredentialFileConfig(config); err != nil {
		return CredentialFileSettings{}, err
	}
	return credentialFileSettings(config), nil
}

func NewDesktopApp() *DesktopApp {
	return &DesktopApp{openConfigFile: openConfigurationFile, window: desktopWindow{
		hide: runtime.Hide,
		show: func(ctx context.Context) {
			runtime.Show(ctx)
			runtime.WindowUnminimise(ctx)
		},
		quit: runtime.Quit,
	}}
}

type desktopWindow struct {
	hide func(context.Context)
	show func(context.Context)
	quit func(context.Context)
}

// HideWindow keeps releases and the webview alive while the window is in the tray.
func (a *DesktopApp) HideWindow() {
	a.window.hide(a.ctx)
}

func (a *DesktopApp) ShowWindow() {
	a.window.show(a.ctx)
}

// ForceExit is shared by the native window menu and tray menu.
func (a *DesktopApp) ForceExit() {
	a.prepareExit()
	a.quitOnce.Do(func() { a.window.quit(a.ctx) })
}

// Native application Quit actions (including macOS Cmd+Q) also cancel work.
// Closing the window bypasses this hook because HideWindowOnClose is enabled.
func (a *DesktopApp) beforeClose(context.Context) bool {
	a.prepareExit()
	return false
}

func (a *DesktopApp) prepareExit() {
	a.prepareExitOnce.Do(func() {
		a.mu.Lock()
		a.exiting = true
		cancel, done := a.cancel, a.operationDone
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			// Let child processes stop and history save, with a bounded wait.
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
			}
		}
	})
}

func (a *DesktopApp) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *DesktopApp) shutdown(context.Context) {
	a.prepareExit()
	a.mu.Lock()
	enabled := a.notificationsInitialized
	a.notifications = false
	a.mu.Unlock()
	if enabled {
		runtime.CleanupNotifications(a.ctx)
	}
}

func (a *DesktopApp) emitEvent(name string, values ...any) {
	if a.emit != nil {
		a.emit(name, values...)
		return
	}
	runtime.EventsEmit(a.ctx, name, values...)
}

func (a *DesktopApp) ConfigureNotifications(enabled bool) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !enabled {
		a.notifications = false
		return false, nil
	}
	if !runtime.IsNotificationAvailable(a.ctx) {
		return false, fmt.Errorf("desktop notifications are unavailable on this system")
	}
	if !a.notificationsInitialized {
		if err := runtime.InitializeNotifications(a.ctx); err != nil {
			return false, err
		}
		a.notificationsInitialized = true
	}
	authorized, err := runtime.RequestNotificationAuthorization(a.ctx)
	a.notifications = authorized && err == nil
	if err != nil {
		return false, err
	}
	if !authorized {
		return false, fmt.Errorf("allow Steam Releaser notifications in your system settings")
	}
	return true, nil
}

func (a *DesktopApp) notify(title, body string) {
	a.mu.Lock()
	enabled := a.notifications
	a.mu.Unlock()
	if enabled {
		send := a.sendNotification
		if send == nil {
			send = func(title, body string) error {
				return runtime.SendNotification(a.ctx, runtime.NotificationOptions{ID: "steam-releaser-" + fmt.Sprint(time.Now().UnixNano()), Title: title, Body: body})
			}
		}
		if err := send(title, body); err != nil {
			a.emitEvent("release:event", release.Event{Level: release.LevelWarning, Message: "Desktop notification could not be delivered: " + err.Error()})
		}
	}
}

func (a *DesktopApp) NotifySteamInput() {
	a.notify("Steam needs your attention", "Return to Steam Releaser to enter your password or Steam Guard response, or approve the sign-in in Steam.")
}

func (a *DesktopApp) GetReleaseHistory(configPath, projectRoot string) ([]history.Record, error) {
	document, err := config.LoadWithProjectRoot(configPath, projectRoot)
	if err != nil {
		return nil, err
	}
	records, err := history.ListVisible(document.Path, document.ProjectRoot())
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	if !busy {
		for i := range records {
			if records[i].Status == "running" {
				records[i].Status = "cancelled"
				records[i].Error = "The application closed before this attempt finished. Retry to validate and rebuild unfinished targets."
				if err := history.Save(records[i]); err != nil {
					return nil, err
				}
			}
		}
	}
	return records, nil
}

func (a *DesktopApp) ClearReleaseHistory(configPath, projectRoot string, recordIDs []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exiting {
		return fmt.Errorf("cannot clear release history while the application is closing")
	}
	document, err := config.LoadWithProjectRoot(configPath, projectRoot)
	if err != nil {
		return err
	}
	return history.HideRecords(document.Path, document.ProjectRoot(), recordIDs)
}

func (a *DesktopApp) CheckReadiness(request OperationRequest) (doctor.Report, error) {
	loaded, err := service.LoadWithProjectRoot(request.ConfigPath, request.ProjectRoot, release.NopReporter{})
	if err != nil {
		return doctor.Report{}, err
	}
	report, err := loaded.Doctor(request.Profile, service.RunOptions{Targets: request.Targets})
	if err != nil {
		return report, err
	}
	depotErr := steam.ValidateDepotSelection(loaded.Document, request.Profile, request.Targets)
	if depotErr != nil {
		report.Issues = append(report.Issues, doctor.Issue{Severity: doctor.SeverityError, Code: "steam.depots", Message: depotErr.Error()})
	}
	options := steam.UploadOptions{Preview: request.Preview, SetLive: request.SetLive}
	optionErr := options.Validate()
	if optionErr != nil {
		report.Issues = append(report.Issues, doctor.Issue{Severity: doctor.SeverityError, Code: "steam.options", Message: optionErr.Error()})
	}
	if depotErr == nil && optionErr == nil {
		if err := steam.ValidateScriptSelection(loaded.Document, request.Profile, request.Targets, steam.ScriptOptions{Preview: request.Preview, SetLive: request.SetLive}); err != nil {
			report.Issues = append(report.Issues, doctor.Issue{Severity: doctor.SeverityError, Code: "steam.script", Message: err.Error()})
		}
	}
	if _, err := auth.ResolveUsername(request.Username); err != nil {
		report.Issues = append(report.Issues, doctor.Issue{Severity: doctor.SeverityWarning, Code: "steam.account", Message: "Sign in to a Steam build account before uploading."})
	}
	return report, nil
}

func (a *DesktopApp) OpenConfigDialog() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Open Steam Releaser Configuration",
		Filters: []runtime.FileFilter{
			{DisplayName: "Steam Releaser YAML", Pattern: "*.yaml;*.yml"},
		},
	})
}

func (a *DesktopApp) SaveConfigDialog() (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "Create Steam Releaser Configuration", DefaultFilename: "steam-release.yaml",
		Filters: []runtime.FileFilter{{DisplayName: "Steam Releaser YAML", Pattern: "*.yaml;*.yml"}},
	})
}

func (a *DesktopApp) OpenProjectDialog() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose Game Project Directory",
	})
}

func (a *DesktopApp) LoadConfig(path, projectRoot string) (ConfigSummary, error) {
	document, err := config.LoadWithProjectRoot(path, projectRoot)
	if err != nil {
		return ConfigSummary{}, err
	}
	return summaryForDocument(document)
}

func (a *DesktopApp) DetectProject(root string) config.Detection {
	return config.Detect(strings.TrimSpace(root))
}

func (a *DesktopApp) PreviewConfig(request CreateConfigRequest) (string, error) {
	options, err := createOptions(request)
	if err != nil {
		return "", err
	}
	_, contents, err := config.Render(options)
	return string(contents), err
}

func (a *DesktopApp) CreateConfig(request CreateConfigRequest) (ConfigSummary, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy {
		return ConfigSummary{}, fmt.Errorf("another operation is already running")
	}
	options, err := createOptions(request)
	if err != nil {
		return ConfigSummary{}, err
	}
	document, err := config.Initialize(options)
	if err != nil {
		return ConfigSummary{}, err
	}
	return summaryForDocument(document)
}

func createOptions(request CreateConfigRequest) (config.InitOptions, error) {
	path := strings.TrimSpace(request.Path)
	if path == "" {
		return config.InitOptions{}, fmt.Errorf("choose a configuration path")
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		return config.InitOptions{}, fmt.Errorf("enter a project name")
	}
	engine := strings.ToLower(strings.TrimSpace(request.Engine))
	switch engine {
	case "godot", "unity", "custom", "prebuilt":
	default:
		return config.InitOptions{}, fmt.Errorf("choose a valid engine")
	}
	options := config.InitOptions{
		Path: path, Name: name, Engine: engine,
		Profile: config.DefaultProfile, AppID: request.AppID,
		Targets: desktopStarterTargets(engine),
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return config.InitOptions{}, err
	}
	projectRoot := filepath.Dir(absolutePath)
	if root := strings.TrimSpace(request.ProjectRoot); root != "" {
		projectRoot, options.Root = root, root
		if relative, err := filepath.Rel(filepath.Dir(absolutePath), root); err == nil {
			options.Root = filepath.ToSlash(relative)
		}
	}
	if len(request.Targets) > 0 {
		options.Targets = nil
		for _, target := range request.Targets {
			initTarget := config.InitTarget{
				Name: strings.TrimSpace(target.Name), Platform: target.Platform,
				DepotID: target.DepotID, BuildSetting: strings.TrimSpace(target.BuildSetting),
				Mappings: target.Mappings, Exclusions: target.Exclusions,
			}
			targetPath := projectRelative(projectRoot, strings.TrimSpace(target.Path))
			if engine == "prebuilt" {
				initTarget.Content = targetPath
			} else {
				initTarget.Output = targetPath
			}
			options.Targets = append(options.Targets, initTarget)
		}
	}
	return options, nil
}

func projectRelative(root, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(relative)
}

func (a *DesktopApp) OpenVDFDialog() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Import SteamPipe App Build Script",
		Filters: []runtime.FileFilter{{DisplayName: "SteamPipe VDF", Pattern: "*.vdf"}},
	})
}

func (a *DesktopApp) ImportVDF(path string) (steam.ImportedBuild, error) {
	return steam.ImportAppBuild(strings.TrimSpace(path))
}

func (a *DesktopApp) DiscoverBuildScripts(root string) []steam.DiscoveredBuild {
	return steam.DiscoverAppBuilds(strings.TrimSpace(root))
}

// SteamAppName returns "" when the store has no public page for the app.
func (a *DesktopApp) SteamAppName(appID uint64) string {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	name, _ := steam.AppName(ctx, appID)
	return name
}

func desktopStarterTargets(engine string) []config.InitTarget {
	targets := []config.InitTarget{
		{Name: "windows", Platform: "windows", DepotID: 1000001},
		{Name: "linux", Platform: "linux", DepotID: 1000002},
		{Name: "macos", Platform: "macos", DepotID: 1000003},
	}
	for i := range targets {
		name := targets[i].Name
		switch engine {
		case "godot":
			targets[i].Output = "builds/" + name + "/REPLACE_WITH_GAME"
			targets[i].BuildSetting = "REPLACE_WITH_" + strings.ToUpper(name) + "_PRESET"
		case "unity":
			targets[i].Output = "Builds/" + name + "/REPLACE_WITH_GAME"
			targets[i].BuildSetting = "Assets/Settings/REPLACE_WITH_" + strings.ToUpper(name) + "_PROFILE.asset"
		case "custom":
			targets[i].Output = "dist/" + name + "/REPLACE_WITH_GAME"
			targets[i].BuildSetting = "replace-with-build-command"
		default:
			targets[i].Content = "builds/" + name + "/REPLACE_WITH_CONTENT"
		}
	}
	return targets
}

// EditConfig deliberately does not parse YAML so an invalid file can still be repaired.
func (a *DesktopApp) EditConfig(path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy || a.exiting {
		return fmt.Errorf("wait for the current operation before editing configuration")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("choose a configuration file first")
	}
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return err
	}
	extension := strings.ToLower(filepath.Ext(absolute))
	if extension != ".yaml" && extension != ".yml" {
		return fmt.Errorf("choose a YAML configuration file")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return fmt.Errorf("open configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("configuration must be a regular file")
	}
	open := a.openConfigFile
	if open == nil {
		open = openConfigurationFile
	}
	if err := open(absolute); err != nil {
		return fmt.Errorf("open YAML editor: %w", err)
	}
	return nil
}

func openConfigurationFile(path string) error {
	var command *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		command = exec.Command("open", "-t", path)
	case "windows":
		command = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func summaryForDocument(document *config.Document) (ConfigSummary, error) {
	hash, err := history.ConfigHash(document.Path)
	if err != nil {
		return ConfigSummary{}, err
	}
	summary := ConfigSummary{
		Path:       document.Path,
		ConfigHash: hash,
		Project:    document.Config.Project.Name,
		Root:       document.ProjectRoot(),
		Engine:     document.Config.Project.Engine.Type,
	}
	summary.Username = desktopUsername()
	if selection, err := document.Select("", nil); err == nil {
		summary.DefaultProfile = selection.Name
	}
	for _, name := range document.ProfileNames() {
		selection, err := document.Select(name, nil)
		if err != nil {
			return ConfigSummary{}, err
		}
		profile := selection.Profile
		profileSummary := ProfileSummary{Name: name, AppID: profile.AppID}
		for _, targetName := range selection.TargetNames {
			target := profile.Targets[targetName]
			setting := target.Build.Preset
			if setting == "" {
				setting = target.Build.Profile
			}
			if setting == "" {
				setting = target.Build.Target
			}
			if setting == "" && len(target.Build.Command) > 0 {
				setting = target.Build.Command[0]
			}
			profileSummary.Targets = append(profileSummary.Targets, TargetSummary{
				Name: targetName, Platform: target.Platform, Architecture: target.Architecture,
				DepotID: target.DepotID, Output: document.ResolveProject(target.Output),
				BuildSetting: setting, BuildArgs: slices.Clone(target.Build.Args), BuildEnv: maps.Clone(target.Build.Env),
			})
		}
		summary.Profiles = append(summary.Profiles, profileSummary)
	}
	return summary, nil
}

func (a *DesktopApp) CachedUsername() string {
	return desktopUsername()
}

// SteamCMDLocation resolves the SteamCMD executable without reading any SteamCMD files.
func (a *DesktopApp) SteamCMDLocation() string {
	resolution := tools.SteamCMD(steamCMDDocument())
	if !resolution.Found {
		return ""
	}
	return resolution.Path
}

// SteamCMDCachedAccounts returns SteamCMD's saved account names once the user has allowed it.
func (a *DesktopApp) SteamCMDCachedAccounts() ([]string, error) {
	preferences, err := localstore.Preferences(nil)
	if err != nil {
		return nil, err
	}
	if preferences[localstore.SteamCMDAccountsKey] != "allow" {
		return nil, fmt.Errorf("reading SteamCMD accounts has not been allowed")
	}
	location := a.SteamCMDLocation()
	if location == "" {
		return []string{}, nil
	}
	return steam.SteamCMDCachedAccounts(location)
}

func steamCMDDocument() *config.Document {
	home, _ := os.UserHomeDir()
	return &config.Document{Path: filepath.Join(home, "steam-release.yaml")}
}

func desktopUsername() string {
	if config, err := auth.LoadCredentialFileConfig(); err == nil && config.Enabled {
		if credentials, err := auth.ReadCredentialFile(config.Path); err == nil && credentials.Username != "" {
			return credentials.Username
		}
	}
	username, _ := auth.LoadUsername()
	return username
}

func (a *DesktopApp) StartOperation(request OperationRequest) error {
	a.mu.Lock()
	if a.exiting {
		a.mu.Unlock()
		return fmt.Errorf("the application is exiting")
	}
	if a.busy {
		a.mu.Unlock()
		return fmt.Errorf("another operation is already running")
	}
	if request.RetryID != "" {
		record, err := history.Find(request.RetryID)
		if err == nil {
			request, err = history.PrepareRetryRequest(record, request.Notifications)
		}
		if err != nil {
			a.mu.Unlock()
			return err
		}
	}
	a.secrets = nil
	a.triedCredentialPassword = false
	a.steamUsername = ""
	a.credentialFile = auth.CredentialFileConfig{}
	var steamReader *os.File
	if request.Action == "login" || request.Action == "upload" || request.Action == "ship" || request.Action == "logout" {
		config, err := auth.LoadCredentialFileConfig()
		if err != nil {
			a.mu.Unlock()
			return fmt.Errorf("load credential file settings: %w", err)
		}
		a.credentialFile = config
		a.steamUsername, _ = auth.ResolveUsername(request.Username)
	}
	if request.Action == "login" || request.Action == "upload" || request.Action == "ship" {
		if request.Action == "login" {
			if err := auth.ValidateUsername(request.Username); err != nil {
				a.mu.Unlock()
				return err
			}
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			a.mu.Unlock()
			return err
		}
		steamReader = reader
		a.steamReader, a.steamWriter = reader, writer
		a.steamInput = make(chan string, 1)
		go feedSteamInput(writer, a.steamInput)
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.busy = true
	a.cancel = cancel
	done := make(chan struct{})
	a.operationDone = done
	a.mu.Unlock()
	a.emitEvent("operation:started", request.Action)
	go func() {
		defer close(done)
		a.run(ctx, request, steamReader)
	}()
	return nil
}

func feedSteamInput(writer *os.File, responses <-chan string) {
	for response := range responses {
		if _, err := io.WriteString(writer, response+"\n"); err != nil {
			return
		}
	}
}

func (a *DesktopApp) SubmitSteamInput(response string) error {
	if response == "" || len(response) > 1024 || strings.ContainsAny(response, "\r\n") {
		return fmt.Errorf("enter one password or Steam Guard response (up to 1024 characters)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.submitSteamInput(response)
}

// Called with mu held. Every response is registered for log redaction.
func (a *DesktopApp) submitSteamInput(response string) error {
	if !a.busy || a.steamInput == nil {
		return fmt.Errorf("no SteamCMD authentication is running")
	}
	select {
	case a.steamInput <- response:
		a.secrets = append(a.secrets, response)
		return nil
	default:
		return fmt.Errorf("previous SteamCMD response is still pending")
	}
}

// A manual password prevents retrying the file password during this operation.
func (a *DesktopApp) SubmitSteamPassword(password string) error {
	if password == "" || len(password) > 1024 || strings.ContainsAny(password, "\r\n\x00") {
		return fmt.Errorf("enter one Steam password (up to 1024 characters)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.submitSteamInput(password); err != nil {
		return err
	}
	if a.credentialFile.Enabled {
		a.triedCredentialPassword = true
	}
	return nil
}

// The saved password never crosses the webview bridge and is tried once per
// operation, for the matching account only. A repeated prompt falls back to UI.
func (a *DesktopApp) SubmitSavedSteamPassword() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.busy || a.steamInput == nil || !a.credentialFile.Enabled {
		return false, nil
	}
	if a.triedCredentialPassword {
		return false, nil
	}
	a.triedCredentialPassword = true
	credentials, err := auth.ReadCredentialFile(a.credentialFile.Path)
	if err != nil {
		return false, err
	}
	if credentials.Password == "" || credentials.Username != a.steamUsername {
		return false, nil
	}
	if err := a.submitSteamInput(credentials.Password); err != nil {
		return false, err
	}
	return true, nil
}

func (a *DesktopApp) CancelOperation() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *DesktopApp) run(ctx context.Context, request OperationRequest, steamInput io.Reader) {
	started := time.Now().UTC()
	var record *history.Record
	var recordMu sync.Mutex
	var logs strings.Builder
	logsTruncated := false
	var redactor history.StreamRedactor
	steamOutputTail := ""
	reporter := release.ReporterFunc(func(event release.Event) {
		recordMu.Lock()
		if record != nil {
			if event.Kind == "upload-checkpoint" {
				record.UploadFingerprint = event.Fingerprint
				recordMu.Unlock()
				return
			}
			if event.Kind == "build-complete" || event.Kind == "build-prebuilt" || event.Kind == "build-reused" {
				if !slices.Contains(record.CompletedTargets, event.Target) {
					record.CompletedTargets = append(record.CompletedTargets, event.Target)
				}
				if record.TargetFingerprints == nil {
					record.TargetFingerprints = make(map[string]string)
				}
				if event.Fingerprint == "" {
					delete(record.TargetFingerprints, event.Target)
				} else {
					record.TargetFingerprints[event.Target] = event.Fingerprint
				}
			}
			if event.Kind == "build-ready" {
				record.BuildReady = true
			}
		}
		// Identify prompts before redaction so even a password resembling prompt
		// text cannot prevent authentication. The frontend also handles old events.
		if event.Target == "SteamCMD login" || event.Target == "SteamCMD upload" {
			steamOutputTail += event.Message
			if len(steamOutputTail) > 160 {
				steamOutputTail = steamOutputTail[len(steamOutputTail)-160:]
			}
			if steamGuardPromptPattern.MatchString(steamOutputTail) {
				event.Kind, steamOutputTail = "steam-guard-prompt", ""
			} else if steamPasswordPromptPattern.MatchString(steamOutputTail) {
				event.Kind, steamOutputTail = "steam-password-prompt", ""
			}
		}
		a.mu.Lock()
		event.Message = redactor.Feed(event.Target+":"+string(event.Level), event.Message, a.secrets)
		a.mu.Unlock()
		if event.Kind == "steam-guard-prompt" {
			event.Message = "Steam Guard response required."
		} else if event.Kind == "steam-password-prompt" {
			event.Message = "Steam password required."
		}
		// Capture only redacted chunks, including before the log-size limit.
		if record != nil && event.Message != "" {
			if logs.Len()+len(event.Message) < 4<<20 {
				if strings.HasPrefix(event.Target, "SteamCMD") {
					logs.WriteString(event.Message)
				} else {
					logs.WriteString("\n[" + event.Target + "] " + event.Message + "\n")
				}
			} else {
				logsTruncated = true
			}
		}
		recordMu.Unlock()
		a.emitEvent("release:event", event)
		if request.Notifications && event.Kind == "build-ready" && request.Action == "ship" {
			a.notify("Build completed", "All selected platforms passed validation. Uploading to Steam.")
		}
	})
	load := a.loadService
	if load == nil {
		load = service.LoadWithProjectRoot
	}
	if request.ConfigPath == "" && (request.Action == "login" || request.Action == "logout") {
		load = func(_, _ string, reporter release.Reporter) (*service.Service, error) {
			return service.Standalone(reporter)
		}
	}
	loaded, err := load(request.ConfigPath, request.ProjectRoot, reporter)
	if err != nil {
		if request.Notifications {
			a.notify("Release failed", "Return to Steam Releaser to review the configuration error.")
		}
		a.finish(OperationResult{Action: request.Action, Error: err.Error()})
		return
	}
	historyWarning := ""
	if request.Action == "build" || request.Action == "ship" || request.Action == "upload" {
		selection, selectErr := loaded.Document.Select(request.Profile, request.Targets)
		if selectErr == nil {
			request.ConfigPath = loaded.Document.Path
			request.ProjectRoot = loaded.Document.ProjectRoot()
			request.Profile, request.Targets = selection.Name, selection.TargetNames
			hash := loaded.Document.ConfigHash
			commit, version, dirty := history.GitMetadataContext(ctx, request.ProjectRoot)
			// Reused targets and upload checkpoints are recorded only after the
			// service verifies them under the project lock. Carrying them forward
			// here could make a failed retry authorize stale data on a later retry.
			record = &history.Record{ID: fmt.Sprint(started.UnixNano()), Request: request, Project: loaded.Document.Config.Project.Name, Root: request.ProjectRoot, AppID: selection.Profile.AppID, StartedAt: started, Status: "running", ConfigHash: hash, Commit: commit, Version: version, Dirty: dirty, CompletedTargets: []string{}}
			record.CustomUploadScript = selection.Profile.ExistingVDF != ""
			if selection.Profile.ExistingVDF != "" {
				record.UploadFingerprint = ""
			}
			if saveErr := history.Save(*record); saveErr != nil {
				historyWarning = saveErr.Error()
			}
		}
	}
	runOptions := service.RunOptions{
		Targets: request.Targets, ReuseTargets: request.ReuseTargets,
		TargetFingerprints: request.TargetFingerprints, UploadFingerprint: request.UploadFingerprint,
		UnverifiedTargets: request.UnverifiedTargets, CaptureFingerprints: record != nil,
	}
	if request.RetrySnapshot != nil {
		snapshot := *request.RetrySnapshot
		runOptions.VerifyRetry = func() error {
			if loaded.Document.ConfigHash != snapshot.ConfigHash ||
				filepath.Clean(loaded.Document.Path) != filepath.Clean(snapshot.ConfigPath) ||
				filepath.Clean(loaded.Document.ProjectRoot()) != filepath.Clean(snapshot.Root) {
				return fmt.Errorf("configuration or project root changed since this attempt; start a new release")
			}
			return history.VerifyRetrySnapshotContext(ctx, snapshot)
		}
	}
	if record != nil {
		runOptions.OnLocked = func(lockedCtx context.Context) error {
			configHash, err := history.ConfigHash(loaded.Document.Path)
			if err != nil {
				return err
			}
			if configHash != loaded.Document.ConfigHash {
				return fmt.Errorf("configuration changed while starting the operation; reload it and start again")
			}
			commit, version, dirty := history.GitMetadataContext(lockedCtx, request.ProjectRoot)
			if err := lockedCtx.Err(); err != nil {
				return err
			}
			sourceHash := ""
			var sourceErr error
			if request.RetrySnapshot != nil && request.RetrySnapshot.SourceHash != "" {
				if commit != request.RetrySnapshot.Commit {
					return fmt.Errorf("Git commit changed while starting the retry; start a new release")
				}
				// VerifyRetry already checked this fingerprint under the same lock.
				sourceHash = request.RetrySnapshot.SourceHash
			} else if commit != "" {
				reporter.Report(release.Event{Level: release.LevelInfo, Message: "Recording source contents for retry safety"})
				sourceHash, sourceErr = history.SourceHashContext(lockedCtx, request.ProjectRoot)
				if err := lockedCtx.Err(); err != nil {
					return err
				}
				if sourceErr != nil {
					sourceHash = "unavailable"
				}
				afterCommit, _, _ := history.GitMetadataContext(lockedCtx, request.ProjectRoot)
				if err := lockedCtx.Err(); err != nil {
					return err
				}
				if commit != afterCommit {
					return fmt.Errorf("Git commit changed while recording source; start again")
				}
			}
			configHash, err = history.ConfigHash(loaded.Document.Path)
			if err != nil {
				return err
			}
			if configHash != loaded.Document.ConfigHash {
				return fmt.Errorf("configuration changed while recording source; reload it and start again")
			}
			recordMu.Lock()
			record.Commit, record.Version, record.Dirty, record.SourceHash = commit, version, dirty, sourceHash
			recordMu.Unlock()
			if sourceErr != nil {
				reporter.Report(release.Event{Level: release.LevelWarning, Message: "Could not record a source checkpoint; automatic retry may require a new operation: " + sourceErr.Error()})
			}
			return nil
		}
	}
	uploadOptions := steam.UploadOptions{
		Username: request.Username,
		Preview:  request.Preview,
		SetLive:  request.SetLive,
		Input:    steamInput,
	}
	var result any
	switch request.Action {
	case "doctor":
		typedReport, doctorErr := a.CheckReadiness(request)
		result, err = typedReport, doctorErr
		if err == nil && !typedReport.OK() {
			err = fmt.Errorf("doctor found one or more blocking issues")
		}
	case "plan":
		result, err = loaded.Plan(request.Profile, runOptions, uploadOptions)
	case "build":
		err = loaded.Build(ctx, request.Profile, runOptions)
	case "upload":
		result, err = loaded.Upload(ctx, request.Profile, runOptions, uploadOptions)
	case "ship":
		result, err = loaded.Ship(ctx, request.Profile, runOptions, uploadOptions)
	case "login":
		err = loaded.LoginWithInput(ctx, request.Username, steamInput)
		if err == nil {
			result = map[string]string{"username": request.Username}
		}
	case "logout":
		err = loaded.Logout(ctx, request.Username, false)
	default:
		err = fmt.Errorf("unknown operation %q", request.Action)
	}
	finished := OperationResult{Action: request.Action, OK: err == nil, Data: result}
	if err != nil {
		finished.Error = err.Error()
	}
	if record != nil {
		recordMu.Lock()
		ended := time.Now().UTC()
		record.FinishedAt, record.DurationMS = &ended, ended.Sub(started).Milliseconds()
		record.Status = "complete"
		if err != nil {
			record.Status = "failed"
		}
		if err != nil && ctx.Err() != nil {
			record.Status = "cancelled"
		}
		a.mu.Lock()
		secrets := append([]string{}, a.secrets...)
		a.mu.Unlock()
		rawLogs := logs.String()
		if logsTruncated && len(rawLogs) > 1024 {
			rawLogs = rawLogs[:len(rawLogs)-1024] + "\n[output omitted]"
		}
		record.Logs, record.Error = history.Sanitize(rawLogs, secrets), history.Sanitize(finished.Error, secrets)
		if upload, ok := result.(steam.UploadResult); ok {
			record.BuildID = upload.BuildID
		}
		if saveErr := history.Save(*record); saveErr != nil {
			historyWarning = saveErr.Error()
		} else {
			historyWarning = ""
		}
		finished.History, finished.HistoryWarning = record, historyWarning
		recordMu.Unlock()
	}
	if request.Notifications && ctx.Err() == nil && (record != nil || request.Action == "login") {
		title := "Release completed"
		body := "Your selected operation completed successfully."
		if request.Action == "build" {
			title = "Build completed"
		}
		if request.Action == "login" {
			title = "Steam sign-in completed"
		}
		if request.Preview {
			body = "Preview completed; no content was published."
		}
		if err != nil {
			title, body = "Release failed", "Return to Steam Releaser to review the error and retry."
		}
		a.notify(title, body)
	}
	a.finish(finished)
}

var steamGuardPromptPattern = regexp.MustCompile(`(?i)steam guard|authenticator|two.factor|guard code|verification code`)
var steamPasswordPromptPattern = regexp.MustCompile(`(?i)password\s*:|enter (?:your )?password`)

func (a *DesktopApp) finish(result OperationResult) {
	a.mu.Lock()
	a.steamUsername = ""
	a.triedCredentialPassword = false
	a.credentialFile = auth.CredentialFileConfig{}
	result.Error = history.Sanitize(result.Error, a.secrets)
	if upload, ok := result.Data.(steam.UploadResult); ok {
		upload.Output = history.Sanitize(upload.Output, a.secrets)
		result.Data = upload
	}
	a.busy = false
	a.cancel = nil
	a.secrets = nil
	if a.steamInput != nil {
		close(a.steamInput)
		a.steamInput = nil
	}
	if a.steamReader != nil {
		_ = a.steamReader.Close()
		a.steamReader = nil
	}
	if a.steamWriter != nil {
		_ = a.steamWriter.Close()
		a.steamWriter = nil
	}
	a.mu.Unlock()
	a.emitEvent("operation:finished", result)
}
