package main

import (
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/LywwKkA-aD/Gul/internal/audio"
	"github.com/LywwKkA-aD/Gul/internal/audio/miniaudio"
	"github.com/LywwKkA-aD/Gul/internal/config"
	"github.com/LywwKkA-aD/Gul/internal/core"
	"github.com/LywwKkA-aD/Gul/internal/domain"
	"github.com/LywwKkA-aD/Gul/internal/logging"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
	"github.com/LywwKkA-aD/Gul/services"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[domain.ConnectionStatus](domain.EventConnectionState)
	application.RegisterEvent[domain.ConnectionLatency](domain.EventConnectionLatency)
	application.RegisterEvent[domain.ChannelNode](domain.EventChannelsTree)
	application.RegisterEvent[domain.ChatMessage](domain.EventChatMessage)
	application.RegisterEvent[domain.TofuPrompt](domain.EventTofuMismatch)
	application.RegisterEvent[domain.TalkingEvent](domain.EventUserTalking)
	application.RegisterEvent[domain.AudioLevels](domain.EventAudioLevels)
}

// voiceAdapter binds core.VoiceEngine to the audio engine, translating the
// opaque hex device ids of the UI layer into miniaudio ones.
type voiceAdapter struct {
	engine *audio.Engine
}

func decodeDeviceID(hexID string) (*miniaudio.DeviceID, error) {
	if hexID == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(hexID)
	var id miniaudio.DeviceID
	if err != nil || len(raw) != len(id) {
		return nil, fmt.Errorf("malformed device id %q", hexID)
	}
	copy(id[:], raw)
	return &id, nil
}

func (v *voiceAdapter) Start(captureID, playbackID string) error {
	capID, err := decodeDeviceID(captureID)
	if err != nil {
		return err
	}
	pbID, err := decodeDeviceID(playbackID)
	if err != nil {
		return err
	}
	return v.engine.Start(capID, pbID)
}

func (v *voiceAdapter) Stop()                   { v.engine.Stop() }
func (v *voiceAdapter) SetMute(muted bool)      { v.engine.SetMute(muted) }
func (v *voiceAdapter) SetDeafen(deafened bool) { v.engine.SetDeafen(deafened) }
func (v *voiceAdapter) SetUserVolume(hash string, volume float32) {
	v.engine.SetUserVolume(hash, volume)
}

// SetGateMode maps the validated core mode onto the engine's own type.
func (v *voiceAdapter) SetGateMode(mode core.GateMode) {
	m := audio.GateVAD
	if mode == core.GateModePTT {
		m = audio.GatePTT
	}
	v.engine.SetGateMode(m)
}

func (v *voiceAdapter) SetVADTuning(open, close float32, hangoverMs int) {
	v.engine.SetVADTuning(open, close, hangoverMs)
}

func (v *voiceAdapter) SetPTT(held bool) { v.engine.SetPTT(held) }

func (v *voiceAdapter) Devices() (playback, capture []domain.AudioDevice, err error) {
	pb, cap, err := v.engine.Devices()
	if err != nil {
		return nil, nil, err
	}
	return convertDevices(pb), convertDevices(cap), nil
}

func convertDevices(infos []miniaudio.DeviceInfo) []domain.AudioDevice {
	out := make([]domain.AudioDevice, 0, len(infos))
	for _, d := range infos {
		out = append(out, domain.AudioDevice{
			ID:        hex.EncodeToString(d.ID[:]),
			Name:      d.Name,
			IsDefault: d.IsDefault,
		})
	}
	return out
}

// wailsEmitter adapts application.App events to domain.Emitter. Events fired
// before the app instance exists (never in practice: pushes start after a
// user-driven Connect) are dropped.
type wailsEmitter struct {
	app *application.App
}

func (e *wailsEmitter) Emit(name string, payload any) {
	if e.app != nil {
		e.app.Event.Emit(name, payload)
	}
}

func main() {
	cfgDir, err := config.Dir()
	if err != nil {
		log.Fatal(err)
	}
	logger, closeLog, err := logging.Setup(cfgDir, slog.LevelDebug)
	if err != nil {
		// A config directory that cannot hold gul.log must not cost the user
		// their client: run on stderr only and say so.
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
		closeLog = func() error { return nil }
		slog.SetDefault(logger)
		logger.Warn("file logging unavailable", "error", err)
	}
	defer func() { _ = closeLog() }()

	emitter := &wailsEmitter{}
	coreApp := core.New(logger, emitter)

	manager, err := mumble.NewManager(cfgDir, logger, coreApp.Callbacks())
	if err != nil {
		logger.Error("mumble manager", "error", err)
		log.Fatal(err)
	}
	defer manager.Close()
	coreApp.SetController(manager)

	engine := audio.NewEngine(audio.Config{
		Packets: manager.VoicePackets(),
		Send:    manager.SendVoice,
		Log:     logger,
		Callbacks: audio.Callbacks{
			OnTalking:    coreApp.HandleTalking,
			OnLevels:     coreApp.HandleLevels,
			OnDeviceLost: coreApp.HandleDeviceLost,
		},
	})
	coreApp.SetVoice(&voiceAdapter{engine: engine})

	app := application.New(application.Options{
		Name:        "Gul",
		Description: "Voice chat for friends on top of Mumble",
		// Wails logs binding arguments and results at DEBUG. Those may contain
		// join passwords and chat text, so keep framework logs at WARN while
		// retaining Gul's own DEBUG diagnostics.
		Logger:   logging.WithMinimumLevel(logger, slog.LevelWarn),
		LogLevel: slog.LevelWarn,
		Services: []application.Service{
			application.NewService(services.NewConnectionService(coreApp)),
			application.NewService(services.NewChannelsService(coreApp)),
			application.NewService(services.NewChatService(coreApp)),
			application.NewService(services.NewDiagnosticsService(coreApp)),
			application.NewService(services.NewAudioService(coreApp)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// A voice client must survive its window: quitting is Cmd+Q.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	emitter.app = app

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Gul",
		Width:     1080,
		Height:    680,
		MinWidth:  860,
		MinHeight: 540,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(238, 240, 244),
		URL:              "/",
	})

	// macOS window conventions for a chat client: the close button hides the
	// window while the app (and the connection) keeps living in the dock.
	// Wails' built-in reopen handler shows hidden windows on a dock click;
	// activation via Cmd+Tab is covered here. Cmd+Q terminates natively and
	// never passes through WindowClosing, so it is not affected.
	if runtime.GOOS == "darwin" {
		win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			e.Cancel()
			win.Hide()
		})
		app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) {
			// Restore only a window hidden by the close button; a minimised
			// one keeps native Cmd+M semantics.
			if !win.IsVisible() && !win.IsMinimised() {
				win.Show()
			}
		})
	}

	logger.Info("gul starting", "version", core.Version, "config_dir", cfgDir)
	if err := app.Run(); err != nil {
		logger.Error("application exited", "error", err)
		log.Fatal(err)
	}
}
