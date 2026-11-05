//go:build windows

package release

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct {
	job windows.Handle
}

func startProcess(command *exec.Cmd) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create process lifetime guard: %w", err)
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure process lifetime guard: %w", err)
	}
	tree := windowsProcessTree{job: job}
	// Attach the builder before it can spawn children, so every descendant
	// belongs to the job that the kernel closes when the application exits.
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := command.Start(); err != nil {
		_ = tree.Close()
		return nil, err
	}
	if err := tree.attachAndResume(uint32(command.Process.Pid)); err != nil {
		_ = command.Process.Kill()
		_ = tree.Close()
		_ = command.Wait()
		return nil, fmt.Errorf("attach process lifetime guard: %w", err)
	}
	return tree, nil
}

func (tree windowsProcessTree) attachAndResume(pid uint32) error {
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		pid,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(tree.job, handle); err != nil {
		return err
	}
	return resumeInitialThread(pid)
}

func resumeInitialThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			defer windows.CloseHandle(thread)
			_, err = windows.ResumeThread(thread)
			return err
		}
	}
	return fmt.Errorf("initial thread for process %d was not found", pid)
}

func (tree windowsProcessTree) Kill() error {
	return windows.TerminateJobObject(tree.job, 1)
}

func (tree windowsProcessTree) Close() error {
	return windows.CloseHandle(tree.job)
}
