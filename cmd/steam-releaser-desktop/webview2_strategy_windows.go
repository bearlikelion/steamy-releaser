//go:build windows && !wv2runtime.browser && !bindings

package main

// Fail closed for Windows desktop builds that would select Wails' default
// automatic downloader. Wails' bindings-only helper does not start the app.
// See docs/desktop.md for the supported build flags.
var _ = buildWindowsDesktopWithWebView2BrowserStrategy
