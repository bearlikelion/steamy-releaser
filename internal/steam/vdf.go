package steam

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/pathcheck"
)

var branchPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const generatedRunIDBytes = 16

type ScriptOptions struct {
	Preview bool
	SetLive string
}

type Scripts struct {
	AppPath     string            `json:"app_path" yaml:"app_path"`
	DepotPaths  map[uint64]string `json:"depot_paths" yaml:"depot_paths"`
	BuildOutput string            `json:"build_output,omitempty" yaml:"build_output,omitempty"`
}

func Generate(document *config.Document, profileName string, selected []string, options ScriptOptions) (Scripts, error) {
	selection, existingPath, err := selectScriptInputs(document, profileName, selected, options)
	if err != nil {
		return Scripts{}, err
	}
	profileName, profile, targetNames := selection.Name, selection.Profile, selection.TargetNames
	if existingPath != "" {
		return Scripts{AppPath: existingPath, DepotPaths: map[uint64]string{}}, nil
	}
	profileDirectory, err := generatedScriptDirectory(profileName)
	if err != nil {
		return Scripts{}, err
	}
	var nonce [generatedRunIDBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Scripts{}, fmt.Errorf("choose Steam upload directory: %w", err)
	}
	runID := "run-" + hex.EncodeToString(nonce[:])
	relativeDirectory := filepath.Join(profileDirectory, runID)
	directory := document.ResolveProject(relativeDirectory)
	scripts := Scripts{AppPath: filepath.Join(directory, "app_build.vdf"), DepotPaths: map[uint64]string{}}
	buildOutput := document.ResolveProject(profile.EffectiveBuildOutput())
	defaultOutput := strings.TrimSpace(profile.BuildOutput) == ""
	if defaultOutput {
		buildOutput = filepath.Join(buildOutput, runID)
	}
	scripts.BuildOutput = buildOutput
	depotTargets := map[uint64][]config.Target{}
	for _, name := range targetNames {
		target := profile.Targets[name]
		depotTargets[target.DepotID] = append(depotTargets[target.DepotID], target)
	}
	depotContents := make(map[uint64]string, len(depotTargets))
	for depotID, targets := range depotTargets {
		path := filepath.Join(directory, fmt.Sprintf("depot_build_%d.vdf", depotID))
		contents, err := depotVDF(document, targets)
		if err != nil {
			return Scripts{}, err
		}
		scripts.DepotPaths[depotID] = path
		depotContents[depotID] = contents
	}
	appContents, err := appVDF(profile, scripts, buildOutput, options)
	if err != nil {
		return Scripts{}, err
	}
	projectPath := document.ProjectRoot()
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		return Scripts{}, err
	}
	project, err := os.OpenRoot(projectPath)
	if err != nil {
		return Scripts{}, err
	}
	defer project.Close()
	if err := project.MkdirAll(profileDirectory, 0o755); err != nil {
		return Scripts{}, fmt.Errorf("create generated Steam script directory: %w", err)
	}
	// Never reuse a previous run's directory, even if a name collision or an
	// independently created filesystem entry is encountered.
	if err := project.Mkdir(relativeDirectory, 0o700); err != nil {
		return Scripts{}, fmt.Errorf("create unique Steam script directory: %w", err)
	}
	if managedBuildOutput(profile) {
		if err := project.MkdirAll(filepath.Join(".steam-releaser", "output"), 0o755); err != nil {
			return Scripts{}, fmt.Errorf("create Steam build output directory: %w", err)
		}
		relativeOutput := filepath.Join(".steam-releaser", "output")
		if defaultOutput {
			relativeOutput = filepath.Join(relativeOutput, runID)
			if err := project.Mkdir(relativeOutput, 0o700); err != nil {
				return Scripts{}, fmt.Errorf("create unique Steam build output directory: %w", err)
			}
		}
		if _, err := project.Stat(relativeOutput); err != nil {
			return Scripts{}, fmt.Errorf("inspect Steam build output directory: %w", err)
		}
	} else if err := os.MkdirAll(document.ResolveProject(profile.EffectiveBuildOutput()), 0o755); err != nil {
		return Scripts{}, err
	}
	for depotID, contents := range depotContents {
		if err := writeAtomic(project, filepath.Join(relativeDirectory, filepath.Base(scripts.DepotPaths[depotID])), []byte(contents)); err != nil {
			return Scripts{}, err
		}
	}
	if err := writeAtomic(project, filepath.Join(relativeDirectory, "app_build.vdf"), []byte(appContents)); err != nil {
		return Scripts{}, err
	}
	return scripts, nil
}

