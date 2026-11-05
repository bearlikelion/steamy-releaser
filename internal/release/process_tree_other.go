//go:build !unix && !windows

package release

import "os/exec"

type directProcessTree struct {
	command *exec.Cmd
}

func startProcess(command *exec.Cmd) (processTree, error) {
	if err := command.Start(); err != nil {
		return nil, err
	}
	return directProcessTree{command: command}, nil
}

func (tree directProcessTree) Kill() error {
	return tree.command.Process.Kill()
}

func (directProcessTree) Close() error {
	return nil
}
