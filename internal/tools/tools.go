package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/thornity/steam-release-tool/internal/config"
)

type Resolution struct {
	Path   string `json:"path" yaml:"path"`
	Found  bool   `json:"found" yaml:"found"`
	Source string `json:"source" yaml:"source"`
}

func Godot(document *config.Document) Resolution {
	configured := ""
	if document.Config.Project.Engine.Type == "godot" {
		configured = document.Config.Project.Engine.Executable
	}
	candidates := []string{}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, "/Applications/Godot.app/Contents/MacOS/Godot")
	case "windows":
		candidates = append(candidates,
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Godot", "Godot.exe"),
		)
	}
	return resolve(configured, "GODOT_BIN", document.ProjectRoot(), []string{"godot", "godot4"}, candidates)
}

func Unity(document *config.Document) Resolution {
	configured := ""
	if document.Config.Project.Engine.Type == "unity" {
		configured = document.Config.Project.Engine.Executable
	}
	return resolveUnity(configured, document.ProjectRoot(), unityCandidates(document.ProjectRoot()))
}

func resolveUnity(configured, projectRoot string, candidates []string) Resolution {
	if value := strings.TrimSpace(os.Getenv("UNITY_BIN")); value != "" {
		return resolveValue(value, projectRoot, "environment UNITY_BIN")
	}
	if strings.TrimSpace(configured) != "" {
		return resolveValue(configured, projectRoot, "configuration")
	}
	// The Unity CLI also installs a command named `unity`. Only accept paths that
	// resolve to an Editor layout when discovering an executable automatically.
	var onPath []string
	for _, name := range []string{"Unity", "unity-editor", "unity"} {
		if path, err := exec.LookPath(name); err == nil && isUnityEditor(path) {
			absolute, _ := filepath.Abs(path)
			onPath = append(onPath, absolute)
		}
	}
	if version := unityProjectVersion(projectRoot); version != "" {
		for _, path := range onPath {
			if unityEditorPathHasVersion(path, version) {
				return Resolution{Path: path, Found: true, Source: "PATH"}
			}
		}
		for _, candidate := range candidates {
			if unityEditorPathHasVersion(candidate, version) && isUnityEditor(candidate) {
				return Resolution{Path: candidate, Found: true, Source: "common install location"}
			}
		}
	}
	if len(onPath) > 0 {
		return Resolution{Path: onPath[0], Found: true, Source: "PATH"}
	}
	for _, candidate := range candidates {
		if isUnityEditor(candidate) {
			return Resolution{Path: candidate, Found: true, Source: "common install location"}
		}
	}
	return Resolution{Path: "Unity", Found: false, Source: "not found"}
}

func unityEditorPathHasVersion(path, version string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		if filepath.Base(directory) == version {
			return true
		}
		if parent := filepath.Dir(directory); parent == directory {
			return false
		}
	}
}

func isUnityEditor(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	name := filepath.Base(resolved)
	if !strings.EqualFold(name, "Unity") && !strings.EqualFold(name, "Unity.exe") {
		return false
	}
	parent := filepath.Dir(resolved)
	if strings.EqualFold(filepath.Base(parent), "Editor") {
		data, err := os.Stat(filepath.Join(parent, "Data"))
		return err == nil && data.IsDir()
	}
	return filepath.Base(parent) == "MacOS" &&
		filepath.Base(filepath.Dir(parent)) == "Contents" &&
		strings.EqualFold(filepath.Base(filepath.Dir(filepath.Dir(parent))), "Unity.app")
}

func SteamCMD(document *config.Document) Resolution {
	configured := document.Config.Tools.SteamCMD
	candidates := []string{}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			filepath.Join(home, "Library", "Application Support", "Steam", "steamcmd", "steamcmd.sh"),
		)
	case "linux":
		candidates = append(candidates,
			filepath.Join(home, ".steam", "steamcmd", "steamcmd.sh"),
			filepath.Join(home, "Steam", "steamcmd", "steamcmd.sh"),
		)
	}
	return resolve(configured, "STEAMCMD_BIN", document.BaseDir(), []string{"steamcmd", "steamcmd.sh"}, candidates)
}

func Custom(document *config.Document, program string) Resolution {
	return resolve(program, "", document.ProjectRoot(), nil, nil)
}

func resolve(configured, environment, relativeTo string, names, candidates []string) Resolution {
	if environment != "" {
		if value := strings.TrimSpace(os.Getenv(environment)); value != "" {
			return resolveValue(value, relativeTo, "environment "+environment)
		}
	}
	if strings.TrimSpace(configured) != "" {
		return resolveValue(configured, relativeTo, "configuration")
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			absolute, _ := filepath.Abs(path)
			return Resolution{Path: absolute, Found: true, Source: "PATH"}
		}
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return Resolution{Path: candidate, Found: true, Source: "common install location"}
		}
	}
	fallback := ""
	if len(names) > 0 {
		fallback = names[0]
	}
	return Resolution{Path: fallback, Found: false, Source: "not found"}
}

func resolveValue(value, relativeTo, source string) Resolution {
	value = filepath.Clean(value)
	if !filepath.IsAbs(value) && containsPathSeparator(value) {
		value = filepath.Join(relativeTo, value)
	}
	if !containsPathSeparator(value) && !filepath.IsAbs(value) {
		if path, err := exec.LookPath(value); err == nil {
			absolute, _ := filepath.Abs(path)
			return Resolution{Path: absolute, Found: true, Source: source}
		}
		return Resolution{Path: value, Found: false, Source: source}
	}
	absolute, _ := filepath.Abs(value)
	info, err := os.Stat(absolute)
	return Resolution{Path: absolute, Found: err == nil && !info.IsDir(), Source: source}
}

func containsPathSeparator(value string) bool {
	return strings.ContainsAny(value, `/\`)
}

func unityCandidates(projectRoot string) []string {
	patterns := []string{}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		patterns = append(patterns,
			"/Applications/Unity/Hub/Editor/*/Unity.app/Contents/MacOS/Unity",
		)
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)")} {
			if root != "" {
				patterns = append(patterns, filepath.Join(root, "Unity", "Hub", "Editor", "*", "Editor", "Unity.exe"))
			}
		}
	case "linux":
		patterns = append(patterns,
			filepath.Join(home, "Unity", "Hub", "Editor", "*", "Editor", "Unity"),
			filepath.Join(home, ".local", "share", "UnityHub", "Editors", "*", "Editor", "Unity"),
		)
	}
	var candidates []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		candidates = append(candidates, matches...)
	}
	requestedVersion := unityProjectVersion(projectRoot)
	sort.SliceStable(candidates, func(i, j int) bool {
		iMatch := requestedVersion != "" && strings.Contains(candidates[i], requestedVersion)
		jMatch := requestedVersion != "" && strings.Contains(candidates[j], requestedVersion)
		if iMatch != jMatch {
			return iMatch
		}
		return candidates[i] > candidates[j]
	})
	return candidates
}

func unityProjectVersion(projectRoot string) string {
	contents, err := os.ReadFile(filepath.Join(projectRoot, "ProjectSettings", "ProjectVersion.txt"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "m_EditorVersion:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
