package workflows

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"
)

// This repository runs routine verification locally. Catch new automatic or
// one-off hosted workflows before they can start paid runners on a push or PR.
func TestHostedActionsAreReleaseOnly(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var workflows []string
	for _, entry := range entries {
		if ext := filepath.Ext(entry.Name()); !entry.IsDir() && (ext == ".yml" || ext == ".yaml") {
			workflows = append(workflows, entry.Name())
		}
	}
	if !reflect.DeepEqual(workflows, []string{"release.yml"}) {
		t.Fatalf("only the tag release workflow is allowed; found %v", workflows)
	}

	data, err := os.ReadFile(filepath.Join(dir, "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On   map[string]yaml.Node `yaml:"on"`
		Jobs map[string]struct {
			Uses     string `yaml:"uses"`
			Strategy struct {
				Matrix struct {
					Include []struct {
						OS       string `yaml:"os"`
						Platform string `yaml:"platform"`
					} `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	push, ok := workflow.On["push"]
	if !ok || len(workflow.On) != 1 {
		t.Fatalf("release must run only on tag pushes, got %v", workflow.On)
	}
	var filters map[string][]string
	if err := push.Decode(&filters); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(filters, map[string][]string{"tags": {"v*"}}) {
		t.Fatalf("release must run only on v* tags, got %v", filters)
	}

	var jobs []string
	for name, job := range workflow.Jobs {
		jobs = append(jobs, name)
		if job.Uses != "" {
			t.Errorf("job %q calls a reusable workflow: %s", name, job.Uses)
		}
	}
	sort.Strings(jobs)
	if !reflect.DeepEqual(jobs, []string{"attest", "frontend", "package", "publish"}) {
		t.Fatalf("unexpected release jobs (routine tests belong locally): %v", jobs)
	}
	packageJob := workflow.Jobs["package"]
	platforms := make(map[string]string)
	for _, entry := range packageJob.Strategy.Matrix.Include {
		if _, exists := platforms[entry.Platform]; exists {
			t.Fatalf("duplicate release platform %q", entry.Platform)
		}
		platforms[entry.Platform] = entry.OS
	}
	want := map[string]string{
		"linux-amd64":   "ubuntu-latest",
		"windows-amd64": "windows-latest",
		"macos-arm64":   "macos-latest",
	}
	if !reflect.DeepEqual(platforms, want) {
		t.Fatalf("unexpected release build matrix: got %v, want %v", platforms, want)
	}
}

func TestScheduledDependabotVersionUpdatesAreDisabled(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "dependabot.yml"))
	if os.IsNotExist(err) {
		return // Without a configuration file, version updates are disabled.
	}
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Limit     *int   `yaml:"open-pull-requests-limit"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, update := range config.Updates {
		if update.Limit == nil || *update.Limit != 0 {
			t.Errorf("scheduled Dependabot version updates must be disabled for %q", update.Ecosystem)
		}
	}
}
