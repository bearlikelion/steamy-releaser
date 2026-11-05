package config

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// ValidateProcessOptions checks values that must survive direct process execution.
func (b Build) ValidateProcessOptions(engine string) error {
	if engine == "prebuilt" && (len(b.Args) > 0 || len(b.Env) > 0) {
		return fmt.Errorf("build.args and build.env require a build engine; prebuilt targets do not run a build")
	}
	for _, group := range []struct {
		name   string
		values []string
	}{{"args", b.Args}, {"command", b.Command}} {
		for i, value := range group.values {
			if strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("build.%s[%d] cannot contain NUL", group.name, i)
			}
		}
	}
	keys := make([]string, 0, len(b.Env))
	for key := range b.Env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	seen := map[string]bool{}
	for _, key := range keys {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("build.env name %q must be nonempty and cannot contain '=' or NUL", key)
		}
		if strings.ContainsRune(b.Env[key], '\x00') {
			return fmt.Errorf("build.env value for %q cannot contain NUL", key)
		}
		if runtime.GOOS == "windows" {
			canonical := strings.ToUpper(key)
			if seen[canonical] {
				return fmt.Errorf("build.env repeats name %q with different casing on Windows", key)
			}
			seen[canonical] = true
		}
	}
	return nil
}

// ArgumentWarnings diagnoses native flags owned by the adapter without changing
// the ordering or behavior of previously accepted argument arrays.
func (b Build) ArgumentWarnings(engine string) []string {
	var warnings []string
	for _, token := range b.Args {
		if engine == "godot" && (token == "--" || token == "++") {
			break // Following tokens belong to the project, not the engine.
		}
		flag, _, _ := strings.Cut(token, "=")
		managed := false
		switch engine {
		case "godot":
			switch flag {
			case "--path", "--export-release", "--export-debug", "--export-pack", "--export-patch", "--import":
				managed = true
			}
		case "unity":
			switch strings.ToLower(flag) {
			case "-projectpath", "-logfile", "-buildtarget", "-activebuildprofile", "-build", "-buildwindowsplayer", "-buildwindows64player", "-buildlinux64player", "-buildosxuniversalplayer", "-executemethod", "-steamreleaseroutput", "-steamreleaserprofile", "-steamreleasertarget":
				managed = true
			}
		}
		if managed {
			warnings = append(warnings, fmt.Sprintf("build.args token %q overlaps an engine argument managed by Steam Releaser; configure the corresponding build setting instead", token))
		}
	}
	return warnings
}
