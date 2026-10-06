package core

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

// lifecycleVoice can pause before a device operation takes its own lock. A
// mutex inside the real engine cannot order goroutines that have not reached
// that mutex yet, so core must never overlap these operations in the first place.
type lifecycleVoice struct {
	fakeVoice
	lifeMu    sync.Mutex
	starts    int
	stops     int
	running   bool
	devices   [][2]string
	startGate func(int, string, string) error
	stopGate  func(int)
	started   chan struct{}
	stopped   chan struct{}
}

func (v *lifecycleVoice) Start(capture, playback string) error {
	v.lifeMu.Lock()
	v.starts++
	index := v.starts
	v.devices = append(v.devices, [2]string{capture, playback})
	v.lifeMu.Unlock()
	if v.startGate != nil {
		if err := v.startGate(index, capture, playback); err != nil {
			return err
		}
	}
	v.lifeMu.Lock()
	v.running = true
	v.lifeMu.Unlock()
	v.started <- struct{}{}
	return nil
}

func (v *lifecycleVoice) Stop() {
	v.lifeMu.Lock()
	v.stops++
	index := v.stops
	v.lifeMu.Unlock()
	if v.stopGate != nil {
		v.stopGate(index)
	}
	v.lifeMu.Lock()
	v.running = false
	v.lifeMu.Unlock()
	v.stopped <- struct{}{}
}

func newLifecycleApp(t *testing.T) (*App, *lifecycleVoice) {
	t.Helper()
	app, _, _ := newTestApp(t)
	v := &lifecycleVoice{started: make(chan struct{}, 32), stopped: make(chan struct{}, 32)}
	app.SetVoice(v)
	t.Cleanup(app.Shutdown)
	return app, v
}

func lifecycleWait(t *testing.T, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(2 * time.Second):
		t.Fatal("voice lifecycle operation did not finish")
	}
}

func lifecycleQuiet(t *testing.T, event <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-event:
		t.Error(message)
	case <-time.After(30 * time.Millisecond):
	}
}

func lifecycleGate(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return gate, release
}

func TestVoiceLifecycleDisconnectDuringStart(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.startGate = func(int, string, string) error { entered <- struct{}{}; <-gate; return nil }
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, entered)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	lifecycleQuiet(t, voice.stopped, "Stop overlapped the pending Start")
	release()
	lifecycleWait(t, voice.started)
	lifecycleWait(t, voice.stopped)
	voice.lifeMu.Lock()
	defer voice.lifeMu.Unlock()
	if voice.running {
		t.Fatal("the microphone remained open after disconnect")
	}
}

