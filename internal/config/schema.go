package config

import "strings"

const CurrentVersion = 1
const DefaultProfile = "release"

type Config struct {
	Version  int                `yaml:"version,omitempty" json:"version"`
	Project  Project            `yaml:"project,omitempty" json:"project"`
	Tools    Tools              `yaml:"tools,omitempty" json:"tools"`
	Profiles map[string]Profile `yaml:"profiles,omitempty" json:"profiles"`
}

// fileConfig accepts a single app at the top level or explicitly named profiles.
// Both forms are resolved to Config before any release operation runs.
type fileConfig struct {
	Config   `yaml:",inline"`
	*Profile `yaml:",inline"`
}

type Project struct {
	Name   string `yaml:"name,omitempty" json:"name"`
	Root   string `yaml:"root,omitempty" json:"root"`
	Engine Engine `yaml:"engine,omitempty" json:"engine"`
}

type Engine struct {
	Type              string `yaml:"type,omitempty" json:"type"`
	Executable        string `yaml:"executable,omitempty" json:"executable,omitempty"`
	ImportBeforeBuild *bool  `yaml:"import_before_build,omitempty" json:"import_before_build,omitempty"`
	MaxParallel       int    `yaml:"max_parallel,omitempty" json:"max_parallel,omitempty"`
	ExecuteMethod     string `yaml:"execute_method,omitempty" json:"execute_method,omitempty"`
}

func (e Engine) ShouldImport() bool {
	return e.ImportBeforeBuild == nil || *e.ImportBeforeBuild
}

type Tools struct {
	SteamCMD string `yaml:"steamcmd,omitempty" json:"steamcmd,omitempty"`
}

type Profile struct {
	AppID       uint64            `yaml:"app_id" json:"app_id"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	BuildOutput string            `yaml:"build_output,omitempty" json:"build_output,omitempty"`
	ExistingVDF string            `yaml:"existing_vdf,omitempty" json:"existing_vdf,omitempty"`
	Targets     map[string]Target `yaml:"targets" json:"targets"`
	PostBuild   []CopyRule        `yaml:"post_build,omitempty" json:"post_build,omitempty"`
}

func (p Profile) EffectiveDescription() string {
	if strings.TrimSpace(p.Description) == "" {
		return "Build uploaded by Steam Releaser"
	}
	return p.Description
}

func (p Profile) EffectiveBuildOutput() string {
	if strings.TrimSpace(p.BuildOutput) == "" {
		return ".steam-releaser/output"
	}
	return p.BuildOutput
}

type Target struct {
	Platform     string        `yaml:"platform,omitempty" json:"platform"`
	Architecture string        `yaml:"architecture,omitempty" json:"architecture,omitempty"`
	DepotID      uint64        `yaml:"depot_id" json:"depot_id"`
	Output       string        `yaml:"output,omitempty" json:"output"`
	Content      string        `yaml:"content,omitempty" json:"content"`
	Build        Build         `yaml:"build,omitempty" json:"build"`
	Required     []string      `yaml:"required,omitempty" json:"required,omitempty"`
	Mappings     []FileMapping `yaml:"mappings,omitempty" json:"mappings,omitempty"`
	Exclusions   []string      `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
}

type Build struct {
	Preset        string            `yaml:"preset,omitempty" json:"preset,omitempty"`
	Target        string            `yaml:"target,omitempty" json:"target,omitempty"`
	Profile       string            `yaml:"profile,omitempty" json:"profile,omitempty"`
	ExecuteMethod string            `yaml:"execute_method,omitempty" json:"execute_method,omitempty"`
	NoGraphics    *bool             `yaml:"no_graphics,omitempty" json:"no_graphics,omitempty"`
	Command       []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Args          []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env           map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type FileMapping struct {
	LocalPath string `yaml:"local_path,omitempty" json:"local_path"`
	DepotPath string `yaml:"depot_path,omitempty" json:"depot_path"`
	Recursive *bool  `yaml:"recursive,omitempty" json:"recursive"`
}

func (m FileMapping) Values() (string, string, bool) {
	local, depot, recursive := m.LocalPath, m.DepotPath, true
	if local == "" {
		local = "*"
	}
	if depot == "" {
		depot = "."
	}
	if m.Recursive != nil {
		recursive = *m.Recursive
	}
	return local, depot, recursive
}

type CopyRule struct {
	From    string   `yaml:"from" json:"from"`
	To      string   `yaml:"to,omitempty" json:"to,omitempty"`
	Targets []string `yaml:"targets,omitempty" json:"targets,omitempty"`
}

type Document struct {
	Path       string `json:"path"`
	Config     Config `json:"config"`
	ConfigHash string `json:"-"`
}
