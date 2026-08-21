package core

import "github.com/LywwKkA-aD/Gul/internal/domain"

// VoiceEngine is what core needs from the audio engine; the adapter in
// main.go binds it to internal/audio and the device layer. Device IDs are
// opaque hex strings ("" = system default).
type VoiceEngine interface {
	Start(captureID, playbackID string) error
	Stop()
	SetMute(muted bool)
	SetDeafen(deafened bool)
	SetUserVolume(hash string, volume float32)
	Devices() (playback, capture []domain.AudioDevice, err error)
}

// SetVoice injects the voice engine. Call before Connect.
func (a *App) SetVoice(v VoiceEngine) {
	a.mu.Lock()
	a.voice = v
	a.mu.Unlock()
}

func (a *App) voiceEngine() VoiceEngine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.voice
}

// startVoice launches the engine with the currently selected devices.
func (a *App) startVoice() {
	v := a.voiceEngine()
	if v == nil {
		return
	}
	a.mu.Lock()
	capID, pbID := a.captureID, a.playbackID
	a.mu.Unlock()
	go func() {
		if err := v.Start(capID, pbID); err != nil {
			a.log.Error("voice engine start", "error", err)
		}
	}()
}

func (a *App) stopVoice() {
	if v := a.voiceEngine(); v != nil {
		go v.Stop()
	}
}

// SetMute gates the microphone; the engine closes the transmission.
func (a *App) SetMute(muted bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetMute(muted)
	}
}

// SetDeafen silences all remote streams locally.
func (a *App) SetDeafen(deafened bool) {
	if v := a.voiceEngine(); v != nil {
		v.SetDeafen(deafened)
	}
}

// SetUserVolume sets the per-user gain by the stable certificate hash, so
// it survives the peer reconnecting.
func (a *App) SetUserVolume(hash string, volume float32) {
	if v := a.voiceEngine(); v != nil {
		v.SetUserVolume(hash, volume)
	}
}

// AudioDevices enumerates devices for the settings UI.
func (a *App) AudioDevices() (playback, capture []domain.AudioDevice, err error) {
	v := a.voiceEngine()
	if v == nil {
		return nil, nil, nil
	}
	return v.Devices()
}

// SelectDevices remembers the chosen devices and restarts the engine when
// it is running ("" keeps the system default).
func (a *App) SelectDevices(captureID, playbackID string) {
	a.mu.Lock()
	a.captureID, a.playbackID = captureID, playbackID
	running := a.status.State == domain.StateConnected || a.status.State == domain.StateReconnecting
	a.mu.Unlock()
	if v := a.voiceEngine(); v != nil && running {
		go func() {
			v.Stop()
			if err := v.Start(captureID, playbackID); err != nil {
				a.log.Error("voice engine restart", "error", err)
			}
		}()
	}
}

// HandleDeviceLost restarts the engine on the currently selected devices
// after one of them stopped (unplug, backend error). Falls back to the
// system defaults when the restart fails.
func (a *App) HandleDeviceLost() {
	v := a.voiceEngine()
	if v == nil {
		return
	}
	a.mu.Lock()
	capID, pbID := a.captureID, a.playbackID
	a.mu.Unlock()
	a.log.Warn("audio device lost, restarting engine")
	v.Stop()
	if err := v.Start(capID, pbID); err != nil {
		a.log.Error("engine restart on selected devices", "error", err)
		if err := v.Start("", ""); err != nil {
			a.log.Error("engine restart on default devices", "error", err)
		}
	}
}

// HandleTalking forwards a talking transition from the DSP goroutine to
// the UI. Must stay fast.
func (a *App) HandleTalking(session uint32, hash string, talking bool) {
	a.emit(domain.EventUserTalking, domain.TalkingEvent{
		Session: session,
		Hash:    hash,
		Talking: talking,
	})
}

// HandleLevels forwards meter values (already throttled by the engine).
func (a *App) HandleLevels(micDb, outDb float64) {
	a.emit(domain.EventAudioLevels, domain.AudioLevels{MicDb: micDb, OutDb: outDb})
}
