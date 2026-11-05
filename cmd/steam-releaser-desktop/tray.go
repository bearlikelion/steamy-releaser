//go:build !bindings

package main

import (
	_ "embed"
	"log"
	"runtime"
	"sync"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"
)

//go:embed build/appicon.png
var trayPNG []byte

//go:embed build/windows/icon.ico
var trayICO []byte

// Linux registers an X11 fallback for desktops such as i3 without a
// StatusNotifier host. Standalone tray tests exercise the portable DBus path.
var legacyTrayFactory func(func(), func()) (start, stop func(), available bool)

func newSystemTray(showWindow, forceExit func()) (start, stop func()) {
	if legacyTrayFactory != nil {
		if start, stop, available := legacyTrayFactory(showWindow, forceExit); available {
			return start, stop
		}
	}
	if runtime.GOOS == "linux" {
		bus, err := dbus.ConnectSessionBus()
		if err != nil {
			// The portable Linux tray requires a session bus. Its native cleanup
			// assumes connection succeeded, so do not start it when unavailable.
			return func() { log.Printf("Steam Releaser system tray unavailable: %v", err) }, func() {}
		}
		_ = bus.Close()
	}
	done := make(chan struct{})
	startNative, stopNative := systray.RunWithExternalLoop(func() {
		icon := trayPNG
		if runtime.GOOS == "windows" {
			icon = trayICO
		}
		systray.SetIcon(icon)
		systray.SetTitle("Steam Releaser")
		systray.SetTooltip("Steam Releaser — releases keep running when the window is closed")
		show := systray.AddMenuItem("Show Steam Releaser", "Restore the release window")
		systray.AddSeparator()
		exit := systray.AddMenuItem("Force Exit", "Cancel active work and exit Steam Releaser")
		go handleTrayActions(show.ClickedCh, exit.ClickedCh, done, showWindow, forceExit)
	}, nil)
	var once sync.Once
	return startNative, func() {
		once.Do(func() {
			close(done)
			stopNative()
		})
	}
}

func handleTrayActions(show, exit <-chan struct{}, done <-chan struct{}, showWindow, forceExit func()) {
	for {
		select {
		case <-show:
			showWindow()
		case <-exit:
			forceExit()
			return
		case <-done:
			return
		}
	}
}
