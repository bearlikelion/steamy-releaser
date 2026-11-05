//go:build unix

package safefile

import (
	"os"

	"golang.org/x/sys/unix"
)

// OpenCandidate avoids blocking if a file changes into a FIFO before open.
// The caller must check the opened file is regular before reading it.
func OpenCandidate(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
}
