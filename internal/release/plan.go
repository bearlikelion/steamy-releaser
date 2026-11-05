package release

type CommandPlan struct {
	Label       string            `json:"label" yaml:"label"`
	Program     string            `json:"program" yaml:"program"`
	Args        []string          `json:"args" yaml:"args"`
	WorkingDir  string            `json:"working_dir" yaml:"working_dir"`
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
	Warnings    []string          `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

type TargetPlan struct {
	Name         string       `json:"name" yaml:"name"`
	Platform     string       `json:"platform" yaml:"platform"`
	Architecture string       `json:"architecture,omitempty" yaml:"architecture,omitempty"`
	DepotID      uint64       `json:"depot_id" yaml:"depot_id"`
	Output       string       `json:"output" yaml:"output"`
	Content      string       `json:"content" yaml:"content"`
	Command      *CommandPlan `json:"command,omitempty" yaml:"command,omitempty"`
}

type UploadPlan struct {
	AppID    uint64   `json:"app_id" yaml:"app_id"`
	DepotIDs []uint64 `json:"depot_ids" yaml:"depot_ids"`
	// Generated scripts use a <run-id> template; the upload result contains
	// the actual path. User-provided scripts retain their exact paths.
	BuildScript string `json:"build_script" yaml:"build_script"`
	Preview     bool   `json:"preview" yaml:"preview"`
	SetLive     string `json:"set_live,omitempty" yaml:"set_live,omitempty"`
}

type Plan struct {
	Project     string        `json:"project" yaml:"project"`
	Profile     string        `json:"profile" yaml:"profile"`
	Engine      string        `json:"engine" yaml:"engine"`
	Parallelism int           `json:"parallelism" yaml:"parallelism"`
	Prepare     []CommandPlan `json:"prepare,omitempty" yaml:"prepare,omitempty"`
	Targets     []TargetPlan  `json:"targets" yaml:"targets"`
	Upload      UploadPlan    `json:"upload" yaml:"upload"`
}
