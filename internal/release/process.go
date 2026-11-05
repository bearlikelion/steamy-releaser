package release

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

const maxCapturedOutput = 4 << 20

type RunResult struct {
	Output          string
	Stdout          string
	Stderr          string
	OutputTruncated bool
}

// OutputStreams keeps each pipe's byte order for interpreting command output.
// Runners that only provide the combined log remain supported.
func (r RunResult) OutputStreams() []string {
	if r.Stdout != "" || r.Stderr != "" {
		return []string{r.Stdout, r.Stderr}
	}
	return []string{r.Output}
}

type ProcessRunner interface {
	Run(ctx context.Context, plan CommandPlan, interactive bool) (RunResult, error)
}

// InputProcessRunner runs a process with input supplied by a caller, such as a
// desktop login form. It reports output immediately, including partial prompts.
type InputProcessRunner interface {
	RunWithInput(ctx context.Context, plan CommandPlan, input io.Reader) (RunResult, error)
}

type CommandRunner struct {
	Reporter Reporter
}

func (r CommandRunner) Run(ctx context.Context, plan CommandPlan, interactive bool) (RunResult, error) {
	return r.run(ctx, plan, interactive, nil, interactive)
}

func (r CommandRunner) RunWithInput(ctx context.Context, plan CommandPlan, input io.Reader) (RunResult, error) {
	return r.run(ctx, plan, false, input, true)
}

func (r CommandRunner) run(ctx context.Context, plan CommandPlan, interactive bool, input io.Reader, streamPrompts bool) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, fmt.Errorf("%s cancelled: %w", plan.Label, err)
	}
	reporter := r.Reporter
	if reporter == nil {
		reporter = NopReporter{}
	}
	reporter.Report(Event{Level: LevelTrace, Message: FormatCommand(plan.Program, plan.Args), Target: plan.Label})
	for _, warning := range plan.Warnings {
		reporter.Report(Event{Level: LevelWarning, Message: warning, Target: plan.Label})
	}

	command := exec.Command(plan.Program, plan.Args...)
	command.Dir = plan.WorkingDir
	if interactive {
		command.Stdin = os.Stdin
	} else if streamPrompts {
		command.Stdin = input
	}
	if len(plan.Environment) > 0 {
		command.Env = command.Environ()
		keys := make([]string, 0, len(plan.Environment))
		for key := range plan.Environment {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			command.Env = append(command.Env, key+"="+plan.Environment[key])
		}
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return RunResult{}, err
	}
	processes, err := startProcess(command)
	if err != nil {
		return RunResult{}, fmt.Errorf("start %s: %w", plan.Label, err)
	}
	defer processes.Close()

	var output, stdoutOutput, stderrOutput cappedBuffer
	var outputMu sync.Mutex
	var readers sync.WaitGroup
	readers.Add(2)
	retain := func(fragment []byte, captured *cappedBuffer) {
		outputMu.Lock()
		before := output.Len()
		_, _ = output.Write(fragment)
		// The separate captures retain only bytes present in the bounded
		// combined log. A truncated log cannot confirm SteamCMD success.
		_, _ = captured.Write(fragment[:output.Len()-before])
		outputMu.Unlock()
	}
	stream := func(reader io.Reader, level Level, captured *cappedBuffer) {
		defer readers.Done()
		if streamPrompts {
			chunk := make([]byte, 4096)
			for {
				n, readErr := reader.Read(chunk)
				if n > 0 {
					message := string(chunk[:n])
					retain(chunk[:n], captured)
					reporter.Report(Event{Level: level, Message: message, Target: plan.Label})
				}
				if readErr != nil {
					return
				}
			}
		}
		buffered := bufio.NewReaderSize(reader, 64*1024)
		for {
			fragment, readErr := buffered.ReadSlice('\n')
			if len(fragment) > 0 {
				retain(fragment, captured)
				message := bytes.TrimSuffix(bytes.TrimSuffix(fragment, []byte{'\n'}), []byte{'\r'})
				reporter.Report(Event{Level: level, Message: string(message), Target: plan.Label})
			}
			// A long line fills the buffer; keep draining the pipe so the child
			// can exit. Each event and the retained output remain bounded.
			if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
				return
			}
		}
	}
	go stream(stdout, LevelInfo, &stdoutOutput)
	go stream(stderr, LevelWarning, &stderrOutput)
	waited := make(chan error, 1)
	go func() {
		// Wait closes the pipes, so drain both streams before reaping the process.
		readers.Wait()
		waited <- command.Wait()
	}()
	cancelled := false
	select {
	case err = <-waited:
	case <-ctx.Done():
		cancelled = true
		_ = processes.Kill()
		err = <-waited
	}
	result := RunResult{
		Output: output.String(), Stdout: stdoutOutput.String(), Stderr: stderrOutput.String(),
		OutputTruncated: output.truncated || stdoutOutput.truncated || stderrOutput.truncated,
	}
	if cancelled {
		return result, fmt.Errorf("%s cancelled: %w", plan.Label, ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("%s failed: %w", plan.Label, err)
	}
	return result, nil
}

func FormatCommand(program string, args []string) string {
	parts := []string{quoteArgument(program)}
	for _, argument := range args {
		parts = append(parts, quoteArgument(argument))
	}
	return strings.Join(parts, " ")
}

func quoteArgument(value string) string {
	if value == "" || strings.ContainsAny(value, " \t\n\"") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}

type cappedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	remaining := maxCapturedOutput - b.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || len(value) > 0
		return len(value), nil
	}
	if len(value) > remaining {
		b.truncated = true
		_, _ = b.Buffer.Write(value[:remaining])
		return len(value), nil
	}
	return b.Buffer.Write(value)
}

func (b *cappedBuffer) WriteString(value string) {
	_, _ = b.Write([]byte(value))
}

func (b *cappedBuffer) WriteByte(value byte) error {
	_, _ = b.Write([]byte{value})
	return nil
}
