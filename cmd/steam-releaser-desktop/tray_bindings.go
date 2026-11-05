//go:build bindings

package main

// Wails runs main while generating bindings. Keep that read-only build step
// from creating native tray resources or connecting to the desktop session.
func newSystemTray(func(), func()) (start, stop func()) {
	return func() {}, func() {}
}
