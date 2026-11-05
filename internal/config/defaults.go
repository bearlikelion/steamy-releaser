package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (d *Document) applyDefaults() error {
	project := &d.Config.Project
	if project.Name == "" {
		project.Name = filepath.Base(d.ProjectRoot())
	}
	project.Engine.Type = strings.ToLower(strings.TrimSpace(project.Engine.Type))
	if project.Engine.Type == "" || project.Engine.Type == "auto" {
		engine, err := inferEngine(d.Config.Profiles)
		if err != nil {
			return err
		}
		project.Engine.Type = engine
	}
	for profileName, profile := range d.Config.Profiles {
		for name, target := range profile.Targets {
			if target.Platform == "" {
				target.Platform = platformFromName(name)
			}
			if target.Output == "" && project.Engine.Type == "prebuilt" {
				target.Output = target.Content
			}
			if target.Content == "" && target.Output != "" {
				target.Content = filepath.ToSlash(filepath.Dir(target.Output))
				// An inferred upload directory must not contain the project itself.
				relative, err := filepath.Rel(d.ResolveProject(target.Content), d.ProjectRoot())
				if err == nil && filepath.IsLocal(relative) {
					return fmt.Errorf("target %q in profile %q must set content explicitly when the output's parent contains the project root", name, profileName)
				}
			}
			profile.Targets[name] = target
		}
	}
	return nil
}

func inferEngine(profiles map[string]Profile) (string, error) {
	engines := map[string]bool{}
	hasArgs := false
	hasEnv := false
	for _, profile := range profiles {
		for _, target := range profile.Targets {
			build := target.Build
			if build.Preset != "" {
				engines["godot"] = true
			}
			if build.Target != "" || build.Profile != "" || build.ExecuteMethod != "" {
				engines["unity"] = true
			}
			if len(build.Command) > 0 {
				engines["custom"] = true
			}
			hasArgs = hasArgs || len(build.Args) > 0
			hasEnv = hasEnv || len(build.Env) > 0
		}
	}
	if len(engines) > 1 {
		return "", fmt.Errorf("build settings mix engines; use one engine per configuration")
	}
	for engine := range engines {
		return engine, nil
	}
	if hasArgs {
		return "", fmt.Errorf("build.args needs a build.preset, build.target, build.profile, or build.command")
	}
	if hasEnv {
		return "", fmt.Errorf("build.env needs a build.preset, build.target, build.profile, or build.command")
	}
	return "prebuilt", nil
}

func platformFromName(name string) string {
	name = strings.ToLower(name)
	for _, platform := range []string{"windows", "linux", "macos"} {
		if name == platform || strings.HasPrefix(name, platform+"-") || strings.HasPrefix(name, platform+"_") {
			return platform
		}
	}
	return ""
}
