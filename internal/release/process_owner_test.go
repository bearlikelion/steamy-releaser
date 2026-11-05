package release

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestCommandRunnerOwnerProcess(t *testing.T) {
	mode := os.Getenv("STEAM_RELEASER_TEST_OWNER")
	if mode == "" {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := (CommandRunner{}).Run(ctx, CommandPlan{
			Label:   "build fixture",
			Program: executable,
			Args:    []string{"-test.run=^TestCommandRunnerOwnedBuilderProcess$"},
		}, false)
		finished <- err
	}()
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if mode == "cancel" {
		cancel()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		return
	}
	// Exit without invoking cancellation or waiting for the runner to clean up.
	os.Exit(0)
}

func TestCommandRunnerOwnedBuilderProcess(t *testing.T) {
	if os.Getenv("STEAM_RELEASER_TEST_OWNER") == "" {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	descendant := exec.Command(executable, "-test.run=^TestCommandRunnerOwnedDescendantProcess$")
	if err := descendant.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = descendant.Process.Kill()
		_ = descendant.Wait()
	}()
	watchOwnedProcess(t, "builder")
}

func TestCommandRunnerOwnedDescendantProcess(t *testing.T) {
	if os.Getenv("STEAM_RELEASER_TEST_OWNER") == "" {
		return
	}
	watchOwnedProcess(t, "descendant")
}

func watchOwnedProcess(t *testing.T, role string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", os.Getenv("STEAM_RELEASER_TEST_OWNER_ADDRESS"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := fmt.Fprintln(connection, role); err != nil {
		t.Fatal(err)
	}
	// The test holds this connection open while the process is alive. Closing
	// it in test cleanup also lets a surviving fixture exit without OS APIs.
	_, _ = io.Copy(io.Discard, connection)
}

func TestCommandRunnerStopsOwnedProcessTree(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("parent lifetime protection is supported on the desktop platforms")
	}
	for _, mode := range []string{"cancel", "exit", "kill"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			owner := exec.CommandContext(ctx, executable, "-test.run=^TestCommandRunnerOwnerProcess$")
			owner.Env = append(os.Environ(), "STEAM_RELEASER_TEST_OWNER="+mode, "STEAM_RELEASER_TEST_OWNER_ADDRESS="+listener.Addr().String())
			input, err := owner.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			if err := owner.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = owner.Process.Kill()
				_ = owner.Wait()
			})
			processes := make(map[string]net.Conn)
			for range 2 {
				connection, err := listener.Accept()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = connection.Close() })
				if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
					t.Fatal(err)
				}
				role, err := bufio.NewReader(connection).ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				processes[role] = connection
			}
			if processes["builder\n"] == nil || processes["descendant\n"] == nil {
				t.Fatal("builder and its descendant did not both start")
			}
			if mode == "kill" {
				if err := owner.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if _, err := io.WriteString(input, "exit\n"); err != nil {
				t.Fatal(err)
			}
			if err := owner.Wait(); mode != "kill" && err != nil {
				t.Fatalf("owner exited unsuccessfully: %v", err)
			}
			for role, connection := range processes {
				if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 1)
				_, err := connection.Read(buffer)
				var netError net.Error
				if err == nil || (errors.As(err, &netError) && netError.Timeout()) {
					t.Fatalf("%s kept running after %s: %v", role, mode, err)
				}
			}
		})
	}
}
