package main

import (
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
)

func newWindowMenu(hideWindow, forceExit func()) *menu.Menu {
	applicationMenu := menu.NewMenu()
	if runtime.GOOS == "darwin" {
		applicationMenu.Append(menu.AppMenu())
	}
	file := applicationMenu.AddSubmenu("File")
	file.AddText("Hide to Tray", nil, func(*menu.CallbackData) { hideWindow() })
	file.AddSeparator()
	file.AddText("Force Exit", keys.CmdOrCtrl("q"), func(*menu.CallbackData) { forceExit() })
	if runtime.GOOS == "darwin" {
		applicationMenu.Append(menu.EditMenu())
		applicationMenu.Append(menu.WindowMenu())
	}
	return applicationMenu
}
