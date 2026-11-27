// Command meet is FastMeet's Go video-meet server: a landing page that creates
// hard-to-guess room links, a room page that runs peer-to-peer WebRTC in the
// browser, and websocket signaling. No accounts, no recording, and media never
// passes through (or gets logged by) this server.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Version is the release version, set at build time with -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	cfg := loadConfig()
	ice, stopTurn, err := setupICE(cfg)
	if err != nil {
		log.Fatalf("ice setup: %v", err)
	}
	defer stopTurn()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newServer(cfg, ice).routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("fastmeet %s listening on %s (turn=%v)", Version, cfg.Addr, ice.turnEnabled())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
