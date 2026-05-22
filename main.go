// Command asc-sqlite-syncer syncs App Store Connect sales, subscription
// and review data into a SQLite database for Grafana to query.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rahilb/asc-sqlite-syncer/internal/asc"
	"github.com/rahilb/asc-sqlite-syncer/internal/config"
	"github.com/rahilb/asc-sqlite-syncer/internal/store"
	"github.com/rahilb/asc-sqlite-syncer/internal/syncer"
)

func main() {
	once := flag.Bool("once", false, "run a single sync and exit (for cron); otherwise run as a daemon")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

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

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("store open failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	sync := syncer.New(cfg, client, st, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *once {
		log.Info("running single sync", "db", cfg.DBPath)
		if err := sync.SyncOnce(ctx); err != nil {
			log.Error("sync finished with errors", "err", err)
			os.Exit(1)
		}
		return
	}

	log.Info("starting sync daemon", "db", cfg.DBPath, "interval", cfg.RefreshInterval,
		"sales", cfg.EnableSales, "subscriptions", cfg.EnableSubscriptions, "reviews", cfg.EnableReviews)
	sync.Run(ctx)
	log.Info("shutting down")
}
