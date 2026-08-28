package audio

import (
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/audio/miniaudio"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
)

// FrameSource and FrameSink abstract the audio devices so the whole
// pipeline runs in tests on synthetic frames (PLAN.md 4.6).
type FrameSource interface {
	ReadFrame(dst []int16) bool
}

type FrameSink interface {
	WriteFrame(src []int16) bool
}

// Callbacks are invoked from the DSP goroutine and must return quickly
// (hand off to a channel or an atomic, never block).
type Callbacks struct {
	// OnTalking reports a remote stream starting or stopping.
	OnTalking func(session uint32, key string, talking bool)
	// OnSelfTalking reports our own transmission starting or stopping. Our
	// voice never comes back from the server, so this is the only source of
	// the local speaking indication.
	OnSelfTalking func(talking bool)
	// OnLevels reports mic and output levels in dBFS every ~50 ms.
	OnLevels func(micDB, outDB float64)
	// OnDeviceLost fires once when a device stops underneath the engine
	// (unplug, backend error); the owner restarts the engine.
	OnDeviceLost func()
}

// Config wires the engine to the voice transport.
type Config struct {
	// Packets delivers incoming raw Opus packets (mumble passthrough).
	Packets <-chan mumble.VoicePacket
	// Send transmits one encoded frame; it takes ownership of the bytes.
	Send func(opus []byte, final bool) error
	// Bitrate is the encoder target (server MaxBitrate already clamped).
	Bitrate int
	// DSP selects the processing shape; nil means DefaultDSP. Changing the
	// shape means restarting the engine - DSP states are not hot-swapped.
	DSP *DSPOptions

	Log       *slog.Logger
	Callbacks Callbacks
}

// Engine runs the M2 voice pipeline: capture -> opus -> transport and
// transport -> jitter -> mixer -> playback. DSP state lives on one locked
// goroutine; the public methods only flip atomics or queue intents.
type Engine struct {
	cfg Config

	mu       sync.Mutex
	ctx      *miniaudio.Context
	capture  *miniaudio.Capture
	playback *miniaudio.Playback
	stop     chan struct{}
	done     chan struct{}

	state engineState
}

// engineState is the cross-goroutine control surface of the DSP loop.
type engineState struct {
	muted    atomic.Bool
	deafened atomic.Bool
	ptt      atomic.Bool
	// users is the per-peer treatment: gain and local mute, keyed by the
	// peer key (users.go, mumble/peerkey.go).
	users userAudioState

	// cue is the pending UI sound, encoded as Cue+1 so that 0 means empty.
	// PlayCue fills it from any goroutine, the DSP goroutine drains it.
	cue atomic.Int32
	// cueVol is the cue gain as float32 bits (math.Float32bits).
	cueVol atomic.Uint32

	gateMu sync.Mutex
	gate   gateSettings
}

// gateSettings is the UI-facing gate configuration, applied to the Gate on
// every tick (the setters are cheap and idempotent).
type gateSettings struct {
	mode       GateMode
	open       float32
	close      float32
	hangoverMs int
}

// NewEngine builds an engine over the given transport config.
func NewEngine(cfg Config) *Engine {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = 40000
	}
	e := &Engine{cfg: cfg}
	e.state.gate = gateSettings{
		mode:       GateVAD,
		open:       gateOpenDefault,
		close:      gateCloseDefault,
		hangoverMs: gateHangoverDefaultMs,
	}
	e.SetCueVolume(cueVolumeDefault)
	return e
}

// Devices enumerates playback and capture devices for the settings UI.
func (e *Engine) Devices() (playback, capture []miniaudio.DeviceInfo, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx != nil {
		return e.ctx.Devices()
	}
	ctx, err := miniaudio.NewContext()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = ctx.Close() }()
	return ctx.Devices()
}

