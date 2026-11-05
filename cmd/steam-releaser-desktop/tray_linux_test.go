//go:build !bindings

package main

import (
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type trayWatcher struct {
	registered chan string
}

func (w *trayWatcher) RegisterStatusNotifierItem(sender dbus.Sender, _ string) *dbus.Error {
	w.registered <- string(sender)
	return nil
}

type trayMenuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

// Run only on an isolated session bus: this test must never create a tray icon
// on the user's desktop. See the CI command using dbus-run-session.
func TestSystemTrayRegistrationAndMenu(t *testing.T) {
	if os.Getenv("STEAM_RELEASER_TEST_TRAY") != "1" {
		t.Skip("requires an isolated dbus-run-session")
	}
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	watcher := &trayWatcher{registered: make(chan string, 1)}
	if err := bus.Export(watcher, "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	shown, exited := make(chan struct{}, 1), make(chan struct{}, 1)
	start, stop := newSystemTray(func() { shown <- struct{}{} }, func() { exited <- struct{}{} })
	start()
	defer stop()
	var sender string
	select {
	case sender = <-watcher.registered:
	case <-time.After(5 * time.Second):
		t.Fatal("tray did not register with the host")
	}
	item := bus.Object(sender, "/StatusNotifierItem")
	title, err := item.GetProperty("org.kde.StatusNotifierItem.Title")
	if err != nil || title.Value() != "Steam Releaser" {
		t.Fatalf("tray title: %v, %v", title, err)
	}
	icon, err := item.GetProperty("org.kde.StatusNotifierItem.IconPixmap")
	if err != nil || icon.Signature().String() != "a(iiay)" {
		t.Fatalf("tray icon: %v, %v", icon.Signature(), err)
	}
	menu := bus.Object(sender, "/StatusNotifierItem/menu")
	var revision uint32
	var layout trayMenuLayout
	if err := menu.Call("com.canonical.dbusmenu.GetLayout", 0, int32(0), int32(-1), []string{"label"}).Store(&revision, &layout); err != nil {
		t.Fatal(err)
	}
	labels := make(map[string]int32)
	for _, child := range layout.Children {
		var entry trayMenuLayout
		if err := dbus.Store([]any{child.Value()}, &entry); err != nil {
			t.Fatal(err)
		}
		if label, ok := entry.Properties["label"].Value().(string); ok {
			labels[label] = entry.ID
		}
	}
	for _, action := range []struct {
		label string
		done  <-chan struct{}
	}{
		{"Show Steam Releaser", shown},
		{"Force Exit", exited},
	} {
		id, found := labels[action.label]
		if !found {
			t.Fatalf("tray menu missing %q: %v", action.label, labels)
		}
		if err := menu.Call("com.canonical.dbusmenu.Event", 0, id, "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
			t.Fatal(err)
		}
		select {
		case <-action.done:
		case <-time.After(time.Second):
			t.Fatalf("tray menu action %q did not run", action.label)
		}
	}
	stop()
}
