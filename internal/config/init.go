package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type InitOptions struct {
	Path     string
	Name     string
	Root     string
	Engine   string
	AppID    uint64
	Profile  string
	SteamCMD string
	Targets  []InitTarget
	Force    bool
}

type InitTarget struct {
	Name         string
	Platform     string
	Architecture string
	DepotID      uint64
	Output       string
	Content      string
	BuildSetting string
	BuildArgs    []string
	BuildEnv     map[string]string
	Mappings     []FileMapping
	Exclusions   []string
}

func Initialize(options InitOptions) (*Document, error) {
	document, contents, err := Render(options)
	if err != nil {
		return nil, err
	}
	absolutePath := document.Path
	if !options.Force {
		if _, statErr := os.Stat(absolutePath); statErr == nil {
			return nil, fmt.Errorf("configuration already exists: %s (use --force to replace it)", absolutePath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return nil, err
	}
	if options.Force {
		file, unlock, err := openLockedConfiguration(absolutePath, os.O_RDWR)
		if errors.Is(err, os.ErrNotExist) {
			if err := createConfiguration(absolutePath, contents); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			writeErr := replaceConfigurationContents(file, absolutePath, contents)
			unlock()
			closeErr := file.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
	} else {
		if err := createConfiguration(absolutePath, contents); err != nil {
			return nil, err
		}
	}
	return document, nil
}

// Render builds the annotated YAML for options without writing it.
func Render(options InitOptions) (*Document, []byte, error) {
	if options.Path == "" {
		options.Path = "steam-release.yaml"
	}
	absolutePath, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, nil, err
	}
	if options.Root == "." {
		options.Root = ""
	}
	projectRoot := options.Root
	if !filepath.IsAbs(projectRoot) {
		projectRoot = filepath.Join(filepath.Dir(absolutePath), projectRoot)
	}
	if options.Profile == "" {
		options.Profile = DefaultProfile
	}
	options.Engine = strings.ToLower(strings.TrimSpace(options.Engine))
	if options.Engine == "" || options.Engine == "auto" {
		options.Engine = detectEngine(projectRoot)
	}
	engine := Engine{Type: options.Engine}
	profile := Profile{AppID: options.AppID, Targets: map[string]Target{}}
	for _, value := range options.Targets {
		if _, exists := profile.Targets[value.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate target %q", value.Name)
		}
		profile.Targets[value.Name] = TargetFromInit(engine.Type, value)
	}
	value := fileConfig{
		Config: Config{
			Project: Project{Name: options.Name, Root: options.Root, Engine: engine},
			Tools:   Tools{SteamCMD: options.SteamCMD},
		},
	}
	value.Profiles = map[string]Profile{options.Profile: profile}
	contents, err := annotatedConfig(value)
	if err != nil {
		return nil, nil, err
	}
	document, err := decode(absolutePath, bytes.NewReader(contents))
	if err != nil {
		return nil, nil, fmt.Errorf("cannot initialize configuration: %w", err)
	}
	return document, contents, nil
}

// TargetFromInit applies the same engine-specific target defaults for CLI and desktop setup.
func TargetFromInit(engine string, value InitTarget) Target {
	target := Target{
		Platform: value.Platform, Architecture: value.Architecture,
		DepotID: value.DepotID, Output: value.Output, Content: value.Content,
		Mappings: slices.Clone(value.Mappings), Exclusions: slices.Clone(value.Exclusions),
	}
	target.Build.Args = slices.Clone(value.BuildArgs)
	target.Build.Env = maps.Clone(value.BuildEnv)
	switch engine {
	case "godot":
		target.Build.Preset = value.BuildSetting
	case "unity":
		if strings.HasSuffix(strings.ToLower(value.BuildSetting), ".asset") {
			target.Build.Profile = value.BuildSetting
		} else {
			target.Build.Target = value.BuildSetting
		}
	case "custom":
		if value.BuildSetting != "" {
			target.Build.Command = []string{value.BuildSetting}
		}
	case "prebuilt":
		if target.Content == "" {
			target.Content, target.Output = target.Output, ""
		}
	}
	return target
}

func detectEngine(root string) string {
	if _, err := os.Stat(filepath.Join(root, "project.godot")); err == nil {
		return "godot"
	}
	if _, err := os.Stat(filepath.Join(root, "ProjectSettings", "ProjectVersion.txt")); err == nil {
		return "unity"
	}
	return "prebuilt"
}
