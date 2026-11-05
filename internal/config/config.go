package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

func Load(path string) (*Document, error) {
	return LoadWithProjectRoot(path, "")
}

// LoadWithProjectRoot applies a machine-local project directory without changing
// the shared YAML file. A relative override is resolved from the process cwd.
func LoadWithProjectRoot(path, projectRoot string) (*Document, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve configuration path: %w", err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, fmt.Errorf("open configuration %s: %w", absolute, err)
	}
	defer file.Close()
	return decodeWithProjectRoot(absolute, file, projectRoot)
}

func decode(path string, reader io.Reader) (*Document, error) {
	return decodeWithProjectRoot(path, reader, "")
}

func decodeWithProjectRoot(path string, reader io.Reader, projectRoot string) (*Document, error) {
	digest := sha256.New()
	decoder := yaml.NewDecoder(io.TeeReader(reader, digest))
	decoder.KnownFields(true)
	value := fileConfig{Config: Config{Version: CurrentVersion}}
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("parse configuration %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("configuration contains more than one YAML document")
		}
		return nil, fmt.Errorf("parse configuration: %w", err)
	}

	if value.Profile != nil {
		if value.Profiles != nil {
			return nil, errors.New("use top-level app_id and targets or named profiles, not both")
		}
		value.Profiles = map[string]Profile{DefaultProfile: *value.Profile}
	}
	if strings.TrimSpace(projectRoot) != "" {
		absoluteRoot, err := filepath.Abs(projectRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve project root override: %w", err)
		}
		info, err := os.Stat(absoluteRoot)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("project root override is not a directory: %s", absoluteRoot)
		}
		value.Project.Root = absoluteRoot
	}
	document := &Document{Path: path, Config: value.Config, ConfigHash: hex.EncodeToString(digest.Sum(nil))}
	if err := document.applyDefaults(); err != nil {
		return nil, err
	}
	if err := document.Validate(); err != nil {
		return nil, err
	}
	return document, nil
}