// Start opens the devices (nil ids = system defaults) and launches the DSP
// goroutine. The engine runs until Stop.
func (e *Engine) Start(captureID, playbackID *miniaudio.DeviceID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done != nil {
		return nil // already running
	}
	ctx, err := miniaudio.NewContext()
	if err != nil {
		return err
	}
	const ringFrames = 16 // 160 ms of slack between callback and DSP tick
	capDev, err := ctx.OpenCapture(captureID, ringFrames)
	if err != nil {
		_ = ctx.Close()
		return err
	}
	pbDev, err := ctx.OpenPlayback(playbackID, ringFrames)
	if err != nil {
		capDev.Close()
		_ = ctx.Close()
		return err
	}
	if err := capDev.Start(); err != nil {
		e.closeDevicesLocked(ctx, capDev, pbDev)
		return err
	}
	if err := pbDev.Start(); err != nil {
		e.closeDevicesLocked(ctx, capDev, pbDev)
		return err
	}
	if rate := capDev.InternalSampleRate(); rate != SampleRate {
		e.cfg.Log.Warn("capture resampled by miniaudio", "device_rate", rate)
	}
	if rate := pbDev.InternalSampleRate(); rate != SampleRate {
		e.cfg.Log.Warn("playback resampled by miniaudio", "device_rate", rate)
	}

	e.ctx, e.capture, e.playback = ctx, capDev, pbDev
	e.stop = make(chan struct{})
	e.done = make(chan struct{})
	go e.run(capDev, pbDev, e.stop, e.done)
	e.cfg.Log.Info("voice engine started")
	return nil
}

func (e *Engine) closeDevicesLocked(ctx *miniaudio.Context, devs ...interface{ Close() }) {
	for _, d := range devs {
		d.Close()
	}
	_ = ctx.Close()
}

// Stop terminates the DSP goroutine and releases the devices.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done == nil {
		return
	}
	close(e.stop)
	<-e.done
	e.dropPendingCue()
	e.capture.Close()
	e.playback.Close()
	_ = e.ctx.Close()
	e.ctx, e.capture, e.playback = nil, nil, nil
	e.stop, e.done = nil, nil
	e.cfg.Log.Info("voice engine stopped")
}

// SetMute stops sending mic frames (a terminator closes the transmission).
func (e *Engine) SetMute(muted bool) { e.state.muted.Store(muted) }

// SetDeafen stops mixing remote streams (silence keeps flowing to the
// device so the ring stays warm).
func (e *Engine) SetDeafen(deafened bool) { e.state.deafened.Store(deafened) }

// SetUserVolume sets a per-user gain (1.0 = unity) keyed by the stable
// peer key; survives the peer reconnecting when the key is a certificate hash.
//
// It does not un-silence anybody: a listener who muted this peer and then
// moved their slider has changed what they will hear on unmute, not asked to
// hear them now.
func (e *Engine) SetUserVolume(key string, volume float32) {
	if volume < 0 {
		volume = 0
	}
	if volume > 4 {
		volume = 4
	}
	e.state.users.setVolume(key, volume)
}

// SetUserMute silences one peer locally, or lets them back in, keyed by the
// same certificate hash as the gain.
//
// This is not "volume zero": the gain the listener chose is kept untouched
// and is exactly what they hear again on unmute. It is also not the Mumble
// mute on the wire - nothing is sent, and the person is never told.
// ForgetAbsentPeers drops the settings of peers who are no longer in the room,
// and only the ones that were never meant to outlive the session.
//
// The room is what decides. A departure event that never arrives - a dropped
// connection, a reconnect that rebuilt the tree from scratch - would leave the
// entry behind forever, and Murmur hands session ids out again, so the volume
// somebody set for a stranger would become the volume of the next stranger to
// be given that number.
func (e *Engine) ForgetAbsentPeers(present map[string]bool) {
	e.state.users.keep(present)
}

func (e *Engine) SetUserMute(key string, muted bool) {
	e.state.users.setMuted(key, muted)
}

// SetPTT reports whether the push-to-talk key is currently held. Only
// relevant while the gate runs in GatePTT mode.
func (e *Engine) SetPTT(held bool) { e.state.ptt.Store(held) }

// SetGateMode switches the transmit gate between VAD and push-to-talk.
func (e *Engine) SetGateMode(m GateMode) {
	if m != GateVAD && m != GatePTT {
		return
	}
	e.state.gateMu.Lock()
	e.state.gate.mode = m
	e.state.gateMu.Unlock()
}

// SetVADTuning sets the gate hysteresis band and hangover. Values are
// validated by the Gate itself on apply.
func (e *Engine) SetVADTuning(open, close float32, hangoverMs int) {
	e.state.gateMu.Lock()
	e.state.gate.open, e.state.gate.close = open, close
	e.state.gate.hangoverMs = hangoverMs
	e.state.gateMu.Unlock()
}

