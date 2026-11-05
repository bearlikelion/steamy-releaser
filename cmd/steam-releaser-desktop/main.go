package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if err := runDesktop(); err != nil {
		log.Fatal(err)
	}
}

func runDesktop() error {
	application := NewDesktopApp()
	started := make(chan struct{})
	showWindow := func() {
		<-started
		application.ShowWindow()
	}
	hideWindow := func() {
		<-started
		application.HideWindow()
	}
	forceExit := func() {
		<-started
		application.ForceExit()
	}
	startTray, stopTray := newSystemTray(showWindow, forceExit)
	startTray()
	defer stopTray()
	return wails.Run(&options.App{
		Title:             "Steam Releaser",
		Width:             1240,
		Height:            820,
		MinWidth:          980,
		MinHeight:         680,
		HideWindowOnClose: true,
		Menu:              newWindowMenu(hideWindow, forceExit),
		BackgroundColour:  &options.RGBA{R: 10, G: 17, B: 26, A: 255},
		AssetServer:       &assetserver.Options{Assets: assets},
		OnStartup: func(ctx context.Context) {
			application.startup(ctx)
			close(started)
		},
		OnShutdown:    application.shutdown,
		OnBeforeClose: application.beforeClose,
		Bind:          []any{application},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.gdleap.steam-releaser",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { showWindow() },
		},
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
	})
}
