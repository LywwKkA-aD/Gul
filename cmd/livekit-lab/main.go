// livekit-lab serves the local screen-sharing experiment and its token broker.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/livekitlab"
)

func main() {
	initDir := flag.String("init", "", "initialize private local credentials in this directory and exit")
	configPath := flag.String("config", "bin/livekit-local/broker.json", "private local credentials file")
	webDir := flag.String("web-dir", "frontend/dist", "built frontend directory")
	flag.Parse()
	if *initDir != "" {
		if err := livekitlab.Initialize(*initDir); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Local LiveKit configuration ready.")
		return
	}
	cfg, err := livekitlab.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(*webDir, "index.html")); err != nil {
		log.Fatal("frontend build missing; run npm --prefix frontend run build")
	}
	handler, err := livekitlab.NewHandler(cfg, os.DirFS(*webDir))
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp4", livekitlab.ListenAddress)
	if err != nil {
		log.Fatal("cannot bind local broker on ", livekitlab.ListenAddress)
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 * 1024,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Println("Local LiveKit lab: http://127.0.0.1:8787/#livekit")
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("local broker stopped")
	}
}