// applyGateSettings pushes the UI-facing configuration onto the gate; runs
// on the DSP goroutine every tick.
func (e *Engine) applyGateSettings(g *Gate) {
	e.state.gateMu.Lock()
	s := e.state.gate
	e.state.gateMu.Unlock()
	g.SetMode(s.mode)
	g.SetThresholds(s.open, s.close)
	g.SetHangoverMs(s.hangoverMs)
}

// run is the DSP goroutine (PLAN.md 4.3-4.4).
func (e *Engine) run(src FrameSource, sink FrameSink, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	opts := DefaultDSP()
	if e.cfg.DSP != nil {
		opts = *e.cfg.DSP
	}
	chain, err := newDSPChain(opts, e.cfg.Log)
	if err != nil {
		e.cfg.Log.Error("dsp chain", "error", err)
		return
	}
	defer chain.close()
	// Standing estimate of the playback path (device period plus typical
	// ring occupancy); AEC3 refines the true delay from the signals.
	chain.delayHint(40)

	var gate *Gate
	if opts.Gate {
		gate = NewGate()
	}
	tx, err := newTxPipeline(e.cfg, chain, gate)
	if err != nil {
		e.cfg.Log.Error("tx pipeline", "error", err)
		return
	}
	defer tx.close()
	rx := newRxPipeline(e.cfg, chain)
	defer rx.close()

	drift := NewDrift()
	ticker := time.NewTicker(FrameMs * time.Millisecond)
	defer ticker.Stop()

	tick := 0
	deviceLost, lostReported := false, false
	var lastUnderruns uint64
	var lastVoice VoiceVitals
	pendingSilence := 0
	for {
		select {
		case <-stop:
			tx.finish()
			return
		case <-ticker.C:
		}
		tick++

		if gate != nil {
			e.applyGateSettings(gate)
		}
		e.applyCue(&rx.cues)

		rx.drain(e.cfg.Packets)
		tx.tick(src, e.state.muted.Load(), e.state.ptt.Load())
		// Underrun padding is replayed into the AEC reference one frame per
		// tick, so a burst of them cannot stall the loop.
		extraSilence := 0
		if pendingSilence > 0 {
			extraSilence = 1
			pendingSilence--
		}
		rx.tick(sink, e.state.deafened.Load(), &e.state.users, extraSilence)

		if tick%5 == 0 && e.cfg.Callbacks.OnLevels != nil {
			e.cfg.Callbacks.OnLevels(DBFS(tx.micRMS), DBFS(rx.outRMS))
		}
		if tick%100 == 0 {
			if dev, ok := src.(interface{ Stats() miniaudio.Stats }); ok {
				st := dev.Stats()
				drift.Sample(st.CallbackFrames, time.Now())
				deviceLost = deviceLost || st.Stopped
			}
			if dev, ok := sink.(interface{ Stats() miniaudio.Stats }); ok {
				st := dev.Stats()
				deviceLost = deviceLost || st.Stopped
				if st.Underruns > lastUnderruns {
					// Cap the backlog: after a long stall AEC3 re-converges
					// anyway, replaying seconds of silence buys nothing.
					pendingSilence = min(pendingSilence+int(st.Underruns-lastUnderruns), 30)
					lastUnderruns = st.Underruns
				}
			}
			if deviceLost && !lostReported {
				lostReported = true
				e.cfg.Log.Warn("audio device stopped underneath the engine")
				if cb := e.cfg.Callbacks.OnDeviceLost; cb != nil {
					go cb()
				}
			}
		}
		// The receive panel, on the same cadence as the connection panel in
		// mumble/manager.go, so the two interleave in a diagnostics archive.
		// Reading them together is the whole point: a socket that carried
		// every byte while the listener heard gaps is a different fault from
		// one that stopped carrying, and until now only the socket had
		// numbers.
		rx.logVitals(tick, &lastVoice)
		if tick%1000 == 0 {
			if ppm, ok := drift.PPM(); ok {
				e.cfg.Log.Debug("capture clock drift", "ppm", int(ppm))
			}
		}
	}
}
