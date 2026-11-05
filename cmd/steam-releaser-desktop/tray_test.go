//go:build !bindings

package main

import (
	"testing"
	"time"
)

func TestTrayShowsWindowAndForceExits(t *testing.T) {
	show, exit, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	shown, exited, stopped := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{})
	go func() {
		handleTrayActions(show, exit, done, func() { shown <- struct{}{} }, func() { exited <- struct{}{} })
		close(stopped)
	}()
	show <- struct{}{}
	select {
	case <-shown:
	case <-time.After(time.Second):
		t.Fatal("tray Show did not restore the window")
	}
	exit <- struct{}{}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("tray Force Exit did not exit")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("tray event listener did not stop after exit")
	}
}

func TestTrayStopsWithoutExitingApplication(t *testing.T) {
	done, stopped := make(chan struct{}), make(chan struct{})
	close(done)
	go func() {
		handleTrayActions(nil, nil, done, func() { t.Error("show on shutdown") }, func() { t.Error("exit on shutdown") })
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("tray listener did not stop on shutdown")
	}
}
