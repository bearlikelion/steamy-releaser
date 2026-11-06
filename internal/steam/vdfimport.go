package steam

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/thornity/steam-release-tool/internal/config"
)

type ImportedBuild struct {
	AppID       uint64          `json:"app_id"`
	Description string          `json:"description"`
	ContentRoot string          `json:"content_root"`
	Depots      []ImportedDepot `json:"depots"`
}

type ImportedDepot struct {
	DepotID    uint64               `json:"depot_id"`
	Platform   string               `json:"platform"`
	Content    string               `json:"content"`
	Mappings   []config.FileMapping `json:"mappings"`
	Exclusions []string             `json:"exclusions"`
}

type keyValue struct {
	key      string
	value    string
	children []keyValue
	block    bool
}

// ImportAppBuild reads a SteamPipe app build script and the depot scripts it references.
func ImportAppBuild(path string) (ImportedBuild, error) {
	root, err := readKeyValues(path)
	if err != nil {
		return ImportedBuild{}, err
	}
	app, ok := child(root, "AppBuild")
	if !ok || !app.block {
		return ImportedBuild{}, fmt.Errorf("%s is not a SteamPipe app build script (no AppBuild block)", filepath.Base(path))
	}
	appID, err := strconv.ParseUint(value(app.children, "AppID"), 10, 64)
	if err != nil || appID == 0 {
		return ImportedBuild{}, fmt.Errorf("app build script has no valid AppID")
	}
	base := filepath.Dir(path)
	result := ImportedBuild{AppID: appID, Description: value(app.children, "Desc"), ContentRoot: resolveScriptPath(base, value(app.children, "ContentRoot")), Depots: []ImportedDepot{}}
	depots, ok := child(app.children, "Depots")
	if !ok || !depots.block {
		return ImportedBuild{}, fmt.Errorf("app build script has no Depots block")
	}
	for _, entry := range depots.children {
		depotID, err := strconv.ParseUint(entry.key, 10, 64)
		if err != nil {
			return ImportedBuild{}, fmt.Errorf("depot %q is not a numeric depot ID", entry.key)
		}
		settings, contentRoot := entry.children, result.ContentRoot
		if !entry.block {
			depotPath := resolveScriptPath(base, entry.value)
			depotRoot, err := readKeyValues(depotPath)
			if err != nil {
				return ImportedBuild{}, err
			}
			depotConfig, ok := child(depotRoot, "DepotBuildConfig")
			if !ok || !depotConfig.block {
				return ImportedBuild{}, fmt.Errorf("%s has no DepotBuildConfig block", filepath.Base(depotPath))
			}
			settings = depotConfig.children
			if override := value(settings, "ContentRoot"); override != "" {
				contentRoot = resolveScriptPath(filepath.Dir(depotPath), override)
			}
		} else if override := value(settings, "ContentRoot"); override != "" {
			contentRoot = resolveScriptPath(base, override)
		}
		result.Depots = append(result.Depots, importDepot(depotID, contentRoot, settings))
	}
	if len(result.Depots) == 0 {
		return ImportedBuild{}, fmt.Errorf("app build script lists no depots")
	}
	return result, nil
}

