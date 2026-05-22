// Package config loads exporter configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the sync daemon.
type Config struct {
	// App Store Connect API credentials.
	KeyID      string // ASC API key ID (the "kid").
	IssuerID   string // ASC API issuer ID.
	PrivateKey []byte // Contents of the .p8 private key (PKCS#8 PEM).

	// Reporting targets.
	VendorNumber string   // Vendor number for sales/subscription reports.
	AppIDs       []string // App Apple IDs for the reviews source.

	// Source toggles.
	EnableSales         bool
	EnableSubscriptions bool
	EnableReviews       bool

	// Report versions (Apple bumps these; the API error lists valid values).
	SalesReportVersion        string
	SubscriptionReportVersion string

	// Storage and sync behaviour.
	DBPath          string        // Path to the SQLite database file.
	BackfillDays    int           // Days of history to pull on the first run.
	ResyncDays      int           // Recent days re-pulled each run (catches restatements).
	RefreshInterval time.Duration // How often the daemon syncs.
	ReviewsMax      int           // Max reviews to page through per app.
	HTTPTimeout     time.Duration
}

// Load reads configuration from the environment, applying defaults and
// validating required fields.
func Load() (*Config, error) {
	c := &Config{
		KeyID:                     os.Getenv("ASC_KEY_ID"),
		IssuerID:                  os.Getenv("ASC_ISSUER_ID"),
		VendorNumber:              os.Getenv("ASC_VENDOR_NUMBER"),
		AppIDs:                    splitList(os.Getenv("ASC_APP_IDS")),
		EnableSales:               envBool("ENABLE_SALES", true),
		EnableSubscriptions:       envBool("ENABLE_SUBSCRIPTIONS", true),
		EnableReviews:             envBool("ENABLE_REVIEWS", true),
		SalesReportVersion:        envStr("SALES_REPORT_VERSION", "1_1"),
		SubscriptionReportVersion: envStr("SUBSCRIPTION_REPORT_VERSION", "1_4"),
		DBPath:                    envStr("DB_PATH", "asc.db"),
		BackfillDays:              envInt("BACKFILL_DAYS", 90),
		ResyncDays:                envInt("RESYNC_DAYS", 5),
		RefreshInterval:           envDuration("REFRESH_INTERVAL", 6*time.Hour),
		ReviewsMax:                envInt("REVIEWS_MAX", 1000),
		HTTPTimeout:               envDuration("HTTP_TIMEOUT", 60*time.Second),
	}

	key, err := loadPrivateKey()
	if err != nil {
		return nil, err
	}
	c.PrivateKey = key

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.KeyID == "" {
		missing = append(missing, "ASC_KEY_ID")
	}
	if c.IssuerID == "" {
		missing = append(missing, "ASC_ISSUER_ID")
	}
	if len(c.PrivateKey) == 0 {
		missing = append(missing, "ASC_PRIVATE_KEY_PATH or ASC_PRIVATE_KEY")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}

	if (c.EnableSales || c.EnableSubscriptions) && c.VendorNumber == "" {
		return fmt.Errorf("ASC_VENDOR_NUMBER is required when sales or subscription sources are enabled")
	}
	if c.EnableReviews && len(c.AppIDs) == 0 {
		return fmt.Errorf("ASC_APP_IDS is required when the reviews source is enabled")
	}
	if !c.EnableSales && !c.EnableSubscriptions && !c.EnableReviews {
		return fmt.Errorf("all sources disabled: nothing to sync")
	}
	if c.BackfillDays < 1 {
		c.BackfillDays = 1
	}
	if c.ResyncDays < 1 {
		c.ResyncDays = 1
	}
	return nil
}

func loadPrivateKey() ([]byte, error) {
	if path := os.Getenv("ASC_PRIVATE_KEY_PATH"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading ASC_PRIVATE_KEY_PATH: %w", err)
		}
		return b, nil
	}
	if raw := os.Getenv("ASC_PRIVATE_KEY"); raw != "" {
		// Allow keys passed with escaped newlines (common in env managers).
		return []byte(strings.ReplaceAll(raw, "\\n", "\n")), nil
	}
	return nil, nil
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
