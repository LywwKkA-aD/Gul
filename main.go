package main

import (
	"embed"
	"log"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gul/internal/config"
	"gul/internal/core"
	"gul/internal/logging"
	"gul/internal/mumble"
	"gul/services"
)

//go:embed all:frontend/dist
var assets embed.FS

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

	tofu, err := mumble.NewTOFUStore(cfgDir)
	if err != nil {
		logger.Error("tofu store", "error", err)
		log.Fatal(err)
	}
	coreApp := core.New(logger, tofu)

	app := application.New(application.Options{
		Name:        "Gul",
		Description: "Voice chat for friends on top of Mumble",
		Services: []application.Service{
			application.NewService(services.NewConnectionService(coreApp)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Gul",
		Width:  1000,
		Height: 618,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(255, 255, 255),
		URL:              "/",
	})

	logger.Info("gul starting", "config_dir", cfgDir)
	if err := app.Run(); err != nil {
		logger.Error("application exited", "error", err)
		log.Fatal(err)
	}
}
