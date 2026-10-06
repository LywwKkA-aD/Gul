package main

import (
	"log/slog"

	"github.com/LywwKkA-aD/Gul/services"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Set to 1 by the standalone lab packager, including launches from Finder.
var liveKitLabBuild string

// runLiveKitLab intentionally creates no core.App: a second local test window
// must not read/write the user's voice settings or start Mumble/audio/hotkeys.
func runLiveKitLab() error {
	app := application.New(application.Options{
		Name:        "Gul LiveKit Lab",
		Description: "Local screen sharing experiment",
		LogLevel:    slog.LevelWarn,
		Services:    []application.Service{application.NewService(services.NewScreenShareLabService())},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Gul — локальный LiveKit",
		Width: 1080, Height: 760, MinWidth: 800, MinHeight: 540,
		URL:              "/#livekit-native",
		BackgroundColour: application.NewRGB(238, 240, 244),
	})
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { go app.Quit() })
	return app.Run()
}
