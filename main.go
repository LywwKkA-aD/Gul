package main

import (
	"context"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"
	"sync/atomic"

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
	"github.com/LywwKkA-aD/Gul/internal/secret"
	"github.com/LywwKkA-aD/Gul/internal/tray"
	"github.com/LywwKkA-aD/Gul/services"
)

//go:embed all:frontend/dist
var assets embed.FS

// secretService names Gul's items in the operating system's credential store:
// the keychain entry the user sees in Keychain Access, the target prefix in
// the Windows Credential Manager, the attribute a Secret Service item is
// found by. It is the product identifier from build/config.yml, so it is
// unique and stable across releases - changing it would orphan every password
// already stored.
const secretService = "io.github.lywwkkaad.gul"

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
	application.RegisterEvent[domain.UpdateAvailable](domain.EventUpdateAvailable)
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
func (v *voiceAdapter) SetUserMute(hash string, muted bool) {
	v.engine.SetUserMute(hash, muted)
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

	// Passwords of remembered servers live in the operating system's own
	// credential store, never in config.json. A machine without one is
	// supported: the servers are still remembered and the user types the
	// password (internal/secret).
	store := secret.New(secretService)
	coreApp.SetSecrets(store)
	if !store.Available() {
		logger.Warn("no credential store on this machine, server passwords will not be remembered")
	}

	// System notifications for a window nobody is looking at. Deliberately not
	// an application.Service: a service whose startup fails aborts the whole
	// application, and this one fails by design on an unpackaged macOS build
	// (notifier.go).
	notifier := newSystemNotifier(logger)
	coreApp.SetNotifier(notifier)

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
		OnShutdown: func() {
			coreApp.Shutdown()
			notifier.stop()
		},
		Services: []application.Service{
			application.NewService(services.NewConnectionService(coreApp)),
			application.NewService(services.NewChannelsService(coreApp)),
			application.NewService(services.NewChatService(coreApp)),
			application.NewService(services.NewDiagnosticsService(coreApp)),
			application.NewService(services.NewAudioService(coreApp)),
			application.NewService(services.NewSettingsService(coreApp)),
			application.NewService(services.NewUpdateService(coreApp)),
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
	watchWindowAttention(coreApp, win)

	// Two things that must not be on the startup path: the notification
	// backend, which puts a permission dialog on screen on macOS, and the
	// version check, which talks to GitHub. Both run once the window is up and
	// neither is waited for.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go notifier.start(context.Background())
		coreApp.StartUpdateCheck()
	})

	logger.Info("gul starting", "version", core.Version, "config_dir", cfgDir)
	if err := app.Run(); err != nil {
		logger.Error("application exited", "error", err)
		log.Fatal(err)
	}
}

// watchWindowAttention keeps core informed about whether the user is looking
// at the window, which is the whole condition for a system notification
// (internal/notify).
//
// Focus is the load-bearing signal and the one every backend maps onto the
// common events (pkg/events/defaults.go: Mac.WindowDidBecomeKey,
// Windows.WindowSetFocus, Linux.WindowFocusIn). Visibility is tracked beside
// it because a window hidden by the close button on macOS keeps the process
// alive; where a platform does not report hiding, the lost focus that came
// with it already says enough.
//
// The seed is "attended". A platform that never reported focus would then stay
// silent, which is the failure this feature has to fail towards: a missed
// notification is a nuisance, a notification over a window the user is reading
// is a defect.
func watchWindowAttention(coreApp *core.App, win *application.WebviewWindow) {
	var visible, focused atomic.Bool
	visible.Store(true)
	focused.Store(true)

	publish := func() { coreApp.SetWindowState(visible.Load(), focused.Load()) }
	// OnWindowEvent, not RegisterHook: this only observes, and a hook is for
	// changing what the event does.
	track := func(event events.WindowEventType, flag *atomic.Bool, value bool) {
		win.OnWindowEvent(event, func(*application.WindowEvent) {
			flag.Store(value)
			publish()
		})
	}
	track(events.Common.WindowFocus, &focused, true)
	track(events.Common.WindowLostFocus, &focused, false)
	track(events.Common.WindowShow, &visible, true)
	track(events.Common.WindowHide, &visible, false)
	track(events.Common.WindowRestore, &visible, true)
	track(events.Common.WindowMinimise, &visible, false)
	publish()
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
	// The click is a toggle request against what core holds, not against the
	// checkbox: Wails flips its own field on one goroutine and calls back on
	// another while the observer below writes the same field from the main
	// thread, so reading it back would be a three-way race. Core decides and
	// reports through the observer, which is what re-syncs the checkbox.
	mute := menu.AddCheckbox(trayMuteLabel, false)
	mute.OnClick(func(*application.Context) { coreApp.ToggleMute() })
	deafen := menu.AddCheckbox(trayDeafenLabel, false)
	deafen.OnClick(func(*application.Context) { coreApp.ToggleDeafen() })
	menu.AddSeparator()
	menu.Add(trayQuitLabel).OnClick(func(*application.Context) { app.Quit() })
	systemTray.SetMenu(menu)

	apply := func(state core.TrayState) {
		mute.SetChecked(state.Muted)
		deafen.SetChecked(state.Deafened)
		systemTray.SetTooltip(state.Tooltip)
		setTrayIcon(systemTray, state.Icon)
	}

	// Before Run the tray has no platform implementation: the setters only
	// record what it will start with, and InvokeAsync would dereference a nil
	// implementation. Afterwards each setter touches native state, so a change
	// arriving on a service or menu goroutine is marshalled onto the main
	// thread. The flag is what tells the two apart.
	var started atomic.Bool
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		started.Store(true)
	})

	apply(coreApp.TrayState())
	coreApp.OnTrayState(func(state core.TrayState) {
		if !started.Load() {
			apply(state)
			return
		}
		application.InvokeAsync(func() { apply(state) })
	})
}

// setTrayIcon paints the tray.
//
// The icon carries the application's colour rather than being a silhouette the
// system tints, so the panel it lands on is ours to handle - and only one
// platform can be told about both panels.
//
// Windows keeps two images and picks by theme (systemtray_windows.go). macOS
// and Linux keep one: their setDarkModeIcon calls the same setter as the plain
// one (systemtray_darwin.go, systemtray_linux.go), so handing them a second
// image only replaces the first. Those two get the single ink chosen to clear
// both a near-white and a near-black panel.
func setTrayIcon(systemTray *application.SystemTray, icon core.TrayIcon) {
	muted := icon == core.TrayIconMicMuted
	if runtime.GOOS == "windows" {
		systemTray.SetIcon(tray.Icon(tray.PanelLight, muted))
		systemTray.SetDarkModeIcon(tray.Icon(tray.PanelDark, muted))
		return
	}
	systemTray.SetIcon(tray.Icon(tray.PanelEither, muted))
}

// showWindow brings the window back from wherever it went: hidden by the close
// button (macOS), minimised, or behind another application.
//
// The whole body is marshalled onto the main thread, and that is not belt and
// braces. Wails hands a menu click to a fresh goroutine (menuitem.go,
// handleClick), and while every WebviewWindow method marshals itself, App.Show
// does not: it calls a.impl.show() straight through to [NSApp unhide:]
// (application.go, application_darwin.go). AppKit off the main thread is
// undefined behaviour, and the process is what pays for it.
//
// One hop covers everything rather than four. Nesting is safe: Wails runs the
// function inline when it is already on the main thread
// (App.dispatchOnMainThread), so the marshalling the window methods do inside
// this one cannot deadlock against it.
func showWindow(app *application.App, win *application.WebviewWindow) {
	application.InvokeSync(func() {
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
	})
}
