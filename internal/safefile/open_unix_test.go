//go:build linux || darwin

package safefile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenCandidateDoesNotBlockOnNamedPipe(t *testing.T) {
	directory := t.TempDir()
	pipe := filepath.Join(directory, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("named pipes unavailable: %v", err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	type result struct {
		file *os.File
		err  error
	}
	done := make(chan result, 1)
	go func() {
		file, err := OpenCandidate(root, "pipe")
		done <- result{file: file, err: err}
	}()
	select {
	case opened := <-done:
		if opened.err != nil {
			t.Fatal(opened.err)
		}
		defer opened.file.Close()
		info, err := opened.file.Stat()
		if err != nil || info.Mode().IsRegular() {
			t.Fatalf("named pipe appeared to be regular: %v", err)
		}
	case <-time.After(5 * time.Second):
		go func() {
			writer, err := os.OpenFile(pipe, os.O_WRONLY, 0)
			if err == nil {
				_ = writer.Close()
			}
		}()
		select {
		case opened := <-done:
			if opened.file != nil {
				_ = opened.file.Close()
			}
		case <-time.After(2 * time.Second):
		}
		t.Fatal("opening a named pipe blocked")
	}
}