// ValidateScriptSelection checks upload script inputs without generating files.
// Generate repeats the check immediately before using the script.
func ValidateScriptSelection(document *config.Document, profileName string, targets []string, options ScriptOptions) error {
	_, _, err := selectScriptInputs(document, profileName, targets, options)
	return err
}

func ValidateDepotSelection(document *config.Document, profileName string, targets []string) error {
	selection, err := document.Select(profileName, targets)
	if err != nil {
		return err
	}
	return validateDepotSelection(selection.Profile, selection.TargetNames)
}

func selectScriptInputs(document *config.Document, profileName string, targets []string, options ScriptOptions) (config.Selection, string, error) {
	selection, err := document.Select(profileName, targets)
	if err != nil {
		return config.Selection{}, "", err
	}
	if err := validateDepotSelection(selection.Profile, selection.TargetNames); err != nil {
		return config.Selection{}, "", err
	}
	if selection.Profile.ExistingVDF != "" {
		if options.Preview || options.SetLive != "" || len(selection.TargetNames) != len(selection.Profile.Targets) {
			return config.Selection{}, "", fmt.Errorf("preview, set-live, and target filtering cannot rewrite profile existing_vdf")
		}
		path := document.ResolveFromBase(selection.Profile.ExistingVDF)
		info, err := os.Stat(path)
		if err != nil {
			return config.Selection{}, "", fmt.Errorf("existing VDF does not exist: %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return config.Selection{}, "", fmt.Errorf("existing VDF is not a regular file: %s", path)
		}
		return selection, path, nil
	}
	if err := ValidateScriptOptions(options); err != nil {
		return config.Selection{}, "", err
	}
	if err := validateGeneratedScriptInputs(document, selection); err != nil {
		return config.Selection{}, "", err
	}
	if err := validateGeneratedStatePaths(document, selection); err != nil {
		return config.Selection{}, "", err
	}
	if err := validateGeneratedStateOutsideContent(document, selection); err != nil {
		return config.Selection{}, "", err
	}
	return selection, "", nil
}

func generatedScriptDirectory(profile string) (string, error) {
	if strings.TrimSpace(profile) == "" || strings.TrimRight(profile, ". ") != profile || !filepath.IsLocal(profile) || filepath.Base(profile) != profile || strings.ContainsAny(profile, `/\:`) {
		return "", fmt.Errorf("invalid profile name for generated Steam scripts")
	}
	return filepath.Join(".steam-releaser", "generated", profile), nil
}

func managedBuildOutput(profile config.Profile) bool {
	return filepath.Clean(profile.EffectiveBuildOutput()) == filepath.Join(".steam-releaser", "output")
}

// This read-only preflight catches existing escaping links before a build. The
// root-confined operations in Generate repeat the check when files are written.
func validateGeneratedStatePaths(document *config.Document, selection config.Selection) error {
	directory, err := generatedScriptDirectory(selection.Name)
	if err != nil {
		return err
	}
	project, err := os.OpenRoot(document.ProjectRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open project for generated Steam scripts: %w", err)
	}
	defer project.Close()
	paths := []string{directory}
	if managedBuildOutput(selection.Profile) {
		paths = append(paths, filepath.Join(".steam-releaser", "output"))
	}
	for _, path := range paths {
		info, err := project.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect generated Steam state %s: %w", path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("generated Steam state %s must be a directory", path)
		}
	}
	return nil
}

