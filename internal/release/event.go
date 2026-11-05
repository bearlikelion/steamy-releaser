package release

import (
	"context"
	"sync"
)

type Level string

const (
	LevelTrace   Level = "trace"
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

type Event struct {
	Level       Level  `json:"level"`
	Message     string `json:"message"`
	Target      string `json:"target,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Fingerprint string `json:"-"`
}

type Reporter interface {
	Report(Event)
}

type ReporterFunc func(Event)

func (f ReporterFunc) Report(event Event) {
	f(event)
}

type NopReporter struct{}

func (NopReporter) Report(Event) {}

type LockedReporter struct {
	mu       sync.Mutex
	reporter Reporter
}

func LockReporter(reporter Reporter) Reporter {
	if reporter == nil {
		reporter = NopReporter{}
	}
	return &LockedReporter{reporter: reporter}
}

func (r *LockedReporter) Report(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reporter.Report(event)
}

func CheckCancelled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
