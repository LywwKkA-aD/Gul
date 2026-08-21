package main

import (
	"embed"
	"log"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gul/internal/config"
	"gul/internal/core"
	"gul/internal/domain"
	"gul/internal/logging"
	"gul/internal/mumble"
	"gul/services"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[domain.ConnectionStatus](domain.EventConnectionState)
	application.RegisterEvent[domain.ChannelNode](domain.EventChannelsTree)
	application.RegisterEvent[domain.ChatMessage](domain.EventChatMessage)
	application.RegisterEvent[domain.TofuPrompt](domain.EventTofuMismatch)
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
		log.Fatal(err)
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

	app := application.New(application.Options{
		Name:        "Gul",
		Description: "Voice chat for friends on top of Mumble",
		Logger:      logger,
		LogLevel:    slog.LevelWarn,
		Services: []application.Service{
			application.NewService(services.NewConnectionService(coreApp)),
			application.NewService(services.NewChannelsService(coreApp)),
			application.NewService(services.NewChatService(coreApp)),
			application.NewService(services.NewDiagnosticsService(coreApp)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	emitter.app = app

	app.Window.NewWithOptions(application.WebviewWindowOptions{
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

	logger.Info("gul starting", "version", core.Version, "config_dir", cfgDir)
	if err := app.Run(); err != nil {
		logger.Error("application exited", "error", err)
		log.Fatal(err)
	}
}