// SteamCMD reads selected content after generating scripts and may write logs
// while uploading. Keep those paths outside every selected content directory.
func validateGeneratedStateOutsideContent(document *config.Document, selection config.Selection) error {
	scripts, err := generatedScriptDirectory(selection.Name)
	if err != nil {
		return err
	}
	statePaths := []struct {
		name string
		path string
	}{
		{name: "generated Steam scripts", path: document.ResolveProject(scripts)},
		{name: "Steam build output", path: document.ResolveProject(selection.Profile.EffectiveBuildOutput())},
	}
	resolved := make([]pathcheck.Path, len(statePaths))
	for i, state := range statePaths {
		resolved[i], err = pathcheck.Resolve(state.path)
		if err != nil {
			return fmt.Errorf("inspect %s path %s: %w", state.name, state.path, err)
		}
	}
	for _, name := range selection.TargetNames {
		contentPath := document.ResolveProject(selection.Profile.Targets[name].Content)
		content, err := pathcheck.Resolve(contentPath)
		if err != nil {
			return fmt.Errorf("inspect target %q content path %s: %w", name, contentPath, err)
		}
		for i, state := range statePaths {
			if pathcheck.Overlap(content, resolved[i]) {
				return fmt.Errorf("%s path %s overlaps target %q content %s; choose separate content and Steam output directories", state.name, state.path, name, contentPath)
			}
		}
	}
	return nil
}

