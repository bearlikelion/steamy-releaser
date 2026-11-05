package artifact

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
)

func Prepare(document *config.Document, plan *release.Plan) error {
	for _, target := range plan.Targets {
		if target.Command == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target.Output), 0o755); err != nil {
			return fmt.Errorf("create output directory for %s: %w", target.Name, err)
		}
		if err := os.MkdirAll(target.Content, 0o755); err != nil {
			return fmt.Errorf("create content directory for %s: %w", target.Name, err)
		}
	}
	if plan.Engine == "unity" {
		return os.MkdirAll(document.ResolveProject(filepath.Join(".steam-releaser", "logs", plan.Profile)), 0o755)
	}
	return nil
}

func Validate(document *config.Document, profileName string, targetNames []string) error {
	selection, err := document.Select(profileName, targetNames)
	if err != nil {
		return err
	}
	profile, names := selection.Profile, selection.TargetNames
	var problems []error
	for _, name := range names {
		target := profile.Targets[name]
		output := document.ResolveProject(target.Output)
		if info, statErr := os.Stat(output); statErr != nil {
			problems = append(problems, fmt.Errorf("target %s is missing build output %s", name, output))
		} else if !info.IsDir() && !info.Mode().IsRegular() {
			problems = append(problems, fmt.Errorf("target %s build output is not a regular file or directory: %s", name, output))
		}
		content := document.ResolveProject(target.Content)
		info, statErr := os.Stat(content)
		if statErr != nil || !info.IsDir() {
			problems = append(problems, fmt.Errorf("target %s is missing content directory %s", name, content))
			continue
		}
		root, openErr := os.OpenRoot(content)
		if openErr != nil {
			problems = append(problems, fmt.Errorf("target %s cannot open content: %w", name, openErr))
			continue
		}
		nonEmpty := false
		walkErr := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := root.Stat(path)
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("artifact %s is not a regular file or directory", path)
			}
			if info.Mode().IsRegular() && entry.Name() != ".gitkeep" {
				nonEmpty = true
			}
			return nil
		})
		if walkErr != nil {
			problems = append(problems, fmt.Errorf("target %s cannot validate content: %w", name, walkErr))
		}
		if !nonEmpty {
			problems = append(problems, fmt.Errorf("target %s content directory is empty: %s", name, content))
		}
		for _, required := range target.Required {
			if !filepath.IsLocal(required) {
				problems = append(problems, fmt.Errorf("target %s has unsafe required pattern %q", name, required))
				continue
			}
			matches, matchErr := doublestar.Glob(root.FS(), filepath.ToSlash(required), doublestar.WithFailOnIOErrors())
			if matchErr != nil || len(matches) == 0 {
				problems = append(problems, fmt.Errorf("target %s is missing required artifact %q", name, required))
			}
		}
		_ = root.Close()
	}
	return errors.Join(problems...)
}

func ApplyCopyRules(document *config.Document, profileName string, selected []string, reporter release.Reporter) error {
	return applyCopyRules(document, profileName, selected, reporter, nil)
}

// ApplyCopyRulesWithGuard checks sources against the snapshot taken before the
// build before allowing copies to affect protected outputs.
func ApplyCopyRulesWithGuard(document *config.Document, profileName string, selected []string, reporter release.Reporter, guard *CopyGuard) error {
	return applyCopyRules(document, profileName, selected, reporter, guard)
}

func applyCopyRules(document *config.Document, profileName string, selected []string, reporter release.Reporter, guard *CopyGuard) error {
	selection, err := document.Select(profileName, selected)
	if err != nil {
		return err
	}
	profile, targetNames := selection.Profile, selection.TargetNames
	project, err := os.OpenRoot(document.ProjectRoot())
	if err != nil {
		return fmt.Errorf("open project for post_build copies: %w", err)
	}
	defer project.Close()
	selectedSet := make(map[string]bool, len(targetNames))
	for _, name := range targetNames {
		selectedSet[name] = true
	}
	for _, rule := range profile.PostBuild {
		if !filepath.IsLocal(rule.From) {
			return fmt.Errorf("post_build source must stay inside the project root: %q", rule.From)
		}
		matches, globErr := doublestar.Glob(project.FS(), filepath.ToSlash(rule.From), doublestar.WithFailOnIOErrors())
		if globErr != nil {
			return fmt.Errorf("invalid post_build glob %q: %w", rule.From, globErr)
		}
		if len(matches) == 0 {
			return fmt.Errorf("post_build glob %q matched no files", rule.From)
		}
		destination := rule.To
		if destination == "" {
			destination = "."
		}
		if !filepath.IsLocal(destination) {
			return fmt.Errorf("post_build destination must be relative and cannot escape content: %q", destination)
		}
		ruleTargets := rule.Targets
		if len(ruleTargets) == 0 {
			ruleTargets = targetNames
		}
		for _, targetName := range ruleTargets {
			if !selectedSet[targetName] {
				continue
			}
			content := document.ResolveProject(profile.Targets[targetName].Content)
			for _, match := range matches {
				source := filepath.Join(document.ProjectRoot(), filepath.FromSlash(match))
				target := filepath.Join(content, destination, filepath.Base(source))
				if copyErr := copyIntoContent(project, filepath.FromSlash(match), content, filepath.Join(destination, filepath.Base(source)), guard); copyErr != nil {
					return fmt.Errorf("copy %s to %s: %w", source, target, copyErr)
				}
				if reporter != nil {
					reporter.Report(release.Event{Level: release.LevelInfo, Target: targetName, Message: fmt.Sprintf("Copied %s -> %s", source, target)})
				}
			}
		}
	}
	return nil
}
