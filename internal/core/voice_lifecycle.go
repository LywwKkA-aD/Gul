package core

import (
	"time"

	"github.com/LywwKkA-aD/Gul/internal/domain"
)

const voiceShutdownGrace = 250 * time.Millisecond

// voiceLifecycle records desired and applied device state under App.mu. Only
// its worker calls Start/Stop, outside every App lock: either call can wait for
// engine callbacks that read the App. The worker exits as soon as it catches
// up, so an idle or disconnected App has no parked lifecycle goroutine.
type voiceLifecycle struct {
	generation uint64
	applied    uint64
	wanted     bool
	running    bool
	fallback   bool
	closed     bool
	done       chan struct{}
}

// setVoiceRunningLocked is called in the same critical section that changes
// connection status. Reconnecting keeps the existing devices; a full session
// change advances the generation even if it overtakes a pending Start/Stop.
func (a *App) setVoiceRunningLocked(wanted bool) {
	life := &a.voiceLife
	if life.closed || life.wanted == wanted {
		return
	}
	life.wanted = wanted
	life.generation++
	life.fallback = false
	a.scheduleVoiceLocked()
}

// restartVoiceLocked invalidates an older device selection or recovery. An
// already queued device-lost callback is harmless after disconnect/shutdown.
func (a *App) restartVoiceLocked(fallback bool) bool {
	life := &a.voiceLife
	if life.closed || !life.wanted || a.voice == nil ||
		(a.status.State != domain.StateConnected && a.status.State != domain.StateReconnecting) {
		return false
	}
	life.generation++
	life.fallback = fallback
	a.scheduleVoiceLocked()
	return true
}

func (a *App) scheduleVoiceLocked() {
	if a.voice == nil || a.voiceLife.done != nil {
		return
	}
	done := make(chan struct{})
	a.voiceLife.done = done
	go a.reconcileVoice(done)
}

func (a *App) reconcileVoice(done chan struct{}) {
	for {
		a.mu.Lock()
		life := &a.voiceLife
		if life.applied == life.generation {
			life.done = nil
			close(done)
			a.mu.Unlock()
			return
		}
		generation := life.generation
		voice := a.voice
		running, wanted, fallback := life.running, life.wanted && !life.closed, life.fallback
		captureID, playbackID := a.captureID, a.playbackID
		if !running && !wanted {
			life.applied = generation
			a.mu.Unlock()
			continue
		}
		a.mu.Unlock()

		if running {
			voice.Stop()
			a.mu.Lock()
			a.voiceLife.running = false
			a.mu.Unlock()
			// Stop may have waited for devices. Re-read the latest session and
			// selection before opening anything, rather than using this snapshot.
			continue
		}

		err := voice.Start(captureID, playbackID)
		if err != nil {
			a.log.Error("voice engine start", "error", err)
			if fallback && a.voiceGenerationCurrent(generation) {
				err = voice.Start("", "")
				if err != nil {
					a.log.Error("engine restart on default devices", "error", err)
				}
			}
		}
		a.mu.Lock()
		a.voiceLife.running = err == nil
		a.voiceLife.applied = generation
		a.mu.Unlock()
		// Start cannot be canceled by VoiceEngine's API. If the session changed
		// while it was opening devices, the next pass closes them before exit.
	}
}

func (a *App) voiceGenerationCurrent(generation uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	life := &a.voiceLife
	return !life.closed && life.wanted && life.generation == generation
}

// shutdownVoice rejects further lifecycle work and gives the last worker a
// bounded chance to close devices. Native device open/close cannot be canceled
// and may hang inside the OS. Such a call must not prevent quitting; if it does
// return while the process still lives, the worker finishes the pending stop.
func (a *App) shutdownVoice() {
	a.mu.Lock()
	life := &a.voiceLife
	if !life.closed {
		life.closed = true
		life.wanted = false
		life.fallback = false
		life.generation++
		a.scheduleVoiceLocked()
	}
	done := life.done
	a.mu.Unlock()
	if done != nil {
		timer := time.NewTimer(voiceShutdownGrace)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			a.log.Warn("voice engine shutdown still pending")
		}
	}
}
