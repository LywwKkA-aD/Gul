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
	"github.com/LywwKkA-aD/Gul/internal/hotkey"
	"github.com/LywwKkA-aD/Gul/internal/logging"
	"github.com/LywwKkA-aD/Gul/internal/mumble"
	"github.com/LywwKkA-aD/Gul/internal/tray"
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
	application.RegisterEvent[domain.SelfAudioState](domain.EventAudioSelf)
	application.RegisterEvent[domain.PTTState](domain.EventAudioPTT)
	application.RegisterEvent[domain.SelfTalkingState](domain.EventAudioSelfTalking)
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

func (v *voiceAdapter) SetCueVolume(volume float32) { v.engine.SetCueVolume(volume) }

// PlayCue maps the core vocabulary onto the engine's own. An unknown cue is
// dropped rather than guessed: a wrong beep is worse than a missing one.
func (v *voiceAdapter) PlayCue(c core.Cue) {
	switch c {
	case core.CueJoin:
		v.engine.PlayCue(audio.CueJoin)
	case core.CueLeave:
		v.engine.PlayCue(audio.CueLeave)
	case core.CueMuted:
		v.engine.PlayCue(audio.CueMuted)
	case core.CueUnmuted:
		v.engine.PlayCue(audio.CueUnmuted)
	}
}

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
			OnTalking:     coreApp.HandleTalking,
			OnSelfTalking: coreApp.HandleSelfTalking,
			OnLevels:      coreApp.HandleLevels,
			OnDeviceLost:  coreApp.HandleDeviceLost,
		},
	})
	coreApp.SetVoice(&voiceAdapter{engine: engine})

	// After SetVoice: the stored device selection has to be in place before
	// the engine starts, and the gate settings reach the engine that now
	// exists. A document this build cannot read costs the settings, not the
	// start - core logs what was lost and runs on defaults.
	settings, settingsErr := config.Load(cfgDir)
	coreApp.UseSettings(cfgDir, settings, settingsErr)

	app := application.New(application.Options{
		Name:        "Gul",
		Description: "Voice chat for friends on top of Mumble",
		// Wails logs binding arguments and results at DEBUG. Those may contain
		// join passwords and chat text, so keep framework logs at WARN while
		// retaining Gul's own DEBUG diagnostics.
		Logger:   logging.WithMinimumLevel(logger, slog.LevelWarn),
		LogLevel: slog.LevelWarn,
		// Stops the global key watch and writes a settings change still inside
		// the debounce window, which would otherwise leave with the process.
		// Runs before the services are shut down, on Cmd+Q as well as on a
		// window-driven quit.
		OnShutdown: coreApp.Shutdown,
		Services: []application.Service{
			application.NewService(services.NewConnectionService(coreApp)),
			application.NewService(services.NewChannelsService(coreApp)),
			application.NewService(services.NewChatService(coreApp)),
			application.NewService(services.NewDiagnosticsService(coreApp)),
			application.NewService(services.NewAudioService(coreApp)),
			application.NewService(services.NewSettingsService(coreApp)),
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

	// The Wayland toggle path registers through Wails; every other backend
	// polls key state itself and ignores the registrar. A monitor is always
	// returned - an unsupported platform reports why through the settings
	// screen - so only rejected options are fatal here.
	monitor, err := hotkey.New(hotkey.Options{Registrar: app.GlobalShortcut})
	if err != nil {
		logger.Error("global hotkey monitor", "error", err)
		log.Fatal(err)
	}
	coreApp.SetKeyMonitor(monitor)

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

	setupTray(app, coreApp, win)

	logger.Info("gul starting", "version", core.Version, "config_dir", cfgDir)
	if err := app.Run(); err != nil {
		logger.Error("application exited", "error", err)
		log.Fatal(err)
	}
}

// Tray labels. The rest of the user-facing text lives in core with the state
// it describes.
const (
	trayShowLabel   = "Показать Gul"
	trayMuteLabel   = "Микрофон выключен"
	trayDeafenLabel = "Звук выключен"
	trayQuitLabel   = "Выход"
)

// setupTray builds the system tray and keeps it in step with core. Every item
// goes through core, which is also what the window calls, so the two can never
// disagree about what is muted.
func setupTray(app *application.App, coreApp *core.App, win *application.WebviewWindow) {
	systemTray := app.SystemTray.New()

	menu := app.NewMenu()
	menu.Add(trayShowLabel).OnClick(func(*application.Context) { showWindow(app, win) })
	// Wails flips a checkbox before it calls back, so the clicked state is
	// the request. Core decides what actually happens and reports it back
	// through the observer below, which is what re-syncs a stale checkbox.
	mute := menu.AddCheckbox(trayMuteLabel, false)
	mute.OnClick(func(ctx *application.Context) { coreApp.SetMute(ctx.ClickedMenuItem().Checked()) })
	deafen := menu.AddCheckbox(trayDeafenLabel, false)
	deafen.OnClick(func(ctx *application.Context) { coreApp.SetDeafen(ctx.ClickedMenuItem().Checked()) })
	menu.AddSeparator()
	menu.Add(trayQuitLabel).OnClick(func(*application.Context) { app.Quit() })
	systemTray.SetMenu(menu)

	apply := func(state core.TrayState) {
		mute.SetChecked(state.Muted)
		deafen.SetChecked(state.Deafened)
		systemTray.SetTooltip(state.Tooltip)
		setTrayIcon(systemTray, state.Icon)
	}

	// Before Run the tray has no platform implementation and the setters only
	// record what it will start with. Afterwards each one touches native
	// state, so a change arriving on a service or menu goroutine is marshalled
	// onto the main thread.
	apply(coreApp.TrayState())
	coreApp.OnTrayState(func(state core.TrayState) {
		application.InvokeAsync(func() { apply(state) })
	})
}

// setTrayIcon paints the tray. macOS wants a template image and tints it for
// the menu bar itself; the other platforms paint what they are given, so they
// get a second glyph for dark panels.
func setTrayIcon(systemTray *application.SystemTray, icon core.TrayIcon) {
	muted := icon == core.TrayIconMicMuted
	if runtime.GOOS == "darwin" {
		systemTray.SetTemplateIcon(tray.Icon(muted))
		return
	}
	systemTray.SetIcon(tray.Icon(muted))
	systemTray.SetDarkModeIcon(tray.IconLight(muted))
}

// showWindow brings the window back from wherever it went: hidden by the close
// button (macOS), minimised, or behind another application.
func showWindow(app *application.App, win *application.WebviewWindow) {
	if runtime.GOOS == "darwin" {
		// The window cannot come forward while the application itself is
		// hidden (Cmd+H).
		app.Show()
	}
	if win.IsMinimised() {
		win.Restore()
	}
	win.Show()
	win.Focus()
}
