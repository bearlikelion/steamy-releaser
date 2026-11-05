package engine

import (
	"fmt"
	"maps"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/tools"
)

func Plan(document *config.Document, profileName string, selected []string) (*release.Plan, error) {
	selection, err := document.Select(profileName, selected)
	if err != nil {
		return nil, err
	}
	profileName, profile, names := selection.Name, selection.Profile, selection.TargetNames
	engine := document.Config.Project.Engine
	plan := &release.Plan{
		Project:     document.Config.Project.Name,
		Profile:     profileName,
		Engine:      engine.Type,
		Parallelism: 1,
		Targets:     make([]release.TargetPlan, 0, len(names)),
		Upload: release.UploadPlan{
			AppID:       profile.AppID,
			DepotIDs:    make([]uint64, 0, len(names)),
			BuildScript: document.ResolveProject(filepath.Join(".steam-releaser", "generated", profileName, "<run-id>", "app_build.vdf")),
		},
	}
	if profile.ExistingVDF != "" {
		plan.Upload.BuildScript = document.ResolveFromBase(profile.ExistingVDF)
	}

	var program string
	switch engine.Type {
	case "godot":
		program = tools.Godot(document).Path
		plan.Parallelism = len(names)
		if engine.ShouldImport() {
			plan.Prepare = []release.CommandPlan{{
				Label:      "Godot import",
				Program:    program,
				Args:       []string{"--headless", "--path", document.ProjectRoot(), "--import"},
				WorkingDir: document.ProjectRoot(),
			}}
		}
	case "unity":
		program = tools.Unity(document).Path
		// Unity opens one Editor per target and must not access a project concurrently.
	case "custom", "prebuilt":
	default:
		return nil, fmt.Errorf("unsupported engine %q", engine.Type)
	}
	if (engine.Type == "godot" || engine.Type == "custom") && engine.MaxParallel > 0 {
		plan.Parallelism = engine.MaxParallel
	}
	plan.Parallelism = max(1, min(plan.Parallelism, len(names)))

	seenDepots := map[uint64]bool{}
	for _, name := range names {
		target := profile.Targets[name]
		plan.Targets = append(plan.Targets, release.TargetPlan{
			Name:         name,
			Platform:     strings.ToLower(target.Platform),
			Architecture: target.Architecture,
			DepotID:      target.DepotID,
			Output:       document.ResolveProject(target.Output),
			Content:      document.ResolveProject(target.Content),
			Command:      buildCommand(document, profileName, name, target, program),
		})
		if !seenDepots[target.DepotID] {
			plan.Upload.DepotIDs = append(plan.Upload.DepotIDs, target.DepotID)
			seenDepots[target.DepotID] = true
		}
	}
	if err := CheckBuildPaths(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func buildCommand(document *config.Document, profileName, name string, target config.Target, program string) *release.CommandPlan {
	root := document.ProjectRoot()
	output := document.ResolveProject(target.Output)
	command := &release.CommandPlan{Label: name, Program: program, WorkingDir: root,
		Environment: maps.Clone(target.Build.Env), Warnings: target.Build.ArgumentWarnings(document.Config.Project.Engine.Type)}
	switch document.Config.Project.Engine.Type {
	case "godot":
		command.Args = append([]string{"--headless", "--path", root, "--export-release", target.Build.Preset, output}, target.Build.Args...)
	case "unity":
		command.Args = unityArgs(document, profileName, name, target)
	case "custom":
		// Replace in one pass so placeholder-like text in paths stays literal.
		replacer := strings.NewReplacer(
			"{project}", root,
			"{profile}", profileName,
			"{target}", name,
			"{output}", output,
			"{content}", document.ResolveProject(target.Content),
		)
		parts := expandAll(target.Build.Command, replacer)
		command.Program = tools.Custom(document, parts[0]).Path
		command.Args = append(parts[1:], expandAll(target.Build.Args, replacer)...)
	case "prebuilt":
		return nil
	}
	return command
}

func unityArgs(document *config.Document, profileName, name string, target config.Target) []string {
	output := document.ResolveProject(target.Output)
	logPath := document.ResolveProject(filepath.Join(".steam-releaser", "logs", profileName, name+".log"))
	args := []string{"-batchmode", "-quit", "-projectPath", document.ProjectRoot(), "-logFile", logPath}
	if target.Build.NoGraphics == nil || *target.Build.NoGraphics {
		args = append(args, "-nographics")
	}
	method := target.Build.ExecuteMethod
	if method == "" {
		method = document.Config.Project.Engine.ExecuteMethod
	}
	if target.Build.Profile != "" {
		args = append(args, "-activeBuildProfile", filepath.ToSlash(target.Build.Profile))
	} else {
		args = append(args, "-buildTarget", target.Build.Target)
	}
	if method == "" {
		buildArgument := "-build"
		if target.Build.Profile == "" {
			buildArgument = unityTargetBuildArgument(target.Build.Target)
		}
		args = append(args, buildArgument, output)
	} else {
		args = append(args,
			"-executeMethod", method,
			"-steamReleaserOutput", output,
			"-steamReleaserProfile", profileName,
			"-steamReleaserTarget", name,
		)
	}
	return append(args, target.Build.Args...)
}

func unityTargetBuildArgument(target string) string {
	switch strings.ToLower(target) {
	case "standalonelinux64", "linux64":
		return "-buildLinux64Player"
	case "standalonewindows64", "win64":
		return "-buildWindows64Player"
	case "standalonewindows", "win":
		return "-buildWindowsPlayer"
	case "standaloneosx", "osxuniversal":
		return "-buildOSXUniversalPlayer"
	case "linuxheadlesssimulation":
		return "-buildLinuxHeadlessSimulation"
	default:
		return "-build"
	}
}

func expandAll(values []string, replacer *strings.Replacer) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = replacer.Replace(value)
	}
	return result
}

func CrossHostWarning(target config.Target) string {
	if runtime.GOOS == "windows" && strings.EqualFold(target.Platform, "macos") && strings.HasSuffix(strings.ToLower(target.Output), ".app") {
		return "Godot .app bundles exported on Windows do not retain executable bits; configure a .zip output or repair permissions on Linux/macOS"
	}
	return ""
}
