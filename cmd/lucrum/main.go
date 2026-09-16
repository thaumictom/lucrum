package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lucrum/internal/items"
	"lucrum/internal/tradeable"
	"lucrum/internal/upstream"
	"lucrum/internal/warframedata"
)

func main() {
	if err := run(); err != nil {
		slog.Error("app stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Go returns errors as values instead of throwing exceptions as TS often does.
	config, err := items.LoadConfig()
	if err != nil {
		return err
	}
	if config.Debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}
	store, err := items.NewStore(config.DataDir)
	if err != nil {
		return err
	}
	client, err := upstream.New(config.DataDir, config.RequestsPerSecond)
	if err != nil {
		return err
	}
	statistics, err := tradeable.New(config.DataDir, store, client, config.RequestsPerSecond)
	if err != nil {
		return err
	}
	gameData, err := warframedata.New(config.DataDir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.Handle("/warframe/v2/wfm-items", store)
	mux.Handle("/warframe/v2/tradeable-items", statistics)
	mux.Handle("/warframe/v2/items", gameData)
	server := &http.Server{
		// An empty host listens on all interfaces so Coolify's proxy can reach us.
		Addr:              ":3100",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// "go" starts a goroutine, a concurrent task. Channels let us wait for each
	// task to finish, somewhat like awaiting promises in TypeScript.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		statistics.Run(ctx)
	}()
	catalogueDone := make(chan struct{})
	go func() {
		defer close(catalogueDone)
		store.Run(ctx, config.FetchInterval, client)
	}()
	gameDataDone := make(chan struct{})
	go func() {
		defer close(gameDataDone)
		gameData.Run(ctx, config.FetchInterval)
	}()
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ListenAndServe() }()
	slog.Info("starting HTTP server", "address", server.Addr, "fetch_interval", config.FetchInterval)

	var serverErr error
	select {
	case <-ctx.Done():
	case serverErr = <-serverDone:
	}
	stop() // Also cancels an in-flight upstream request.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("graceful shutdown timed out", "error", err)
		server.Close()
	}
	<-workerDone
	<-catalogueDone
	<-gameDataDone
	if errors.Is(serverErr, http.ErrServerClosed) {
		return nil
	}
	return serverErr
}
