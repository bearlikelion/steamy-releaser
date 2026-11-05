package doctor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/thornity/steam-release-tool/internal/artifact"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/engine"
	"github.com/thornity/steam-release-tool/internal/tools"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type Issue struct {
	Severity Severity `json:"severity" yaml:"severity"`
	Code     string   `json:"code" yaml:"code"`
	Message  string   `json:"message" yaml:"message"`
	Target   string   `json:"target,omitempty" yaml:"target,omitempty"`
}

type Report struct {
	Project string  `json:"project" yaml:"project"`
	Profile string  `json:"profile,omitempty" yaml:"profile,omitempty"`
	Issues  []Issue `json:"issues" yaml:"issues"`
}

func (r Report) OK() bool {
	for _, issue := range r.Issues {
		if issue.Severity == SeverityError {
			return false
		}
	}
	return true
}

func Run(document *config.Document, profileName string, selected []string) (Report, error) {
	report := Report{Project: document.Config.Project.Name, Profile: profileName}
	selection, err := document.Select(profileName, selected)
	if err != nil {
		return report, err
	}
	report.Profile = selection.Name
	profile, targetNames := selection.Profile, selection.TargetNames
	add := func(severity Severity, code, message, target string) {
		report.Issues = append(report.Issues, Issue{Severity: severity, Code: code, Message: message, Target: target})
	}

	root := document.ProjectRoot()
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		add(SeverityError, "project.root", "Project root does not exist: "+root, "")
	}
	steamCMD := tools.SteamCMD(document)
	if !steamCMD.Found {
		add(SeverityError, "tool.steamcmd", "SteamCMD was not found; configure tools.steamcmd or STEAMCMD_BIN", "")
	} else {
		add(SeverityInfo, "tool.steamcmd", "SteamCMD: "+steamCMD.Path, "")
	}

	switch document.Config.Project.Engine.Type {
	case "godot":
		godot := tools.Godot(document)
		if !godot.Found {
			add(SeverityError, "tool.godot", "Godot was not found; configure project.engine.executable or GODOT_BIN", "")
		} else {
			add(SeverityInfo, "tool.godot", "Godot: "+godot.Path, "")
		}
		if _, statErr := os.Stat(filepath.Join(root, "project.godot")); statErr != nil {
			add(SeverityError, "project.godot", "project.godot was not found in the project root", "")
		}
		presets := godotPresets(filepath.Join(root, "export_presets.cfg"))
		for _, name := range targetNames {
			target := profile.Targets[name]
			if !presets[target.Build.Preset] {
				add(SeverityError, "godot.preset", fmt.Sprintf("Export preset %q was not found", target.Build.Preset), name)
			}
			if warning := engine.CrossHostWarning(target); warning != "" {
				add(SeverityWarning, "godot.cross_host", warning, name)
			}
		}
	case "unity":
		unity := tools.Unity(document)
		if !unity.Found {
			add(SeverityError, "tool.unity", "Unity Editor was not found; configure project.engine.executable or UNITY_BIN with the Editor executable", "")
		} else {
			add(SeverityInfo, "tool.unity", "Unity Editor: "+unity.Path, "")
		}
		if err := engine.ValidateUnityProjectRoot(root); err != nil {
			add(SeverityError, "project.unity", err.Error(), "")
		}
		for _, name := range targetNames {
			target := profile.Targets[name]
			if target.Build.Profile != "" {
				path := document.ResolveProject(target.Build.Profile)
				if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
					add(SeverityError, "unity.profile", "Unity Build Profile asset was not found: "+path, name)
				}
			}
			if unity.Found && target.Build.Target != "" && !unityModuleLikelyPresent(unity.Path, target.Build.Target) {
				add(SeverityWarning, "unity.module", "The Unity platform module could not be confirmed for "+target.Build.Target, name)
			}
		}
	case "custom":
		plan, err := engine.Plan(document, selection.Name, targetNames)
		if err != nil {
			return report, err
		}
		for _, target := range plan.Targets {
			resolution := tools.Custom(document, target.Command.Program)
			if !resolution.Found {
				add(SeverityError, "tool.custom", "Custom build executable was not found: "+resolution.Path, target.Name)
			}
		}
	case "prebuilt":
		add(SeverityInfo, "engine.prebuilt", "Build step is disabled; existing artifacts will be used", "")
	}

	for _, name := range targetNames {
		target := profile.Targets[name]
		for _, warning := range target.Build.ArgumentWarnings(document.Config.Project.Engine.Type) {
			add(SeverityWarning, "build.args", warning, name)
		}
		if document.Config.Project.Engine.Type == "prebuilt" {
			if err := artifact.Validate(document, selection.Name, []string{name}); err != nil {
				add(SeverityError, "artifact.invalid", err.Error(), name)
			}
			continue
		}
		if _, statErr := os.Stat(document.ResolveProject(target.Output)); statErr != nil {
			add(SeverityWarning, "artifact.missing", "Build artifact is not present yet: "+document.ResolveProject(target.Output), name)
		}
	}
	return report, nil
}

func godotPresets(path string) map[string]bool {
	result := map[string]bool{}
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		value, ok := strings.CutPrefix(line, "name=")
		if !ok {
			continue
		}
		if unquoted, unquoteErr := strconv.Unquote(value); unquoteErr == nil {
			result[unquoted] = true
		}
	}
	return result
}

func unityModuleLikelyPresent(executable, target string) bool {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	var playbackRoot string
	if runtime.GOOS == "darwin" {
		playbackRoot = filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "PlaybackEngines"))
	} else {
		playbackRoot = filepath.Join(filepath.Dir(executable), "Data", "PlaybackEngines")
	}
	module := ""
	lower := strings.ToLower(target)
	switch {
	case strings.Contains(lower, "windows"):
		module = "WindowsStandaloneSupport"
	case strings.Contains(lower, "linux"):
		module = "LinuxStandaloneSupport"
	case strings.Contains(lower, "osx"), strings.Contains(lower, "mac"):
		module = "MacStandaloneSupport"
	default:
		return true
	}
	info, err := os.Stat(filepath.Join(playbackRoot, module))
	return err == nil && info.IsDir()
}
