package config

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// annotatedConfig keeps optional examples at the scope where they can be enabled.
// Only settings for the selected engine are included in a project's template.
func annotatedConfig(value fileConfig) ([]byte, error) {
	var root yaml.Node
	if err := root.Encode(value); err != nil {
		return nil, err
	}
	root.HeadComment = "Steam Releaser configuration\nEdit profiles, targets, and build options here, then Reload and validate in the app.\nCheck depot IDs, paths, and engine settings before publishing.\nUncomment optional examples to enable them; keep their indentation.\nReference: https://github.com/thornity/steam-release-tool/blob/main/docs/configuration.md\n\n# version: 1"

	project := mappingValue(&root, "project")
	annotateOptions(project,
		configExample{"name", "Project name defaults to the game folder name.", "name: My Game"},
		configExample{"root", "Project folder relative to this file. Omit for a machine-local folder selected in the app.", "root: ../game"},
	)
	engine := value.Project.Engine.Type
	engineNode := mappingValue(project, "engine")
	if engine == "godot" || engine == "unity" {
		annotateOptions(engineNode, configExample{"executable", "Optional engine executable. Discovery also checks the environment and PATH.", "executable: /path/to/engine"})
	}
	switch engine {
	case "godot":
		annotateOptions(engineNode,
			configExample{"import_before_build", "Import before exporting by default; disable when imports are handled elsewhere.", "import_before_build: false"},
			configExample{"max_parallel", "Limit concurrent builds. Godot otherwise uses the available CPU count.", "max_parallel: 2"},
		)
	case "custom":
		annotateOptions(engineNode, configExample{"max_parallel", "Custom builds run serially by default.", "max_parallel: 2"})
	case "unity":
		annotateOptions(engineNode, configExample{"execute_method", "Optional default build method for Unity targets. Unity builds run serially.", "execute_method: Company.Build.Release"})
	}
	if tools := mappingValue(&root, "tools"); tools != nil {
		annotateOptions(tools, configExample{"steamcmd", "Optional SteamCMD executable. Discovery also checks STEAMCMD_BIN and PATH.", "steamcmd: /path/to/steamcmd"})
	} else {
		mappingKey(&root, "profiles").HeadComment = "Optional tool override. Passwords and Steam Guard codes do not belong in this file.\n# tools:\n#   steamcmd: /path/to/steamcmd"
	}

	profiles := mappingValue(&root, "profiles")
	for i := 0; i+1 < len(profiles.Content); i += 2 {
		profile := profiles.Content[i+1]
		targets := mappingValue(profile, "targets")
		if targets == nil || len(targets.Content) < 2 {
			continue // The normal decoder reports missing targets before any file is written.
		}
		firstTarget := targets.Content[0].Value
		annotateOptions(profile,
			configExample{"description", "Steam upload description; defaults to Build uploaded by Steam Releaser.", "description: Release build"},
			configExample{"build_output", "Generated Steam scripts and logs, relative to the project folder.", "build_output: .steam-releaser/output"},
			configExample{"existing_vdf", "Use an app-build VDF relative to this YAML file. Cannot combine with target filtering, preview, or branch activation.", "existing_vdf: steam/app_build.vdf"},
			configExample{"post_build", "Copy extra files after building, relative to the project folder. Omit targets to copy to all targets.", fmt.Sprintf("post_build:\n  - from: extras/launcher\n    to: launcher\n    targets: [%q]", firstTarget)},
		)
		for j := 0; j+1 < len(targets.Content); j += 2 {
			target := targets.Content[j+1]
			platform := platformFromName(targets.Content[j].Value)
			if platform == "" {
				platform = "windows"
			}
			content := "builds/" + targets.Content[j].Value
			if output := mappingValue(target, "output"); output != nil {
				content = filepath.ToSlash(filepath.Dir(output.Value))
			}
			contentYAML, err := yaml.Marshal(content)
			if err != nil {
				return nil, err
			}
			annotateOptions(target,
				configExample{"platform", "Platform is inferred from windows, linux, or macos target names.", "platform: " + platform},
				configExample{"architecture", "Architecture is descriptive; engine build settings select the actual architecture.", "architecture: x64"},
				configExample{"content", "Upload directory relative to the project folder; defaults to the parent of output.", "content: " + strings.TrimSpace(string(contentYAML))},
				configExample{"required", "Required files, as globs relative to content, checked before uploading.", "required: [\"**/*\"]"},
				configExample{"mappings", "Steam file mappings. The default uploads all content recursively.", "mappings:\n  - local_path: \"*\"\n    depot_path: .\n    recursive: true"},
				configExample{"exclusions", "Exclude files relative to content. .gitkeep is excluded by default.", "exclusions: [\"*.pdb\", \"**/.DS_Store\"]"},
			)
			build := mappingValue(target, "build")
			if build == nil {
				continue
			}
			if engine == "unity" {
				if mappingValue(build, "profile") != nil {
					annotateOptions(build, configExample{"target", "Alternative to profile. Remove profile when using a legacy build target.", "target: StandaloneWindows64"})
				} else {
					annotateOptions(build, configExample{"profile", "Alternative to target. Remove target when using a Unity Build Profile.", "profile: Assets/Settings/Windows.asset"})
				}
				annotateOptions(build,
					configExample{"execute_method", "Optional per-target build method; overrides the project's method.", "execute_method: Company.Build.Release"},
					configExample{"no_graphics", "Unity runs without a graphics device by default. Disable for builds that require one.", "no_graphics: false"},
				)
			}
			annotateOptions(build,
				configExample{"args", "Extra argument tokens in order. Each list item is one argument; no shell quoting.", "args: [--verbose]"},
				configExample{"env", "Literal environment overrides for this build only. Quote values; keep secrets outside YAML.", "env:\n  EDITION: \"release\"\n  ENABLE_ANALYTICS: \"0\"\n  OPTIONAL_SETTING: \"\""},
			)
		}
	}
	appendExamples(profiles, "Add another profile by copying the active profile above, or enable this example.\nUse that app's own depot IDs and separate output folders for demo and release builds.\nA profile name does not change game features; select the engine's preset/profile or script options.")
	// Derive a second profile from the same engine defaults so its example stays valid.
	demoName := "demo"
	if _, exists := value.Profiles[demoName]; exists {
		demoName = "another-release"
	}
	demoTarget := InitTarget{Name: "windows", DepotID: 2000001, Content: "builds/" + demoName + "/windows", BuildSetting: demoBuildSetting(engine)}
	if engine != "prebuilt" {
		demoTarget.Output = demoTarget.Content + "/REPLACE_WITH_GAME"
	}
	demo := Profile{AppID: 2000000, Targets: map[string]Target{"windows": TargetFromInit(engine, demoTarget)}}
	var example bytes.Buffer
	exampleEncoder := yaml.NewEncoder(&example)
	exampleEncoder.SetIndent(2)
	if err := exampleEncoder.Encode(map[string]Profile{demoName: demo}); err != nil {
		return nil, err
	}
	if err := exampleEncoder.Close(); err != nil {
		return nil, err
	}
	appendExamples(profiles, commentYAML(example.String()))

	var contents bytes.Buffer
	encoder := yaml.NewEncoder(&contents)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return contents.Bytes(), nil
}

type configExample struct {
	key, description, source string
}

func annotateOptions(mapping *yaml.Node, examples ...configExample) {
	for _, example := range examples {
		if mappingValue(mapping, example.key) == nil {
			appendExamples(mapping, example.description+"\n"+commentYAML(example.source))
		}
	}
}

func appendExamples(mapping *yaml.Node, comment string) {
	if mapping == nil || len(mapping.Content) < 2 {
		return
	}
	key := mapping.Content[len(mapping.Content)-2]
	if key.FootComment != "" {
		key.FootComment += "\n\n"
	}
	key.FootComment += comment
}

func commentYAML(source string) string {
	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")
	for i := range lines {
		lines[i] = "# " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func mappingKey(mapping *yaml.Node, name string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i]
		}
	}
	return nil
}

func demoBuildSetting(engine string) string {
	switch engine {
	case "godot":
		return "REPLACE_WITH_DEMO_PRESET"
	case "unity":
		return "Assets/Settings/REPLACE_WITH_DEMO_PROFILE.asset"
	case "custom":
		return "replace-with-demo-build-command"
	default:
		return ""
	}
}
