// Package store persists App Store Connect data in SQLite for Grafana to read.
//
// Each report date is written with delete-then-insert inside a transaction, so
// re-syncing a date exactly mirrors the latest report: restatements overwrite,
// and series that vanished from the report are removed.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite".

	"github.com/rahilb/asc-sqlite-syncer/internal/asc"
)

// Store is a SQLite-backed data store.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sales_units (
	report_date  TEXT NOT NULL,
	app          TEXT NOT NULL,
	sku          TEXT NOT NULL,
	country      TEXT NOT NULL,
	product_type TEXT NOT NULL,
	units        REAL NOT NULL,
	PRIMARY KEY (report_date, app, sku, country, product_type)
);
CREATE INDEX IF NOT EXISTS idx_sales_units_date ON sales_units(report_date);

CREATE TABLE IF NOT EXISTS sales_proceeds (
	report_date TEXT NOT NULL,
	app         TEXT NOT NULL,
	sku         TEXT NOT NULL,
	currency    TEXT NOT NULL,
	proceeds    REAL NOT NULL,
	PRIMARY KEY (report_date, app, sku, currency)
);
CREATE INDEX IF NOT EXISTS idx_sales_proceeds_date ON sales_proceeds(report_date);

CREATE TABLE IF NOT EXISTS active_subscriptions (
	report_date  TEXT NOT NULL,
	app          TEXT NOT NULL,
	subscription TEXT NOT NULL,
	country      TEXT NOT NULL,
	active       REAL NOT NULL,
	PRIMARY KEY (report_date, app, subscription, country)
);
CREATE INDEX IF NOT EXISTS idx_active_subs_date ON active_subscriptions(report_date);

CREATE TABLE IF NOT EXISTS reviews (
	snapshot_date TEXT NOT NULL,
	app           TEXT NOT NULL,
	total         INTEGER NOT NULL,
	rating_avg    REAL NOT NULL,
	PRIMARY KEY (snapshot_date, app)
);

CREATE TABLE IF NOT EXISTS review_ratings (
	snapshot_date TEXT NOT NULL,
	app           TEXT NOT NULL,
	rating        INTEGER NOT NULL,
	count         REAL NOT NULL,
	PRIMARY KEY (snapshot_date, app, rating)
);

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// Open opens (creating if needed) the SQLite database and applies the schema.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite %q: %w", path, err)
	}
	// SQLite is single-writer; serialise to avoid "database is locked".
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// IsBackfilled reports whether the first-run backfill has completed.
func (s *Store) IsBackfilled(ctx context.Context) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='backfilled'`).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == "1", nil
}

// SetBackfilled marks the first-run backfill as complete.
func (s *Store) SetBackfilled(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES('backfilled','1')
		 ON CONFLICT(key) DO UPDATE SET value='1'`)
	return err
}

// ReplaceSales rewrites all sales rows for a report date.
func (s *Store) ReplaceSales(ctx context.Context, date string, d *asc.SalesData) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sales_units WHERE report_date=?`, date); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sales_proceeds WHERE report_date=?`, date); err != nil {
			return err
		}
		for k, v := range d.Units {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO sales_units(report_date, app, sku, country, product_type, units) VALUES(?,?,?,?,?,?)`,
				date, k.App, k.SKU, k.Country, k.ProductType, v); err != nil {
				return err
			}
		}
		for k, v := range d.Proceeds {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO sales_proceeds(report_date, app, sku, currency, proceeds) VALUES(?,?,?,?,?)`,
				date, k.App, k.SKU, k.Currency, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceSubscriptions rewrites all subscription rows for a report date.
func (s *Store) ReplaceSubscriptions(ctx context.Context, date string, d *asc.SubscriptionData) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM active_subscriptions WHERE report_date=?`, date); err != nil {
			return err
		}
		for k, v := range d.Active {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO active_subscriptions(report_date, app, subscription, country, active) VALUES(?,?,?,?,?)`,
				date, k.App, k.Subscription, k.Country, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceReviews rewrites the review snapshot for a date across all apps.
func (s *Store) ReplaceReviews(ctx context.Context, date string, data []*asc.ReviewsData) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM reviews WHERE snapshot_date=?`, date); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM review_ratings WHERE snapshot_date=?`, date); err != nil {
			return err
		}
		for _, d := range data {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO reviews(snapshot_date, app, total, rating_avg) VALUES(?,?,?,?)`,
				date, d.AppName, d.Total, d.RatingAvg); err != nil {
				return err
			}
			for star, count := range d.RatingCounts {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO review_ratings(snapshot_date, app, rating, count) VALUES(?,?,?,?)`,
					date, d.AppName, star, count); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// tx runs fn inside a transaction, committing on success and rolling back on error.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
