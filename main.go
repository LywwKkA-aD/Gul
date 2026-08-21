package main

import (
	"embed"
	"log"
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"gul/internal/config"
	"gul/internal/core"
	"gul/internal/logging"
	"gul/internal/mumble"
	"gul/services"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[string]("time")
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
	defer closeLog()

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
			application.NewService(&GreetService{}),
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
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	// Template demo event; removed together with GreetService once the
	// Connect screen lands.
	go func() {
		for {
			app.Event.Emit("time", time.Now().Format(time.RFC1123))
			time.Sleep(time.Second)
		}
	}()

	logger.Info("gul starting", "config_dir", cfgDir)
	if err := app.Run(); err != nil {
		logger.Error("application exited", "error", err)
		log.Fatal(err)
	}
}