func Discover(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(current); statErr == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		for _, name := range []string{"steam-release.yaml", "steam-release.yml"} {
			candidate := filepath.Join(current, name)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate, nil
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", errors.New("steam-release.yaml was not found in this directory or a parent")
}

func (d *Document) Validate() error {
	if d.Config.Version != CurrentVersion {
		return fmt.Errorf("unsupported configuration version %d; expected %d", d.Config.Version, CurrentVersion)
	}
	if strings.TrimSpace(d.Config.Project.Name) == "" {
		return errors.New("project.name cannot be empty")
	}
	engine := strings.ToLower(strings.TrimSpace(d.Config.Project.Engine.Type))
	switch engine {
	case "godot", "unity", "custom", "prebuilt":
	default:
		return fmt.Errorf("project.engine.type must be godot, unity, custom, or prebuilt; got %q", engine)
	}
	d.Config.Project.Engine.Type = engine
	if len(d.Config.Profiles) == 0 {
		return errors.New("define app_id and targets, or at least one named profile")
	}

	for profileName, profile := range d.Config.Profiles {
		if strings.TrimSpace(profileName) == "" {
			return errors.New("profile names cannot be empty")
		}
		if !safeName(profileName) {
			return fmt.Errorf("profile name %q may only contain letters, numbers, spaces, dots, underscores, and hyphens", profileName)
		}
		if profile.AppID == 0 {
			return fmt.Errorf("profile %q has an invalid app_id", profileName)
		}
		if len(profile.Targets) == 0 {
			return fmt.Errorf("profile %q must contain at least one target", profileName)
		}
		depotIDs := map[uint64]string{}
		for targetName, target := range profile.Targets {
			if err := target.Build.ValidateProcessOptions(engine); err != nil {
				return fmt.Errorf("profile %q target %q: %w", profileName, targetName, err)
			}
			if strings.TrimSpace(targetName) == "" {
				return fmt.Errorf("profile %q contains a target with an empty name", profileName)
			}
			if !safeName(targetName) {
				return fmt.Errorf("target name %q may only contain letters, numbers, spaces, dots, underscores, and hyphens", targetName)
			}
			if target.DepotID == 0 {
				return fmt.Errorf("target %q in profile %q has an invalid depot_id", targetName, profileName)
			}
			if other, exists := depotIDs[target.DepotID]; exists {
				previous := profile.Targets[other]
				if d.ResolveProject(previous.Content) != d.ResolveProject(target.Content) {
					return fmt.Errorf("targets %q and %q in profile %q share depot_id %d but have different content directories", other, targetName, profileName, target.DepotID)
				}
				if len(previous.Mappings) == 0 || len(target.Mappings) == 0 {
					return fmt.Errorf("targets %q and %q in profile %q share depot_id %d and must define separate mappings", other, targetName, profileName, target.DepotID)
				}
			} else {
				depotIDs[target.DepotID] = targetName
			}
			if strings.TrimSpace(target.Output) == "" {
				if engine == "prebuilt" {
					return fmt.Errorf("prebuilt target %q must define a content directory", targetName)
				}
				return fmt.Errorf("target %q must define output for %s builds", targetName, engine)
			}
			if strings.TrimSpace(target.Content) == "" {
				return fmt.Errorf("target %q has an empty content directory", targetName)
			}
			switch engine {
			case "godot":
				if strings.TrimSpace(target.Build.Preset) == "" {
					return fmt.Errorf("Godot target %q must define build.preset", targetName)
				}
			case "unity":
				if strings.TrimSpace(target.Build.Target) == "" && strings.TrimSpace(target.Build.Profile) == "" {
					return fmt.Errorf("Unity target %q must define build.target or build.profile", targetName)
				}
			case "custom":
				if len(target.Build.Command) == 0 || strings.TrimSpace(target.Build.Command[0]) == "" {
					return fmt.Errorf("custom target %q must define build.command", targetName)
				}
			}
		}
		for _, rule := range profile.PostBuild {
			if strings.TrimSpace(rule.From) == "" {
				return fmt.Errorf("profile %q contains a post_build rule without from", profileName)
			}
			for _, targetName := range rule.Targets {
				if _, exists := profile.Targets[targetName]; !exists {
					return fmt.Errorf("profile %q post_build rule references unknown target %q", profileName, targetName)
				}
			}
		}
	}
	return nil
}

func safeName(value string) bool {
	if value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case ' ', '.', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func (d *Document) BaseDir() string {
	return filepath.Dir(d.Path)
}

func (d *Document) ProjectRoot() string {
	root := d.Config.Project.Root
	if root == "" {
		root = "."
	}
	return d.ResolveFromBase(root)
}

func (d *Document) ResolveFromBase(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(d.BaseDir(), path))
}

func (d *Document) ResolveProject(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(d.ProjectRoot(), path))
}

type Selection struct {
	Name        string
	Profile     Profile
	TargetNames []string
}

func (d *Document) ProfileNames() []string {
	names := make([]string, 0, len(d.Config.Profiles))
	for name := range d.Config.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Select resolves the default profile and validates target names in one place.
func (d *Document) Select(name string, targets []string) (Selection, error) {
	if name == "" {
		if _, exists := d.Config.Profiles[DefaultProfile]; exists {
			name = DefaultProfile
		} else if len(d.Config.Profiles) == 1 {
			name = d.ProfileNames()[0]
		} else {
			return Selection{}, fmt.Errorf("choose a profile: %s", strings.Join(d.ProfileNames(), ", "))
		}
	}
	profile, exists := d.Config.Profiles[name]
	if !exists {
		return Selection{}, fmt.Errorf("unknown release profile %q", name)
	}
	names, err := selectTargets(profile, targets)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Name: name, Profile: profile, TargetNames: names}, nil
}

func selectTargets(profile Profile, selected []string) ([]string, error) {
	if len(selected) == 0 {
		names := make([]string, 0, len(profile.Targets))
		for name := range profile.Targets {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(selected))
	for _, name := range selected {
		if _, exists := profile.Targets[name]; !exists {
			return nil, fmt.Errorf("profile does not contain target %q", name)
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}