// Valve's KeyValues parser does not enable escape sequences by default. A
// backslash-escaped quote may therefore terminate a value and introduce script
// syntax. Reject unsafe values before building or writing generated scripts.
func validateGeneratedScriptInputs(document *config.Document, selection config.Selection) error {
	profile := selection.Profile
	if err := validateVDFValue("description", profile.EffectiveDescription()); err != nil {
		return err
	}
	buildOutput := document.ResolveProject(profile.EffectiveBuildOutput())
	if strings.TrimSpace(profile.BuildOutput) == "" {
		buildOutput = filepath.Join(buildOutput, "run-"+strings.Repeat("0", 2*generatedRunIDBytes))
	}
	if err := validateVDFValue("build output path", buildOutput); err != nil {
		return err
	}
	for _, name := range selection.TargetNames {
		target := profile.Targets[name]
		if err := validateVDFValue(fmt.Sprintf("target %q content path", name), document.ResolveProject(target.Content)); err != nil {
			return err
		}
		for _, mapping := range target.Mappings {
			local, depot, _ := mapping.Values()
			if err := validateMappingPath(fmt.Sprintf("target %q mapping source", name), local); err != nil {
				return err
			}
			if err := validateMappingPath(fmt.Sprintf("target %q mapping destination", name), depot); err != nil {
				return err
			}
		}
		for _, exclusion := range target.Exclusions {
			if err := validateMappingPath(fmt.Sprintf("target %q exclusion", name), exclusion); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateVDFValue(name, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8 for the Steam build script", name)
	}
	// Valve's public KeyValues parser uses a 4096-byte token buffer. Leave
	// room for its terminator rather than producing a truncated value.
	if len(value) >= 4096 {
		return fmt.Errorf("%s is too long for the Steam build script", name)
	}
	for _, character := range value {
		if character == '"' || unicode.IsControl(character) {
			return fmt.Errorf("%s contains a double quote or control character that cannot be safely encoded in the Steam build script", name)
		}
	}
	return nil
}

// Steam mapping paths use both slash styles, including on non-Windows hosts.
// Reject portable forms of absolute paths, NTFS streams, and parent traversal.
func validateMappingPath(name, value string) error {
	if err := validateVDFValue(name, value); err != nil {
		return err
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	invalid := strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":")
	for _, component := range strings.Split(normalized, "/") {
		// Windows strips trailing spaces and dots from ordinary path components.
		if component != "" && component != "." && strings.Trim(component, ". ") == "" {
			invalid = true
		}
	}
	if invalid {
		return fmt.Errorf("%s must be a relative Steam build script path without parent traversal, a drive, or a stream", name)
	}
	return nil
}

func validateDepotSelection(profile config.Profile, targetNames []string) error {
	selectedTargets := make(map[string]bool, len(targetNames))
	for _, name := range targetNames {
		selectedTargets[name] = true
	}
	for _, name := range targetNames {
		depotID := profile.Targets[name].DepotID
		for other, target := range profile.Targets {
			if target.DepotID == depotID && !selectedTargets[other] {
				return fmt.Errorf("depot %d is shared by targets %q and %q; select all targets in the depot for upload", depotID, name, other)
			}
		}
	}
	return nil
}

func ValidateScriptOptions(options ScriptOptions) error {
	if options.Preview && options.SetLive != "" {
		return fmt.Errorf("preview and set-live cannot be used together")
	}
	if options.SetLive != "" {
		if !branchPattern.MatchString(options.SetLive) {
			return fmt.Errorf("set-live branch contains unsupported characters")
		}
		if strings.EqualFold(options.SetLive, "default") {
			return fmt.Errorf("Steam does not allow automatically setting the default branch live")
		}
	}
	return nil
}

func appVDF(profile config.Profile, scripts Scripts, buildOutput string, options ScriptOptions) (string, error) {
	var builder vdfBuilder
	builder.line(0, `"AppBuild"`)
	builder.line(0, `{`)
	builder.pair(1, "AppID", fmt.Sprint(profile.AppID))
	builder.pair(1, "Desc", profile.EffectiveDescription())
	if options.Preview {
		builder.pair(1, "Preview", "1")
	} else {
		builder.pair(1, "Preview", "0")
	}
	if options.SetLive != "" {
		builder.pair(1, "SetLive", options.SetLive)
	}
	builder.pair(1, "BuildOutput", buildOutput)
	builder.line(1, `"Depots"`)
	builder.line(1, `{`)
	ids := make([]uint64, 0, len(scripts.DepotPaths))
	for id := range scripts.DepotPaths {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		builder.pair(2, fmt.Sprint(id), filepath.Base(scripts.DepotPaths[id]))
	}
	builder.line(1, `}`)
	builder.line(0, `}`)
	return builder.String(), builder.err
}

func depotVDF(document *config.Document, targets []config.Target) (string, error) {
	var builder vdfBuilder
	first := targets[0]
	builder.line(0, `"DepotBuild"`)
	builder.line(0, `{`)
	builder.pair(1, "DepotID", fmt.Sprint(first.DepotID))
	builder.pair(1, "ContentRoot", document.ResolveProject(first.Content))
	for _, target := range targets {
		mappings := target.Mappings
		if len(mappings) == 0 {
			mappings = []config.FileMapping{{}}
		}
		for _, mapping := range mappings {
			local, depot, recursive := mapping.Values()
			builder.line(1, `"FileMapping"`)
			builder.line(1, `{`)
			builder.pathPair(2, "LocalPath", local)
			builder.pathPair(2, "DepotPath", depot)
			if recursive {
				builder.pair(2, "Recursive", "1")
			} else {
				builder.pair(2, "Recursive", "0")
			}
			builder.line(1, `}`)
		}
	}
	exclusions := []string{"*.gitkeep"}
	for _, target := range targets {
		exclusions = append(exclusions, target.Exclusions...)
	}
	for _, exclusion := range exclusions {
		builder.pathPair(1, "FileExclusion", exclusion)
	}
	builder.line(0, `}`)
	return builder.String(), builder.err
}

type vdfBuilder struct {
	strings.Builder
	err error
}

func (builder *vdfBuilder) line(depth int, value string) {
	builder.WriteString(strings.Repeat("\t", depth))
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func (builder *vdfBuilder) pair(depth int, key, value string) {
	if builder.err != nil {
		return
	}
	if err := validateVDFValue("field name", key); err != nil {
		builder.err = err
		return
	}
	if err := validateVDFValue(fmt.Sprintf("field %q", key), value); err != nil {
		builder.err = err
		return
	}
	builder.line(depth, fmt.Sprintf(`"%s" "%s"`, key, value))
}

func (builder *vdfBuilder) pathPair(depth int, key, value string) {
	if builder.err != nil {
		return
	}
	if err := validateMappingPath(fmt.Sprintf("field %q", key), value); err != nil {
		builder.err = err
		return
	}
	builder.pair(depth, key, value)
}

func writeAtomic(project *os.Root, path string, contents []byte) error {
	temporaryName := filepath.Join(filepath.Dir(path), ".vdf-"+rand.Text())
	temporary, err := project.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer project.Remove(temporaryName)
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return project.Rename(temporaryName, path)
}
