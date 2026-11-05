package main

import (
	"testing"

	"github.com/wailsapp/wails/v2/pkg/menu"
)

func TestNativeFileMenuHidesToTrayAndForceExits(t *testing.T) {
	hidden, exited := false, false
	windowMenu := newWindowMenu(func() { hidden = true }, func() { exited = true })
	var file *menu.Menu
	for _, item := range windowMenu.Items {
		if item.Label == "File" {
			file = item.SubMenu
		}
	}
	if file == nil || len(file.Items) != 3 {
		t.Fatal("missing native File menu")
	}
	if file.Items[0].Label != "Hide to Tray" || !file.Items[1].IsSeparator() || file.Items[2].Label != "Force Exit" {
		t.Fatal("native File menu is missing hide and exit actions")
	}
	file.Items[0].Click(nil)
	if !hidden || exited {
		t.Fatal("Hide to Tray should hide without exiting")
	}
	file.Items[2].Click(nil)
	if !exited {
		t.Fatal("Force Exit should terminate the application")
	}
	if file.Items[2].Accelerator == nil {
		t.Fatal("Force Exit should have a native keyboard shortcut")
	}
}
