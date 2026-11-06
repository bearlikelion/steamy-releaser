package config

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Detection struct {
	Engine   string           `json:"engine"`
	Name     string           `json:"name"`
	Presets  []DetectedPreset `json:"presets"`
	Profiles []string         `json:"profiles"`
	Existing string           `json:"existing"`
}

type DetectedPreset struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Output   string `json:"output"`
}

// Detect inspects a game folder so a new configuration can start from its real settings.
func Detect(root string) Detection {
	detection := Detection{Engine: detectEngine(root), Name: filepath.Base(root), Presets: []DetectedPreset{}, Profiles: []string{}}
	for _, name := range []string{"steam-release.yaml", "steam-release.yml"} {
		if info, err := os.Stat(filepath.Join(root, name)); err == nil && info.Mode().IsRegular() {
			detection.Existing = filepath.Join(root, name)
			break
		}
	}
	switch detection.Engine {
	case "godot":
		if name := iniValue(filepath.Join(root, "project.godot"), "config/name"); name != "" {
			detection.Name = name
		}
		detection.Presets = godotExportPresets(filepath.Join(root, "export_presets.cfg"))
	case "unity":
		if name := unityProductName(filepath.Join(root, "ProjectSettings", "ProjectSettings.asset")); name != "" {
			detection.Name = name
		}
		matches, _ := filepath.Glob(filepath.Join(root, "Assets", "Settings", "Build Profiles", "*.asset"))
		for _, match := range matches {
			if relative, err := filepath.Rel(root, match); err == nil {
				detection.Profiles = append(detection.Profiles, filepath.ToSlash(relative))
			}
		}
		slices.Sort(detection.Profiles)
	}
	return detection
}

func godotExportPresets(path string) []DetectedPreset {
	presets := []DetectedPreset{}
	file, err := os.Open(path)
	if err != nil {
		return presets
	}
	defer file.Close()
	var current *DetectedPreset
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			current = nil
			section := strings.Trim(line, "[]")
			if index, ok := strings.CutPrefix(section, "preset."); ok && !strings.Contains(index, ".") {
				presets = append(presets, DetectedPreset{})
				current = &presets[len(presets)-1]
			}
			continue
		}
		if current == nil {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value, err := strconv.Unquote(raw)
		if err != nil {
			continue
		}
		switch key {
		case "name":
			current.Name = value
		case "platform":
			current.Platform = godotSteamPlatform(value)
		case "export_path":
			current.Output = value
		}
	}
	return slices.DeleteFunc(presets, func(preset DetectedPreset) bool {
		return preset.Name == "" || preset.Platform == ""
	})
}

func godotSteamPlatform(platform string) string {
	switch platform {
	case "Windows Desktop":
		return "windows"
	case "Linux", "Linux/X11":
		return "linux"
	case "macOS", "Mac OSX":
		return "macos"
	}
	return ""
}

func iniValue(path, key string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(contents)) {
		if raw, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			if value, err := strconv.Unquote(raw); err == nil {
				return value
			}
		}
	}
	return ""
}

func unityProductName(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(contents)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "productName:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
