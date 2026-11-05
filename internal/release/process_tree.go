package release

// processTree owns the command's lifetime and cancellation. Desktop platform
// adapters also stop owned processes when the application exits unexpectedly.
type processTree interface {
	Kill() error
	Close() error
}
