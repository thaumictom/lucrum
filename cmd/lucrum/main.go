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
	store, err := items.NewStore(config.DataDir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:              ":3100",
		Handler:           store,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// "go" starts a goroutine, a concurrent task. Channels let us wait for each
	// task to finish, somewhat like awaiting promises in TypeScript.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		store.Run(ctx, config.FetchInterval)
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
	if errors.Is(serverErr, http.ErrServerClosed) {
		return nil
	}
	return serverErr
}
