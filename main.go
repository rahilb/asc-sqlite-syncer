// Command asc-prometheus-exporter exposes App Store Connect sales, subscription
// and review metrics in Prometheus format.
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

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rahilb/asc-prometheus-exporter/internal/asc"
	"github.com/rahilb/asc-prometheus-exporter/internal/collector"
	"github.com/rahilb/asc-prometheus-exporter/internal/config"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config error", "err", err)
		os.Exit(1)
	}

	ts, err := asc.NewTokenSource(cfg.KeyID, cfg.IssuerID, cfg.PrivateKey)
	if err != nil {
		log.Error("auth setup failed", "err", err)
		os.Exit(1)
	}
	client := asc.New(ts, cfg.HTTPTimeout)
	coll := collector.New(cfg, client, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go coll.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(coll.Registry(), promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>ASC Exporter</title></head>` +
			`<body><h1>App Store Connect Exporter</h1><a href="/metrics">/metrics</a></body></html>`))
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.ListenAddr,
			"sales", cfg.EnableSales, "subscriptions", cfg.EnableSubscriptions, "reviews", cfg.EnableReviews)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
}
