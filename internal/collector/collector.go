// Package collector wires the ASC client to Prometheus metrics, refreshing
// them on a background timer and serving the cached values to scrapes.
package collector

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/rahilb/asc-prometheus-exporter/internal/asc"
	"github.com/rahilb/asc-prometheus-exporter/internal/config"
)

// Collector holds the metric vectors and the data sources that feed them.
type Collector struct {
	cfg    *config.Config
	client *asc.Client
	log    *slog.Logger
	reg    *prometheus.Registry

	salesUnits    *prometheus.GaugeVec
	salesProceeds *prometheus.GaugeVec
	activeSubs    *prometheus.GaugeVec
	reviewsTotal  *prometheus.GaugeVec
	ratingAvg     *prometheus.GaugeVec
	ratingCount   *prometheus.GaugeVec
	reportDate    *prometheus.GaugeVec

	refreshSuccess  *prometheus.GaugeVec
	refreshDuration *prometheus.GaugeVec
	refreshTime     *prometheus.GaugeVec
}

// New builds a Collector and registers all metrics.
func New(cfg *config.Config, client *asc.Client, log *slog.Logger) *Collector {
	reg := prometheus.NewRegistry()
	c := &Collector{
		cfg:    cfg,
		client: client,
		log:    log,
		reg:    reg,

		salesUnits: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_sales_units",
			Help: "Units (downloads/sales) from the latest daily sales report.",
		}, []string{"app", "sku", "country", "product_type"}),
		salesProceeds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_sales_proceeds",
			Help: "Developer proceeds from the latest daily sales report, per currency of proceeds.",
		}, []string{"app", "sku", "currency"}),
		activeSubs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_active_subscriptions",
			Help: "Active subscribers from the latest daily subscription report.",
		}, []string{"app", "subscription", "country"}),
		reviewsTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_app_reviews_total",
			Help: "Total number of customer reviews reported by App Store Connect.",
		}, []string{"app"}),
		ratingAvg: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_app_review_rating_avg",
			Help: "Average star rating over the sampled customer reviews.",
		}, []string{"app"}),
		ratingCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_app_reviews_rating_count",
			Help: "Count of sampled customer reviews by star rating.",
		}, []string{"app", "rating"}),
		reportDate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_report_date_seconds",
			Help: "Unix timestamp of the report date that supplied the current data.",
		}, []string{"report"}),

		refreshSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_refresh_success",
			Help: "1 if the last refresh of the source succeeded, else 0.",
		}, []string{"source"}),
		refreshDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_refresh_duration_seconds",
			Help: "Duration of the last refresh of the source.",
		}, []string{"source"}),
		refreshTime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "asc_refresh_timestamp_seconds",
			Help: "Unix timestamp of the last refresh attempt of the source.",
		}, []string{"source"}),
	}

	reg.MustRegister(
		c.salesUnits, c.salesProceeds, c.activeSubs,
		c.reviewsTotal, c.ratingAvg, c.ratingCount, c.reportDate,
		c.refreshSuccess, c.refreshDuration, c.refreshTime,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return c
}

// Registry exposes the registry for the HTTP handler.
func (c *Collector) Registry() *prometheus.Registry { return c.reg }

// Run does an immediate refresh, then refreshes on the configured interval
// until the context is cancelled.
func (c *Collector) Run(ctx context.Context) {
	c.refreshAll(ctx)
	ticker := time.NewTicker(c.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshAll(ctx)
		}
	}
}

func (c *Collector) refreshAll(ctx context.Context) {
	if c.cfg.EnableSales {
		c.runSource(ctx, "sales", c.refreshSales)
	}
	if c.cfg.EnableSubscriptions {
		c.runSource(ctx, "subscriptions", c.refreshSubscriptions)
	}
	if c.cfg.EnableReviews {
		c.runSource(ctx, "reviews", c.refreshReviews)
	}
}

// runSource times a source refresh and records success/duration/timestamp.
func (c *Collector) runSource(ctx context.Context, name string, fn func(context.Context) error) {
	start := time.Now()
	err := fn(ctx)
	c.refreshDuration.WithLabelValues(name).Set(time.Since(start).Seconds())
	c.refreshTime.WithLabelValues(name).Set(float64(time.Now().Unix()))
	if err != nil {
		c.refreshSuccess.WithLabelValues(name).Set(0)
		c.log.Error("refresh failed", "source", name, "err", err)
		return
	}
	c.refreshSuccess.WithLabelValues(name).Set(1)
	c.log.Info("refresh ok", "source", name)
}

func (c *Collector) refreshSales(ctx context.Context) error {
	data, err := c.client.SalesReport(ctx, c.cfg.VendorNumber, c.cfg.SalesReportVersion, c.cfg.ReportLookback)
	if err != nil {
		return err
	}
	// Reset so series that no longer appear in the report disappear.
	c.salesUnits.Reset()
	c.salesProceeds.Reset()
	for k, v := range data.Units {
		c.salesUnits.WithLabelValues(k.App, k.SKU, k.Country, k.ProductType).Set(v)
	}
	for k, v := range data.Proceeds {
		c.salesProceeds.WithLabelValues(k.App, k.SKU, k.Currency).Set(v)
	}
	setReportDate(c.reportDate, "sales", data.Date)
	return nil
}

func (c *Collector) refreshSubscriptions(ctx context.Context) error {
	data, err := c.client.SubscriptionReport(ctx, c.cfg.VendorNumber, c.cfg.SubscriptionReportVersion, c.cfg.ReportLookback)
	if err != nil {
		return err
	}
	c.activeSubs.Reset()
	for k, v := range data.Active {
		c.activeSubs.WithLabelValues(k.App, k.Subscription, k.Country).Set(v)
	}
	setReportDate(c.reportDate, "subscriptions", data.Date)
	return nil
}

func (c *Collector) refreshReviews(ctx context.Context) error {
	c.reviewsTotal.Reset()
	c.ratingAvg.Reset()
	c.ratingCount.Reset()
	var firstErr error
	for _, appID := range c.cfg.AppIDs {
		data, err := c.client.CustomerReviews(ctx, appID, c.cfg.ReviewsMax)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.log.Error("reviews fetch failed", "app", appID, "err", err)
			continue
		}
		c.reviewsTotal.WithLabelValues(data.AppName).Set(float64(data.Total))
		c.ratingAvg.WithLabelValues(data.AppName).Set(data.RatingAvg)
		for star, count := range data.RatingCounts {
			c.ratingCount.WithLabelValues(data.AppName, strconv.Itoa(star)).Set(count)
		}
	}
	return firstErr
}

// setReportDate parses a YYYY-MM-DD report date and records it as a timestamp.
func setReportDate(vec *prometheus.GaugeVec, report, date string) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return
	}
	vec.WithLabelValues(report).Set(float64(t.Unix()))
}
