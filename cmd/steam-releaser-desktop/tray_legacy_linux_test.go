//go:build linux && !bindings

package main

import (
	"os"
	"testing"
	"time"
)

func TestLegacyTrayNeedsX11Display(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if _, _, available := newLegacySystemTray(func() {}, func() {}); available {
		t.Fatal("X11 tray fallback should not run without an X11 display")
	}
}

func TestLegacyTrayFallbackAndNativeActions(t *testing.T) {
	if os.Getenv("STEAM_RELEASER_TEST_TRAY") != "1" {
		t.Skip("requires an isolated dbus-run-session")
	}
	// This display is never opened: test selection and callbacks without
	// starting GTK or placing a tray icon on the user's desktop.
	t.Setenv("DISPLAY", ":unused")
	shown, exited := make(chan struct{}, 1), make(chan struct{}, 1)
	_, _, available := newLegacySystemTray(func() { shown <- struct{}{} }, func() { exited <- struct{}{} })
	if !available {
		t.Fatal("missing native X11 fallback when no StatusNotifier host exists")
	}
	steamReleaserTrayAction(0)
	steamReleaserTrayAction(1)
	for _, action := range []struct {
		label string
		done  <-chan struct{}
	}{{"Show", shown}, {"Force Exit", exited}} {
		select {
		case <-action.done:
		case <-time.After(time.Second):
			t.Fatalf("native tray action %s did not run", action.label)
		}
	}
}
