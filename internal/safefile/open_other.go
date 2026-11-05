//go:build !unix

package safefile

import "os"

// OpenCandidate opens a candidate file. The caller must verify its type.
func OpenCandidate(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
