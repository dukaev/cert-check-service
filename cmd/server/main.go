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

	"github.com/dukaev/cert-check-service/internal/handler"
	"github.com/dukaev/cert-check-service/internal/storage"
)

const (
	shutdownTimeout = 10 * time.Second
	maxHeaderBytes  = 4 * 1024 // 4 KB — defends against header-spam DoS
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store := storage.NewMemoryStore()
	store.Seed()
	h := handler.New(store, handler.RealClock{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Liveness: process is alive. Always 200 — do NOT include backend checks here,
		// otherwise a Redis blip would kill the pod instead of marking it not-ready.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", h.Ready)
	mux.HandleFunc("GET /api/v1/check", h.Check)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler.WithRequestID(handler.AccessLog(logger, "/healthz", "/readyz")(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	// Signal-driven graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("server starting", "addr", addr)
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-serveErr:
		if err != nil {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "err", err)
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}
