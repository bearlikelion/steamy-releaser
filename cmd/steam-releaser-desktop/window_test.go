package main

import (
	"context"
	"testing"
	"time"
)

func TestCloseToTrayKeepsOperationAliveAndRestoresWindow(t *testing.T) {
	app := NewDesktopApp()
	app.startup(context.Background())
	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()
	app.cancel, app.busy = cancel, true
	hidden, shown := false, false
	app.window.hide = func(context.Context) { hidden = true }
	app.window.show = func(context.Context) { shown = true }
	app.HideWindow()
	if !hidden || !app.busy || ctx.Err() != nil {
		t.Fatal("closing to the tray must hide the window and keep the operation alive")
	}
	app.ShowWindow()
	if !shown || !app.busy || ctx.Err() != nil {
		t.Fatal("restoring from the tray must keep the operation alive")
	}
}

func TestForceExitCancelsAndWaitsForOperationBeforeQuitting(t *testing.T) {
	app := NewDesktopApp()
	app.startup(context.Background())
	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()
	app.cancel = cancel
	app.operationDone = make(chan struct{})
	quit := make(chan struct{}, 1)
	app.window.quit = func(context.Context) { quit <- struct{}{} }
	exited := make(chan struct{})
	go func() {
		app.ForceExit()
		close(exited)
	}()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("force exit did not cancel the operation")
	}
	select {
	case <-quit:
		t.Fatal("quit before the cancelled operation finished")
	default:
	}
	if err := app.StartOperation(OperationRequest{Action: "plan"}); err == nil {
		t.Fatal("accepted a new operation while exiting")
	}
	close(app.operationDone)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("force exit did not quit after the operation finished")
	}
	<-quit
	app.ForceExit()
	select {
	case <-quit:
		t.Fatal("force exit must be idempotent")
	default:
	}
}

func TestForceExitWithoutActiveOperation(t *testing.T) {
	app := NewDesktopApp()
	app.startup(context.Background())
	quit := false
	app.window.quit = func(context.Context) { quit = true }
	app.ForceExit()
	if !quit {
		t.Fatal("force exit did not quit an idle application")
	}
}

func TestNativeQuitCancelsOperation(t *testing.T) {
	app := NewDesktopApp()
	app.startup(context.Background())
	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()
	app.cancel = cancel
	if app.beforeClose(app.ctx) {
		t.Fatal("native Quit should be allowed after cancellation")
	}
	if ctx.Err() == nil {
		t.Fatal("native Quit did not cancel active work")
	}
}

func TestShutdownCancelsAndWaitsForOperation(t *testing.T) {
	app := NewDesktopApp()
	app.startup(context.Background())
	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()
	app.cancel = cancel
	app.operationDone = make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		app.shutdown(app.ctx)
		close(stopped)
	}()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the operation")
	}
	select {
	case <-stopped:
		t.Fatal("shutdown returned before the cancelled operation finished")
	case <-time.After(50 * time.Millisecond):
	}
	if err := app.StartOperation(OperationRequest{Action: "plan"}); err == nil {
		t.Fatal("accepted a new operation during shutdown")
	}
	close(app.operationDone)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not return after the operation finished")
	}
}
