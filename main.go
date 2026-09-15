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

	"github.com/thaumictom/lucrum/internal/clients"
	"github.com/thaumictom/lucrum/internal/config"
	"github.com/thaumictom/lucrum/internal/httpapi"
	"github.com/thaumictom/lucrum/internal/storage"
	"github.com/thaumictom/lucrum/internal/workers"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	store, err := storage.New(cfg.DataDir, log, time.Now())
	if err != nil {
		log.Error("cannot open storage", "error", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	service := workers.New(store, clients.NewMarket(cfg.RequestsPerSecond), clients.NewWFCD(), cfg, log)
	done := make(chan struct{})
	go func() { service.Run(ctx); close(done) }()
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: httpapi.New(store),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	serverError := make(chan error, 1)
	go func() { serverError <- server.ListenAndServe() }()
	log.Info("Lucrum listening", "address", cfg.HTTPAddr, "data_dir", cfg.DataDir)
	failed := false
	select {
	case <-ctx.Done():
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP server failed", "error", err)
			failed = true
		}
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if err := server.Shutdown(shutdown); err != nil {
		log.Warn("HTTP shutdown", "error", err)
		server.Close()
	}
	<-done
	if failed {
		os.Exit(1)
	}
}
