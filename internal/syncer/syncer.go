// Package syncer drives the periodic pull of App Store Connect data into the
// store. On the first run it backfills history; thereafter it re-pulls a recent
// window each cycle so Apple's restatements are picked up.
package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rahilb/asc-sqlite-syncer/internal/asc"
	"github.com/rahilb/asc-sqlite-syncer/internal/config"
	"github.com/rahilb/asc-sqlite-syncer/internal/store"
)

// Syncer pulls ASC data into the store on a schedule.
type Syncer struct {
	cfg    *config.Config
	client *asc.Client
	store  *store.Store
	log    *slog.Logger
}

// New builds a Syncer.
func New(cfg *config.Config, client *asc.Client, st *store.Store, log *slog.Logger) *Syncer {
	return &Syncer{cfg: cfg, client: client, store: st, log: log}
}

// Run syncs immediately, then on the configured interval until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) {
	if err := s.SyncOnce(ctx); err != nil {
		s.log.Error("initial sync had errors", "err", err)
	}
	ticker := time.NewTicker(s.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SyncOnce(ctx); err != nil {
				s.log.Error("sync had errors", "err", err)
			}
		}
	}
}

// SyncOnce performs a single sync pass. It logs and continues past per-date and
// per-source errors, returning the first error seen (nil if all succeeded).
func (s *Syncer) SyncOnce(ctx context.Context) error {
	backfilled, err := s.store.IsBackfilled(ctx)
	if err != nil {
		return fmt.Errorf("checking backfill state: %w", err)
	}

	days := s.cfg.ResyncDays
	if !backfilled {
		days = s.cfg.BackfillDays
		s.log.Info("first run: backfilling", "days", days)
	}

	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Report dates lag, so start at yesterday and walk back, oldest first.
	for i := days; i >= 1; i-- {
		date := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		if s.cfg.EnableSales {
			note(s.syncSales(ctx, date))
		}
		if s.cfg.EnableSubscriptions {
			note(s.syncSubscriptions(ctx, date))
		}
	}

	// Reviews are not date-keyed; store a snapshot stamped with today's date.
	if s.cfg.EnableReviews {
		note(s.syncReviews(ctx))
	}

	// Mark backfill done even if some dates 404'd; the window has been covered.
	if !backfilled {
		if err := s.store.SetBackfilled(ctx); err != nil {
			note(fmt.Errorf("marking backfilled: %w", err))
		}
	}
	if firstErr == nil {
		s.log.Info("sync complete", "days", days)
	}
	return firstErr
}

func (s *Syncer) syncSales(ctx context.Context, date string) error {
	data, found, err := s.client.SalesReportForDate(ctx, s.cfg.VendorNumber, s.cfg.SalesReportVersion, date)
	if err != nil {
		s.log.Error("sales fetch failed", "date", date, "err", err)
		return err
	}
	if !found {
		return nil
	}
	if err := s.store.ReplaceSales(ctx, date, data); err != nil {
		s.log.Error("sales store failed", "date", date, "err", err)
		return err
	}
	s.log.Debug("sales synced", "date", date, "units_series", len(data.Units))
	return nil
}

func (s *Syncer) syncSubscriptions(ctx context.Context, date string) error {
	data, found, err := s.client.SubscriptionReportForDate(ctx, s.cfg.VendorNumber, s.cfg.SubscriptionReportVersion, date)
	if err != nil {
		s.log.Error("subscriptions fetch failed", "date", date, "err", err)
		return err
	}
	if !found {
		return nil
	}
	if err := s.store.ReplaceSubscriptions(ctx, date, data); err != nil {
		s.log.Error("subscriptions store failed", "date", date, "err", err)
		return err
	}
	s.log.Debug("subscriptions synced", "date", date, "series", len(data.Active))
	return nil
}

func (s *Syncer) syncReviews(ctx context.Context) error {
	today := time.Now().UTC().Format("2006-01-02")
	var collected []*asc.ReviewsData
	var firstErr error
	for _, appID := range s.cfg.AppIDs {
		data, err := s.client.CustomerReviews(ctx, appID, s.cfg.ReviewsMax)
		if err != nil {
			s.log.Error("reviews fetch failed", "app", appID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		collected = append(collected, data)
	}
	if len(collected) > 0 {
		if err := s.store.ReplaceReviews(ctx, today, collected); err != nil {
			s.log.Error("reviews store failed", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