func TestVoiceLifecycleNewConnectWaitsForOldStop(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.stopGate = func(index int) {
		if index == 1 {
			entered <- struct{}{}
			<-gate
		}
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	lifecycleWait(t, entered)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleQuiet(t, voice.started, "new Start overlapped the previous session's Stop")
	release()
	lifecycleWait(t, voice.stopped)
	lifecycleWait(t, voice.started)
	voice.lifeMu.Lock()
	defer voice.lifeMu.Unlock()
	if !voice.running {
		t.Fatal("an old stop silenced the new connection")
	}
}

func TestVoiceLifecycleDeviceChangeCannotRestartAfterDisconnect(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.stopGate = func(index int) {
		if index == 1 {
			entered <- struct{}{}
			<-gate
		}
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	app.SelectDevices("capture-new", "playback-new")
	lifecycleWait(t, entered)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	release()
	lifecycleWait(t, voice.stopped)
	lifecycleQuiet(t, voice.started, "queued device change reopened the microphone after disconnect")
}

func TestVoiceLifecycleIgnoresDeviceWorkWhileDisconnected(t *testing.T) {
	app, voice := newLifecycleApp(t)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	lifecycleWait(t, voice.stopped)
	app.SelectDevices("next-capture", "next-playback")
	app.HandleDeviceLost()
	lifecycleQuiet(t, voice.started, "a stale device-lost callback restarted a disconnected client")
}

func TestVoiceLifecycleUsesLatestDeviceSelection(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.startGate = func(index int, _, _ string) error {
		if index == 1 {
			entered <- struct{}{}
			<-gate
		}
		return nil
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, entered)
	app.SelectDevices("obsolete-capture", "obsolete-playback")
	app.SelectDevices("latest-capture", "latest-playback")
	lifecycleQuiet(t, voice.stopped, "device restart overlapped initial Start")
	release()
	lifecycleWait(t, voice.started)
	lifecycleWait(t, voice.stopped)
	lifecycleWait(t, voice.started)
	voice.lifeMu.Lock()
	defer voice.lifeMu.Unlock()
	if len(voice.devices) != 2 || voice.devices[1] != [2]string{"latest-capture", "latest-playback"} {
		t.Fatalf("started on devices %v; wanted only the latest pair after initial Start", voice.devices)
	}
}

func TestVoiceLifecycleShutdownBoundsBlockedStartAndRejectsFurtherWork(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.startGate = func(int, string, string) error { entered <- struct{}{}; <-gate; return nil }
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, entered)
	shutdown := make(chan struct{})
	go func() { app.Shutdown(); close(shutdown) }()
	select {
	case <-shutdown:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("shutdown waited indefinitely for an uncancellable Start")
	}
	release()
	lifecycleWait(t, voice.started)
	lifecycleWait(t, voice.stopped)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	app.SelectDevices("capture", "playback")
	app.HandleDeviceLost()
	app.Shutdown()
	lifecycleQuiet(t, voice.started, "a callback restarted audio after shutdown")
}

func TestVoiceLifecycleShutdownBoundsBlockedStop(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.stopGate = func(int) { entered <- struct{}{}; <-gate }
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	shutdown := make(chan struct{})
	go func() { app.Shutdown(); close(shutdown) }()
	lifecycleWait(t, entered)
	select {
	case <-shutdown:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("shutdown waited indefinitely for an uncancellable Stop")
	}
	release()
	lifecycleWait(t, voice.stopped)
}

func TestVoiceLifecycleDeviceLostFallsBackOnlyInCurrentSession(t *testing.T) {
	app, voice := newLifecycleApp(t)
	app.SelectDevices("capture", "playback")
	voice.startGate = func(index int, _, _ string) error {
		if index == 2 {
			return errors.New("device unavailable")
		}
		return nil
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	app.HandleDeviceLost()
	lifecycleWait(t, voice.stopped)
	lifecycleWait(t, voice.started)
	voice.lifeMu.Lock()
	defer voice.lifeMu.Unlock()
	if len(voice.devices) != 3 || voice.devices[1] != [2]string{"capture", "playback"} || voice.devices[2] != [2]string{} {
		t.Fatalf("device-loss attempts = %v, want selected devices then defaults", voice.devices)
	}
}

func TestVoiceLifecycleDoesNotFallbackAfterDisconnect(t *testing.T) {
	app, voice := newLifecycleApp(t)
	gate, release := lifecycleGate(t)
	entered := make(chan struct{}, 1)
	voice.startGate = func(index int, _, _ string) error {
		if index == 2 {
			entered <- struct{}{}
			<-gate
			return errors.New("device unavailable")
		}
		return nil
	}
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	go app.HandleDeviceLost()
	lifecycleWait(t, entered)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	release()
	lifecycleQuiet(t, voice.started, "fallback opened default devices after disconnect")
}

func TestVoiceLifecycleEngineCallbacksDoNotHoldStateLock(t *testing.T) {
	app, voice := newLifecycleApp(t)
	voice.startGate = func(int, string, string) error { _ = app.Status(); return nil }
	voice.stopGate = func(int) { _ = app.Status() }
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateConnected})
	lifecycleWait(t, voice.started)
	app.HandleStatus(domain.ConnectionStatus{State: domain.StateDisconnected})
	lifecycleWait(t, voice.stopped)
}
