//go:build unix

package release

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

type unixProcessTree struct {
	guard    *exec.Cmd
	lifetime *os.File
}

func startProcess(command *exec.Cmd) (processTree, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// The guard owns the process group before the builder starts. Its pipe has
	// a single writer in this application, so EOF also detects abrupt exits.
	// A POSIX shell provides the same behavior on Linux and macOS without
	// launching another copy of the desktop application.
	guard := exec.Command("/bin/sh", "-c", "read -r completion; kill -s KILL 0")
	guard.Stdin = reader
	guard.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := guard.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("start process lifetime guard: %w", err)
	}
	_ = reader.Close()
	tree := unixProcessTree{guard: guard, lifetime: writer}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: guard.Process.Pid}
	if err := command.Start(); err != nil {
		_ = tree.Close()
		return nil, err
	}
	return tree, nil
}

func (tree unixProcessTree) Kill() error {
	return syscall.Kill(-tree.guard.Process.Pid, syscall.SIGKILL)
}

func (tree unixProcessTree) Close() error {
	err := tree.lifetime.Close()
	// Stop any remaining descendants and reap the guard on completion too.
	_ = tree.Kill()
	_ = tree.guard.Wait()
	return err
}
