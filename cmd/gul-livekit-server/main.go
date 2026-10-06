// gul-livekit-server serves the password-protected Gul API behind a local TLS proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LywwKkA-aD/Gul/internal/livekitlab"
)

func main() {
	configPath := flag.String("config", "/etc/gul-livekit/server.json", "private server configuration file")
	flag.Parse()
	cfg, err := livekitlab.LoadPublicConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := livekitlab.NewPublicHandler(cfg, nil)
	if err != nil {
		log.Fatal("invalid public broker configuration")
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		log.Fatal("cannot bind public broker loopback listener")
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 * 1024,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go handler.RunMaintenance(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Print("Gul LiveKit API ready on configured loopback listener")
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("public broker stopped")
	}
}