func importDepot(depotID uint64, contentRoot string, settings []keyValue) ImportedDepot {
	depot := ImportedDepot{DepotID: depotID, Content: contentRoot, Mappings: []config.FileMapping{}, Exclusions: []string{}}
	for _, entry := range settings {
		switch {
		case strings.EqualFold(entry.key, "FileMapping") && entry.block:
			mapping := config.FileMapping{
				LocalPath: filepath.ToSlash(strings.ReplaceAll(value(entry.children, "LocalPath"), `\`, "/")),
				DepotPath: strings.ReplaceAll(value(entry.children, "DepotPath"), `\`, "/"),
			}
			if recursive := value(entry.children, "recursive"); recursive != "" {
				enabled := recursive != "0"
				mapping.Recursive = &enabled
			}
			depot.Mappings = append(depot.Mappings, mapping)
		case strings.EqualFold(entry.key, "FileExclusion") && !entry.block:
			depot.Exclusions = append(depot.Exclusions, strings.ReplaceAll(entry.value, `\`, "/"))
		}
	}
	if len(depot.Mappings) == 1 {
		local, depotPath, recursive := depot.Mappings[0].Values()
		if folder, ok := strings.CutSuffix(local, "*"); ok && depotPath == "." && recursive && !strings.ContainsAny(folder, "*?") {
			depot.Content = filepath.Join(contentRoot, filepath.FromSlash(folder))
			depot.Mappings = []config.FileMapping{}
		}
	}
	depot.Platform = guessPlatform(depot.Content, depot.Mappings)
	return depot
}

func guessPlatform(content string, mappings []config.FileMapping) string {
	text := strings.ToLower(filepath.ToSlash(content))
	for _, mapping := range mappings {
		text += " " + strings.ToLower(mapping.LocalPath+" "+mapping.DepotPath)
	}
	for _, guess := range []struct{ platform, hint string }{
		{"windows", "win"}, {"macos", "mac"}, {"macos", "osx"}, {"linux", "linux"}, {"linux", "steamos"},
	} {
		if strings.Contains(text, guess.hint) {
			return guess.platform
		}
	}
	return ""
}

func resolveScriptPath(base, path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), `\`, "/")
	if path == "" {
		return filepath.Clean(base)
	}
	if filepath.IsAbs(path) || (len(path) > 1 && path[1] == ':') {
		return filepath.Clean(filepath.FromSlash(path))
	}
	return filepath.Join(base, filepath.FromSlash(path))
}

func child(entries []keyValue, key string) (keyValue, bool) {
	index := slices.IndexFunc(entries, func(entry keyValue) bool { return strings.EqualFold(entry.key, key) })
	if index < 0 {
		return keyValue{}, false
	}
	return entries[index], true
}

func value(entries []keyValue, key string) string {
	entry, ok := child(entries, key)
	if !ok || entry.block {
		return ""
	}
	return strings.TrimSpace(entry.value)
}

func readKeyValues(path string) ([]keyValue, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tokens, err := tokenizeKeyValues(string(contents))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	entries, rest, err := parseKeyValues(tokens)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if len(rest) > 0 {
		return nil, fmt.Errorf("%s: unexpected closing brace", filepath.Base(path))
	}
	return entries, nil
}

// Tokens are quoted strings, bare words, and braces; Valve's parser treats backslashes literally.
func tokenizeKeyValues(text string) ([]string, error) {
	tokens := []string{}
	for i := 0; i < len(text); {
		switch character := text[i]; {
		case character == ' ' || character == '\t' || character == '\r' || character == '\n':
			i++
		case strings.HasPrefix(text[i:], "//"):
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case character == '{' || character == '}':
			tokens = append(tokens, string(character))
			i++
		case character == '"':
			end := strings.IndexByte(text[i+1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("unterminated quoted string")
			}
			tokens = append(tokens, "\x00"+text[i+1:i+1+end])
			i += end + 2
		case character == '[':
			end := strings.IndexByte(text[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated condition")
			}
			i += end + 1
		default:
			start := i
			for i < len(text) && !strings.ContainsRune(" \t\r\n{}\"", rune(text[i])) {
				i++
			}
			tokens = append(tokens, "\x00"+text[start:i])
		}
	}
	return tokens, nil
}

func parseKeyValues(tokens []string) ([]keyValue, []string, error) {
	entries := []keyValue{}
	for len(tokens) > 0 {
		if tokens[0] == "}" {
			return entries, tokens, nil
		}
		if tokens[0] == "{" {
			return nil, nil, fmt.Errorf("block is missing its key")
		}
		key := strings.TrimPrefix(tokens[0], "\x00")
		tokens = tokens[1:]
		if len(tokens) == 0 {
			return nil, nil, fmt.Errorf("key %q has no value", key)
		}
		if tokens[0] == "{" {
			children, rest, err := parseKeyValues(tokens[1:])
			if err != nil {
				return nil, nil, err
			}
			if len(rest) == 0 {
				return nil, nil, fmt.Errorf("block %q is missing its closing brace", key)
			}
			entries = append(entries, keyValue{key: key, children: children, block: true})
			tokens = rest[1:]
			continue
		}
		if tokens[0] == "}" {
			return nil, nil, fmt.Errorf("key %q has no value", key)
		}
		entries = append(entries, keyValue{key: key, value: strings.TrimPrefix(tokens[0], "\x00")})
		tokens = tokens[1:]
	}
	return entries, nil, nil
}

type DiscoveredBuild struct {
	Path  string        `json:"path"`
	Build ImportedBuild `json:"build"`
}

var skippedDiscoveryDirs = map[string]bool{
	".git": true, ".godot": true, ".import": true, ".steam-releaser": true,
	"node_modules": true, "Library": true, "Temp": true, "Logs": true, "obj": true,
}

// DiscoverAppBuilds finds SteamPipe app build scripts below root, nearest first.
func DiscoverAppBuilds(root string) []DiscoveredBuild {
	found := []DiscoveredBuild{}
	baseDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && (skippedDiscoveryDirs[entry.Name()] || strings.Count(path, string(filepath.Separator))-baseDepth > 5) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".vdf") || len(found) >= 50 {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > 1<<20 {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil || !strings.Contains(strings.ToLower(string(contents)), "appbuild") {
			return nil
		}
		if build, err := ImportAppBuild(path); err == nil {
			found = append(found, DiscoveredBuild{Path: path, Build: build})
		}
		return nil
	})
	slices.SortStableFunc(found, func(a, b DiscoveredBuild) int {
		return strings.Count(a.Path, string(filepath.Separator)) - strings.Count(b.Path, string(filepath.Separator))
	})
	return found
}
