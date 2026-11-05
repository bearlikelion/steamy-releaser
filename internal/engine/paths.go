package engine

import (
	"fmt"
	"slices"

	"github.com/thornity/steam-release-tool/internal/pathcheck"
	"github.com/thornity/steam-release-tool/internal/release"
)

// CheckBuildPaths rejects outputs that would overwrite one another and limits
// builds to one worker when their configured write/content paths overlap. It
// can be called again after locking to account for changed symlinks.
func CheckBuildPaths(plan *release.Plan) error {
	buildTargets := 0
	for _, target := range plan.Targets {
		if target.Command != nil {
			buildTargets++
		}
	}
	if buildTargets < 2 {
		return nil
	}
	type targetPaths struct {
		target  *release.TargetPlan
		output  pathcheck.Path
		content pathcheck.Path
	}
	var targets []targetPaths
	for index := range plan.Targets {
		target := &plan.Targets[index]
		if target.Command == nil {
			continue
		}
		output, err := pathcheck.Resolve(target.Output)
		if err != nil {
			return fmt.Errorf("inspect build output for target %q: %w", target.Name, err)
		}
		content, err := pathcheck.Resolve(target.Content)
		if err != nil {
			return fmt.Errorf("inspect content for target %q: %w", target.Name, err)
		}
		targets = append(targets, targetPaths{target: target, output: output, content: content})
	}
	for i := range targets {
		for j := i + 1; j < len(targets); j++ {
			a, b := targets[i], targets[j]
			if pathcheck.Overlap(a.output, b.output) {
				return fmt.Errorf("targets %q and %q have overlapping build outputs %q and %q", a.target.Name, b.target.Name, a.target.Output, b.target.Output)
			}
			if pathcheck.Overlap(a.content, b.content) || pathcheck.Overlap(a.output, b.content) || pathcheck.Overlap(b.output, a.content) {
				plan.Parallelism = 1
				for _, pair := range [][2]*release.TargetPlan{{a.target, b.target}, {b.target, a.target}} {
					warning := fmt.Sprintf("Build or content paths overlap with target %q; builds run serially but can still overwrite shared files", pair[1].Name)
					if !slices.Contains(pair[0].Command.Warnings, warning) {
						pair[0].Command.Warnings = append(pair[0].Command.Warnings, warning)
					}
				}
			}
		}
	}
	return nil
}
